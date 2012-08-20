package main

import (
	"bytes"
	"fmt"
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"os"
)

const (
	tabstop_length            = 8
	view_vertical_threshold   = 5
	view_horizontal_threshold = 10
)

//----------------------------------------------------------------------------
// view_tree
//----------------------------------------------------------------------------

type view_tree struct {
	// At the same time only one of these groups can be valid:
	// 1) 'left', 'right' and 'split'
	// 2) 'top', 'bottom' and 'split'
	// 3) 'leaf'
	parent     *view_tree
	left       *view_tree
	top        *view_tree
	right      *view_tree
	bottom     *view_tree
	leaf       *view
	split      float32
	tulib.Rect // updated with 'resize' call
}

func new_view_tree_leaf(parent *view_tree, v *view) *view_tree {
	return &view_tree{
		parent: parent,
		leaf:   v,
	}
}

func (v *view_tree) split_vertically() {
	top := v.leaf
	bottom := new_view(top.sr, top.buf)
	*v = view_tree{
		parent: v.parent,
		top:    new_view_tree_leaf(v, top),
		bottom: new_view_tree_leaf(v, bottom),
		split:  0.5,
	}
}

func (v *view_tree) split_horizontally() {
	left := v.leaf
	right := new_view(left.sr, left.buf)
	*v = view_tree{
		parent: v.parent,
		left:   new_view_tree_leaf(v, left),
		right:  new_view_tree_leaf(v, right),
		split:  0.5,
	}
}

func (v *view_tree) draw() {
	if v.leaf != nil {
		v.leaf.draw()
		return
	}

	if v.left != nil {
		v.left.draw()
		v.right.draw()
	} else {
		v.top.draw()
		v.bottom.draw()
	}
}

func (v *view_tree) resize(pos tulib.Rect) {
	v.Rect = pos
	if v.leaf != nil {
		v.leaf.resize(pos.Width, pos.Height)
		return
	}

	if v.left != nil {
		// horizontal split, use 'w'
		w := pos.Width
		if w > 0 {
			// reserve one line for splitter, if we have one line
			w--
		}
		lw := int(float32(w) * v.split)
		rw := w - lw
		v.left.resize(tulib.Rect{pos.X, pos.Y, lw, pos.Height})
		v.right.resize(tulib.Rect{pos.X + lw + 1, pos.Y, rw, pos.Height})
	} else {
		// vertical split, use 'h', no need to reserve one line for
		// splitter, because splitters are part of the buffer's output
		// (their status bars act like a splitter)
		h := pos.Height
		th := int(float32(h) * v.split)
		bh := h - th
		v.top.resize(tulib.Rect{pos.X, pos.Y, pos.Width, th})
		v.bottom.resize(tulib.Rect{pos.X, pos.Y + th, pos.Width, bh})
	}
}

func (v *view_tree) traverse(cb func(*view_tree)) {
	if v.leaf != nil {
		cb(v)
		return
	}

	if v.left != nil {
		v.left.traverse(cb)
		v.right.traverse(cb)
	} else if v.top != nil {
		v.top.traverse(cb)
		v.bottom.traverse(cb)
	}
}

func (v *view_tree) nearest_vsplit() *view_tree {
	v = v.parent
	for v != nil {
		if v.top != nil {
			return v
		}
		v = v.parent
	}
	return nil
}

func (v *view_tree) nearest_hsplit() *view_tree {
	v = v.parent
	for v != nil {
		if v.left != nil {
			return v
		}
		v = v.parent
	}
	return nil
}

func (v *view_tree) one_step() float32 {
	if v.top != nil {
		return 1.0 / float32(v.Height)
	} else if v.left != nil {
		return 1.0 / float32(v.Width-1)
	}
	return 0.0
}

func (v *view_tree) normalize_split() {
	var off int
	if v.top != nil {
		off = int(float32(v.Height) * v.split)
	} else {
		off = int(float32(v.Width-1) * v.split)
	}
	v.split = float32(off) * v.one_step()
}

func (v *view_tree) step_resize(n int) {
	if v.Width <= 1 || v.Height <= 0 {
		// avoid division by zero, result is really bad
		return
	}

	one := v.one_step()
	v.normalize_split()
	v.split += one*float32(n) + (one * 0.5)
	if v.split > 1.0 {
		v.split = 1.0
	}
	if v.split < 0.0 {
		v.split = 0.0
	}
	v.resize(v.Rect)
}

func (v *view_tree) reparent() {
	if v.left != nil {
		v.left.parent = v
		v.right.parent = v
	} else if v.top != nil {
		v.top.parent = v
		v.bottom.parent = v
	}
}

func (v *view_tree) sibling() *view_tree {
	p := v.parent
	if p == nil {
		return nil
	}
	switch {
	case v == p.left:
		return p.right
	case v == p.right:
		return p.left
	case v == p.top:
		return p.bottom
	case v == p.bottom:
		return p.top
	}
	panic("unreachable")
}

func (v *view_tree) first_leaf_node() *view_tree {
	if v.left != nil {
		return v.left.first_leaf_node()
	} else if v.top != nil {
		return v.top.first_leaf_node()
	} else if v.leaf != nil {
		return v
	}
	panic("unreachable")
}

//----------------------------------------------------------------------------
// godit
//
// Main top-level structure, that handles views composition, status bar and
// input messaging. Also it's the spot where keyboard macros are implemented.
//----------------------------------------------------------------------------

type godit struct {
	uibuf         tulib.Buffer
	active        *view_tree // this one is always a leaf node
	views         *view_tree // a root node
	buffers       []*buffer
	lastcmdclass  vcommand_class
	statusbuf     bytes.Buffer
	quitflag      bool
	overlay       overlay_mode
	termbox_event chan termbox.Event
}

func new_godit(filenames []string) *godit {
	g := new(godit)
	g.buffers = make([]*buffer, 0, 20)
	for _, filename := range filenames {
		buf, err := new_buffer_from_file(filename)
		if err != nil {
			buf = new_buffer()
			buf.name = filename
		}
		g.buffers = append(g.buffers, buf)
	}
	if len(g.buffers) == 0 {
		buf := new_buffer()
		buf.name = "*new*"
		g.buffers = append(g.buffers, buf)
	}
	if g.buffers[0].path == "" {
		g.set_status("(New file)")
	}
	g.views = new_view_tree_leaf(nil, new_view(g, g.buffers[0]))
	g.active = g.views
	return g
}

func (g *godit) set_status(format string, args ...interface{}) {
	g.statusbuf.Reset()
	fmt.Fprintf(&g.statusbuf, format, args...)
}

func (g *godit) split_horizontally() {
	if g.active.Width == 0 {
		return
	}
	g.active.split_horizontally()
	g.active = g.active.left
	g.resize()
}

func (g *godit) split_vertically() {
	if g.active.Height == 0 {
		return
	}
	g.active.split_vertically()
	g.active = g.active.top
	g.resize()
}

func (g *godit) kill_active_view() {
	p := g.active.parent
	if p == nil {
		return
	}

	pp := p.parent
	sib := g.active.sibling()
	g.active.leaf.deactivate()
	g.active.leaf.detach()

	*p = *sib
	p.parent = pp
	p.reparent()

	g.active = p.first_leaf_node()
	g.active.leaf.activate()
	g.resize()
}

func (g *godit) kill_all_views_but_active() {
	g.views.traverse(func(v *view_tree) {
		if v == g.active {
			return
		}
		if v.leaf != nil {
			v.leaf.detach()
		}
	})
	g.views = g.active
	g.views.parent = nil
	g.resize()
}

// Call it manually only when views layout has changed.
func (g *godit) resize() {
	g.uibuf = tulib.TermboxBuffer()
	views_area := g.uibuf.Rect
	views_area.Height -= 1 // reserve space for command line
	g.views.resize(views_area)
}

func (g *godit) draw_autocompl() {
	view := g.active.leaf
	x, y := g.active.X, g.active.Y
	if view.ac == nil {
		return
	}

	proposals := view.ac.actual_proposals()
	if len(proposals) > 0 {
		cx, cy := view.cursor_position_for(view.ac.origin)
		view.ac.draw_onto(&g.uibuf, x+cx, y+cy)
	}
}

func (g *godit) draw() {
	var overlay_needs_cursor bool
	if g.overlay != nil {
		overlay_needs_cursor = needs_cursor(g.overlay.cursor_position())
	}

	// draw everything
	g.views.draw()
	g.composite_recursively(g.views)
	g.draw_status()

	// draw overlay if any
	if g.overlay != nil {
		g.overlay.draw()
	}

	// draw autocompletion
	if !overlay_needs_cursor {
		g.draw_autocompl()
	}

	// update cursor position
	var cx, cy int
	if !overlay_needs_cursor {
		cx, cy = g.cursor_position()
	} else {
		// this can be true, only when g.overlay != nil, see above
		cx, cy = g.overlay.cursor_position()
	}
	termbox.SetCursor(cx, cy)
}

func (g *godit) draw_status() {
	lp := tulib.DefaultLabelParams
	r := g.uibuf.Rect
	r.Y = r.Height - 1
	r.Height = 1
	g.uibuf.Fill(r, termbox.Cell{Fg: lp.Fg, Bg: lp.Bg, Ch: ' '})
	g.uibuf.DrawLabel(r, &lp, g.statusbuf.Bytes())
}

func (g *godit) composite_recursively(v *view_tree) {
	if v.leaf != nil {
		g.uibuf.Blit(v.Rect, 0, 0, &v.leaf.uibuf)
		return
	}

	if v.left != nil {
		g.composite_recursively(v.left)
		g.composite_recursively(v.right)
		splitter := v.right.Rect
		splitter.X -= 1
		splitter.Width = 1
		g.uibuf.Fill(splitter, termbox.Cell{
			Fg: termbox.AttrReverse,
			Bg: termbox.AttrReverse,
			Ch: '│',
		})
	} else {
		g.composite_recursively(v.top)
		g.composite_recursively(v.bottom)
	}
}

func (g *godit) cursor_position() (int, int) {
	x, y := g.active.leaf.cursor_position()
	return g.active.X + x, g.active.Y + y
}

func (g *godit) on_sys_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlG:
		g.set_overlay_mode(nil)
		g.set_status("Quit")
	case termbox.KeyF1:
		g.buffers[0].dump_history()
		g.active.leaf.dump_info()
	}
}

func (g *godit) on_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlX:
		g.set_overlay_mode(init_extended_mode(g))
	default:
		g.active.leaf.on_key(ev)
	}
}

func (g *godit) main_loop() {
	g.termbox_event = make(chan termbox.Event, 20)
	go func() {
		for {
			g.termbox_event <- termbox.PollEvent()
		}
	}()
	for {
		select {
		case ev := <-g.termbox_event:
			ok := g.handle_event(&ev)
			if !ok {
				return
			}
			g.consume_more_events()
			g.draw()
			termbox.Flush()
		}
	}
}

func (g *godit) consume_more_events() bool {
	for {
		select {
		case ev := <-g.termbox_event:
			ok := g.handle_event(&ev)
			if !ok {
				return false
			}
		default:
			return true
		}
	}
	panic("unreachable")
}

func (g *godit) handle_event(ev *termbox.Event) bool {
	switch ev.Type {
	case termbox.EventKey:
		g.set_status("") // reset status on every key event
		g.on_sys_key(ev)
		if g.overlay != nil {
			g.overlay.on_key(ev)
		} else {
			g.on_key(ev)
		}

		if g.quitflag {
			return false
		}
	case termbox.EventResize:
		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
		g.resize()
		if g.overlay != nil {
			g.overlay.on_resize(ev)
		}
	case termbox.EventError:
		panic(ev.Err)
	}
	return true
}

func (g *godit) set_overlay_mode(m overlay_mode) {
	if g.overlay != nil {
		g.overlay.exit()
	}
	g.overlay = m
}

//----------------------------------------------------------------------------
// overlay mode
//----------------------------------------------------------------------------

type overlay_mode interface {
	cursor_position() (int, int)
	exit()
	draw()
	on_resize(ev *termbox.Event)
	on_key(ev *termbox.Event)
}

type stub_overlay_mode struct{}

func (stub_overlay_mode) cursor_position() (int, int) { return -2, -2 }
func (stub_overlay_mode) exit()                       {}
func (stub_overlay_mode) draw()                       {}
func (stub_overlay_mode) on_resize(ev *termbox.Event) {}
func (stub_overlay_mode) on_key(ev *termbox.Event)    {}

//----------------------------------------------------------------------------

func main() {
	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputAlt)

	godit := new_godit(os.Args[1:])
	godit.resize()
	godit.draw()
	termbox.SetCursor(godit.cursor_position())
	termbox.Flush()
	godit.main_loop()
}

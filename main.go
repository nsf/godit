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
		w-- // reserve one line for splitter
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

//----------------------------------------------------------------------------
// godit
//
// Main top-level structure, that handles views composition, status bar and
// input messaging. Also it's the spot where keyboard macros are implemented.
//----------------------------------------------------------------------------

type godit struct {
	uibuf        tulib.Buffer
	active       *view_tree // this one is always a leaf node
	views        *view_tree // a root node
	buffers      []*buffer
	lastcmdclass vcommand_class
	statusbuf    bytes.Buffer
	quitflag     bool
	overlay      overlay_mode
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

// Call it manually only when views layout has changed.
func (g *godit) resize() {
	g.uibuf = tulib.TermboxBuffer()
	views_area := g.uibuf.Rect
	views_area.Height -= 1 // reserve space for command line
	g.views.resize(views_area)
}

func (g *godit) draw() {
	// draw everything
	g.views.draw()
	g.composite_recursively(g.views)
	g.draw_status()

	// draw overlay if any
	if g.overlay != nil {
		g.overlay.draw()
	}

	// update cursor position
	if g.overlay == nil || !g.overlay.needs_cursor() {
		termbox.SetCursor(g.cursor_position())
	}
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

		g.draw()
		termbox.Flush()
	case termbox.EventResize:
		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
		g.resize()
		if g.overlay != nil {
			g.overlay.on_resize(ev)
		}
		g.draw()
		termbox.Flush()
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
	needs_cursor() bool
	exit()
	draw()
	on_resize(ev *termbox.Event)
	on_key(ev *termbox.Event)
}

type default_overlay_mode struct{}

func (default_overlay_mode) needs_cursor() bool          { return false }
func (default_overlay_mode) exit()                       {}
func (default_overlay_mode) draw()                       {}
func (default_overlay_mode) on_resize(ev *termbox.Event) {}
func (default_overlay_mode) on_key(ev *termbox.Event)    {}

//----------------------------------------------------------------------------
// extended mode
//----------------------------------------------------------------------------

type extended_mode struct {
	default_overlay_mode
	godit *godit
}

func init_extended_mode(godit *godit) extended_mode {
	e := extended_mode{godit: godit}
	e.godit.set_status("C-x")
	return e
}

func (e extended_mode) on_key(ev *termbox.Event) {
	g := e.godit
	v := g.active.leaf

	switch ev.Key {
	case termbox.KeyCtrlC:
		g.quitflag = true
	case termbox.KeyCtrlX:
		v.on_vcommand(vcommand_swap_cursor_and_mark, 0)
	case termbox.KeyCtrlW:
		g.set_overlay_mode(init_view_op_mode(g))
		return
	default:
		switch ev.Ch {
		case 'w':
			g.set_overlay_mode(init_view_op_mode(g))
			return
		case '2':
			g.split_vertically()
		case '3':
			g.split_horizontally()
		case 'o':
			if g.views.left == g.active {
				g.active = g.views.right
				g.active.leaf.activate()
			} else if g.views.right == g.active {
				g.active = g.views.left
				g.active.leaf.activate()
			}
		default:
			goto undefined
		}
	}

	g.set_overlay_mode(nil)
	return
undefined:
	g.set_status("C-x %s is undefined", tulib.KeyToString(ev.Key, ev.Ch, ev.Mod))
	g.set_overlay_mode(nil)
}

//----------------------------------------------------------------------------
// view op mode
//----------------------------------------------------------------------------

type view_op_mode struct {
	default_overlay_mode
	godit *godit
}

const view_names = `1234567890abcdefgjkmnopqrstuwxyzABCDEFGJKLMNOPQRSTUWXYZ`

var view_op_mode_name = []byte("View Operations mode")

func init_view_op_mode(godit *godit) view_op_mode {
	termbox.HideCursor()
	v := view_op_mode{godit: godit}
	return v
}

func (v view_op_mode) draw() {
	g := v.godit
	r := g.uibuf.Rect
	r.Y = r.Height - 1
	r.Height = 1
	g.uibuf.Fill(r, termbox.Cell{
		Fg: termbox.ColorDefault,
		Bg: termbox.ColorDefault,
		Ch: ' ',
	})
	lp := tulib.DefaultLabelParams
	lp.Fg = termbox.ColorYellow
	g.uibuf.DrawLabel(r, &lp, view_op_mode_name)

	// draw views names
	name := 0
	g.views.traverse(func(leaf *view_tree) {
		if name >= len(view_names) {
			return
		}
		bg := termbox.ColorRed
		if leaf == g.active {
			bg = termbox.ColorBlue
		}
		r := leaf.Rect
		r.Width = 3
		r.Height = 1
		x := r.X + 1
		y := r.Y
		g.uibuf.Fill(r, termbox.Cell{
			Fg: termbox.ColorDefault,
			Bg: bg,
			Ch: ' ',
		})
		g.uibuf.Set(x, y, termbox.Cell{
			Fg: termbox.ColorWhite | termbox.AttrBold,
			Bg: bg,
			Ch: rune(view_names[name]),
		})
		name++
	})

	// draw splitters
	r = g.active.Rect
	var x, y int

	// horizontal ----------------------
	hr := r
	hr.X += (r.Width - 1) / 2
	hr.Width = 1
	hr.Height = 3
	g.uibuf.Fill(hr, termbox.Cell{
		Fg: termbox.ColorCyan,
		Bg: termbox.ColorBlue,
		Ch: '│',
	})

	x = hr.X
	y = hr.Y + 1
	g.uibuf.Set(x, y, termbox.Cell{
		Fg: termbox.ColorCyan | termbox.AttrBold,
		Bg: termbox.ColorBlue,
		Ch: 'H',
	})

	// vertical ----------------------
	vr := r
	vr.Y += (r.Height - 1) / 2
	vr.Height = 1
	vr.Width = 5
	g.uibuf.Fill(vr, termbox.Cell{
		Fg: termbox.ColorCyan,
		Bg: termbox.ColorBlue,
		Ch: '─',
	})

	x = vr.X + 2
	y = vr.Y
	g.uibuf.Set(x, y, termbox.Cell{
		Fg: termbox.ColorCyan | termbox.AttrBold,
		Bg: termbox.ColorBlue,
		Ch: 'V',
	})
}

func (v view_op_mode) select_name(ch rune) *view_tree {
	g := v.godit
	sel := (*view_tree)(nil)
	name := 0
	g.views.traverse(func(leaf *view_tree) {
		if name >= len(view_names) {
			return
		}
		if rune(view_names[name]) == ch {
			sel = leaf
		}
		name++
	})

	return sel
}

func (v view_op_mode) needs_cursor() bool {
	return true
}

func (v view_op_mode) on_key(ev *termbox.Event) {
	g := v.godit
	if ev.Ch != 0 {
		leaf := v.select_name(ev.Ch)
		if leaf != nil {
			g.active = leaf
			g.active.leaf.activate()
			return
		}

		switch ev.Ch {
		case 'h', 'H':
			g.split_horizontally()
			return
		case 'v', 'V':
			g.split_vertically()
			return
		}
	}

	switch ev.Key {
	case termbox.KeyCtrlN, termbox.KeyArrowDown:
		node := g.active.nearest_vsplit()
		if node != nil {
			node.step_resize(1)
		}
		return
	case termbox.KeyCtrlP, termbox.KeyArrowUp:
		node := g.active.nearest_vsplit()
		if node != nil {
			node.step_resize(-1)
		}
		return
	case termbox.KeyCtrlF, termbox.KeyArrowRight:
		node := g.active.nearest_hsplit()
		if node != nil {
			node.step_resize(1)
		}
		return
	case termbox.KeyCtrlB, termbox.KeyArrowLeft:
		node := g.active.nearest_hsplit()
		if node != nil {
			node.step_resize(-1)
		}
		return
	}

	g.set_overlay_mode(nil)
}

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

	for {
		ev := termbox.PollEvent()
		ok := godit.handle_event(&ev)
		if !ok {
			return
		}
	}
}

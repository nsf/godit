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
	left   *view_tree
	top    *view_tree
	right  *view_tree
	bottom *view_tree
	leaf   *view
	split  float32
	pos    tulib.Rect // updated with 'resize' call
}

func new_view_tree_leaf(v *view) *view_tree {
	return &view_tree{
		leaf: v,
	}
}

func (v *view_tree) split_vertically() {
	top := v.leaf
	bottom := new_view(top.parent, top.buf)
	*v = view_tree{
		top:    new_view_tree_leaf(top),
		bottom: new_view_tree_leaf(bottom),
		split:  0.5,
	}
}

func (v *view_tree) split_horizontally() {
	left := v.leaf
	right := new_view(left.parent, left.buf)
	*v = view_tree{
		left:  new_view_tree_leaf(left),
		right: new_view_tree_leaf(right),
		split: 0.5,
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
	v.pos = pos
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
	g.views = new_view_tree_leaf(new_view(g, g.buffers[0]))
	g.active = g.views
	return g
}

func (g *godit) set_status(format string, args ...interface{}) {
	g.statusbuf.Reset()
	fmt.Fprintf(&g.statusbuf, format, args...)
}

func (g *godit) split_horizontally() {
	g.active.split_horizontally()
	g.active = g.active.left
	g.resize()
}

func (g *godit) split_vertically() {
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
		g.uibuf.Blit(v.pos, 0, 0, &v.leaf.uibuf)
		return
	}

	if v.left != nil {
		g.composite_recursively(v.left)
		g.composite_recursively(v.right)
		splitter := v.right.pos
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
	return g.active.pos.X + x, g.active.pos.Y + y
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
		g.set_overlay_mode(extended_mode{godit: g})
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
	if m != nil {
		m.init()
	}
	g.overlay = m
}

//----------------------------------------------------------------------------
// overlay mode
//----------------------------------------------------------------------------

type overlay_mode interface {
	needs_cursor() bool
	init()
	exit()
	draw()
	on_resize(ev *termbox.Event)
	on_key(ev *termbox.Event)
}

type default_overlay_mode struct{}

func (default_overlay_mode) needs_cursor() bool          { return false }
func (default_overlay_mode) init()                       {}
func (default_overlay_mode) exit()                       {}
func (default_overlay_mode) draw()                       {}
func (default_overlay_mode) on_resize(ev *termbox.Event) {}
func (default_overlay_mode) on_key(ev *termbox.Event)    {}

//----------------------------------------------------------------------------
// extended mode
//----------------------------------------------------------------------------

type extended_mode struct {
	godit *godit
	default_overlay_mode
}

func (e extended_mode) init() {
	e.godit.set_status("C-x")
}

func (e extended_mode) on_key(ev *termbox.Event) {
	g := e.godit
	v := g.active.leaf

	switch ev.Key {
	case termbox.KeyCtrlC:
		g.quitflag = true
	case termbox.KeyCtrlX:
		v.on_vcommand(vcommand_swap_cursor_and_mark, 0)
	default:
		switch ev.Ch {
		case '2':
			g.split_vertically()
		case '3':
			g.split_horizontally()
		case 'o':
			if g.views.left == g.active {
				g.active = g.views.right
			} else if g.views.right == g.active {
				g.active = g.views.left
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

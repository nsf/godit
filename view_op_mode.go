package main

import (
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
)

//----------------------------------------------------------------------------
// view op mode
//----------------------------------------------------------------------------

type view_op_mode struct {
	stub_overlay_mode
	godit *godit
}

const view_names = `1234567890abcdefgijlmnpqrstuwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ`

var view_op_mode_name = []byte("view operations mode")

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
		bg := termbox.ColorBlue
		if leaf == g.active {
			bg = termbox.ColorRed
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
		Fg: termbox.ColorWhite,
		Bg: termbox.ColorRed,
		Ch: '│',
	})

	x = hr.X
	y = hr.Y + 1
	g.uibuf.Set(x, y, termbox.Cell{
		Fg: termbox.ColorWhite | termbox.AttrBold,
		Bg: termbox.ColorRed,
		Ch: 'h',
	})

	// vertical ----------------------
	vr := r
	vr.Y += (r.Height - 1) / 2
	vr.Height = 1
	vr.Width = 5
	g.uibuf.Fill(vr, termbox.Cell{
		Fg: termbox.ColorWhite,
		Bg: termbox.ColorRed,
		Ch: '─',
	})

	x = vr.X + 2
	y = vr.Y
	g.uibuf.Set(x, y, termbox.Cell{
		Fg: termbox.ColorWhite | termbox.AttrBold,
		Bg: termbox.ColorRed,
		Ch: 'v',
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
			g.active.leaf.deactivate()
			g.active = leaf
			g.active.leaf.activate()
			return
		}

		switch ev.Ch {
		case 'h':
			g.split_horizontally()
			return
		case 'v':
			g.split_vertically()
			return
		case 'k':
			g.kill_active_view()
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

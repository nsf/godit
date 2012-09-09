package main

import (
	"github.com/nsf/termbox-go"
)

type redo_mode struct {
	stub_overlay_mode
	godit *godit
}

func init_redo_mode(godit *godit) redo_mode {
	r := redo_mode{godit: godit}
	return r
}

func (r redo_mode) on_key(ev *termbox.Event) {
	g := r.godit
	v := g.active.leaf
	if ev.Mod == 0 && ev.Key == termbox.KeyCtrlSlash {
		v.on_vcommand(vcommand_redo, 0)
		return
	}

	g.set_overlay_mode(nil)
	g.on_key(ev)
}

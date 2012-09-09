package main

import (
	"github.com/nsf/termbox-go"
)

type macro_repeat_mode struct {
	stub_overlay_mode
	godit *godit
}

func init_macro_repeat_mode(godit *godit) macro_repeat_mode {
	m := macro_repeat_mode{godit: godit}
	godit.set_overlay_mode(nil)
	m.godit.replay_macro()
	m.godit.set_status("(Type e to repeat macro)")
	return m
}

func (m macro_repeat_mode) on_key(ev *termbox.Event) {
	g := m.godit
	if ev.Mod == 0 && ev.Ch == 'e' {
		g.set_overlay_mode(nil)
		g.replay_macro()
		g.set_overlay_mode(m)
		g.set_status("(Type e to repeat macro)")
		return
	}

	g.set_overlay_mode(nil)
	g.on_key(ev)
}

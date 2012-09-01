package main

import (
	"github.com/nsf/termbox-go"
)

//----------------------------------------------------------------------------
// overlay mode
//----------------------------------------------------------------------------

type overlay_mode interface {
	needs_cursor() bool
	cursor_position() (int, int)
	exit()
	draw()
	on_resize(ev *termbox.Event)
	on_key(ev *termbox.Event)
}

type stub_overlay_mode struct{}

func (stub_overlay_mode) needs_cursor() bool          { return false }
func (stub_overlay_mode) cursor_position() (int, int) { return -1, -1 }
func (stub_overlay_mode) exit()                       {}
func (stub_overlay_mode) draw()                       {}
func (stub_overlay_mode) on_resize(ev *termbox.Event) {}
func (stub_overlay_mode) on_key(ev *termbox.Event)    {}

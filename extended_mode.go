package main

import (
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"io/ioutil"
)

//----------------------------------------------------------------------------
// extended mode
//----------------------------------------------------------------------------

type extended_mode struct {
	stub_overlay_mode
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
	case termbox.KeyCtrlA:
		v.on_vcommand(vcommand_autocompl_init, 0)
	default:
		switch ev.Ch {
		case 'w':
			g.set_overlay_mode(init_view_op_mode(g))
			return
		case '0':
			g.kill_active_view()
		case '1':
			g.kill_all_views_but_active()
		case '2':
			g.split_vertically()
		case '3':
			g.split_horizontally()
		case 'o':
			sibling := g.active.sibling()
			if sibling != nil && sibling.leaf != nil {
				g.active.leaf.deactivate()
				g.active = sibling
				g.active.leaf.activate()
			}
		case 'b':
			g.set_overlay_mode(init_line_edit_mode(line_edit_mode_params{
				godit:           g,
				ac_decide:       make_godit_buffer_ac_decide(g),
				prompt:          "Buffer:",
				initial_content: g.active.leaf.buf.path,
				on_apply: func(buf *buffer) {
					b, _ := ioutil.ReadAll(buf.reader())
					bs := string(b)
					for _, buf := range g.buffers {
						if buf.path == bs {
							g.active.leaf.attach(buf)
						}
					}
				},
			}))
			return
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

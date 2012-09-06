package main

import (
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
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
	b := v.buf

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
	case termbox.KeyCtrlF:
		g.set_overlay_mode(init_line_edit_mode(g, g.open_buffer_lemp()))
		return
	case termbox.KeyCtrlS:
		if b.synced_with_disk() {
			g.set_status("(No changes need to be saved)")
			break
		}

		if b.path != "" {
			v.finalize_action_group()
			err := b.save()
			if err != nil {
				g.set_status(err.Error())
			} else {
				v.dirty |= dirty_status
				g.set_status("Wrote %s", b.path)
			}
			break
		}

		g.set_overlay_mode(init_line_edit_mode(g, g.save_as_buffer_lemp(v)))
		return
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
			g.set_overlay_mode(init_line_edit_mode(g, g.switch_buffer_lemp()))
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

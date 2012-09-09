package main

import (
	"github.com/nsf/termbox-go"
)

type region_indent_mode struct {
	stub_overlay_mode
	godit *godit
}

func init_region_indent_mode(godit *godit, dir int) region_indent_mode {
	v := godit.active.leaf
	r := region_indent_mode{godit: godit}
	beg := v.cursor
	end := v.cursor
	if v.buf.is_mark_set() {
		end = v.buf.mark
	}

	if beg.line_num > end.line_num {
		beg, end = end, beg
	}
	beg.boffset = 0
	end.boffset = len(end.line.data)

	v.set_tags(view_tag{
		begin_line: beg.line_num,
		begin_offset: beg.boffset,
		end_line: end.line_num,
		end_offset: end.boffset,
		fg: termbox.ColorDefault,
		bg: termbox.ColorBlue,
	})
	if dir > 0 {
		v.on_vcommand(vcommand_indent_region, 0)
	} else if dir < 0 {
		v.on_vcommand(vcommand_deindent_region, 0)
	}
	v.dirty = dirty_everything
	godit.set_status("(Type > or < to indent/deindent respectively)")
	return r
}

func (r region_indent_mode) exit() {
	v := r.godit.active.leaf
	v.set_tags()
	v.dirty = dirty_everything
}

func (r region_indent_mode) on_key(ev *termbox.Event) {
	g := r.godit
	v := g.active.leaf
	if ev.Mod == 0 {
		switch ev.Ch {
		case '>':
			v.on_vcommand(vcommand_indent_region, 0)
			g.set_status("(Type > or < to indent/deindent respectively)")
			return
		case '<':
			v.on_vcommand(vcommand_deindent_region, 0)
			g.set_status("(Type > or < to indent/deindent respectively)")
			return
		}
	}

	g.set_overlay_mode(nil)
	g.on_key(ev)
}

package main

import (
	"bytes"
	"github.com/nsf/termbox-go"
)

type incsearch_context struct {
	last_word []byte
	last_loc  cursor_location
}

func incsearch_lemp(g *godit) line_edit_mode_params {
	v := g.active.leaf
	ctx := incsearch_context{
		last_word: make([]byte, 0, 32),
		last_loc:  v.cursor,
	}

	search_forward := func() {
		v.finalize_action_group()
		v.last_vcommand = vcommand_move_cursor_forward

		cursor, ok := ctx.last_loc.search_forward(ctx.last_word)
		if !ok {
			v.move_cursor_end_of_file()
			ctx.last_loc = v.cursor
			v.set_tags()
		} else {
			ctx.last_loc = cursor
			v.set_tags(view_tag{
				beg_line:   cursor.line_num,
				beg_offset: cursor.boffset,
				end_line:   cursor.line_num,
				end_offset: cursor.boffset + len(ctx.last_word),
				fg:         termbox.ColorCyan,
				bg:         termbox.ColorMagenta,
			})
			cursor.boffset += len(ctx.last_word)
			v.move_cursor_to(cursor)
		}
		v.center_view_on_cursor()
		v.dirty = dirty_everything
		v.highlight_bytes = ctx.last_word
	}

	key_filter := func(ev *termbox.Event) bool {
		if ev.Mod == 0 && ev.Key == termbox.KeyCtrlS {
			if ctx.last_loc.eol() && ctx.last_loc.last_line() {
				ctx.last_loc = cursor_location{
					line:     v.buf.first_line,
					line_num: 1,
					boffset:  0,
				}
			} else {
				ctx.last_loc.boffset += len(ctx.last_word)
			}
			search_forward()
			return true
		}
		return false
	}

	post_key_hook := func(buffer *buffer) {
		new_word := buffer.first_line.data
		if bytes.Equal(new_word, ctx.last_word) {
			return
		}
		ctx.last_word = copy_byte_slice(ctx.last_word, new_word)
		search_forward()
	}

	cancel := func() {
		v.highlight_bytes = nil
		v.set_tags()
		v.dirty = dirty_everything
	}

	return line_edit_mode_params{
		prompt:        "I-search:",
		key_filter:    key_filter,
		post_key_hook: post_key_hook,
		on_apply:      func(*buffer) { cancel() },
		on_cancel:     cancel,
	}
}

package main

import (
	"github.com/nsf/termbox-go"
	"bytes"
)

type isearch_mode struct {
	*line_edit_mode
	last_word []byte
	last_loc cursor_location
}

func init_isearch_mode(g *godit) *isearch_mode {
	v := g.active.leaf
	m := new(isearch_mode)
	m.last_word = make([]byte, 0, 32)
	m.last_loc = v.cursor

	cancel := func() {
		v.highlight_bytes = nil
		v.set_tags()
		v.dirty = dirty_everything
	}
	m.line_edit_mode = init_line_edit_mode(g, line_edit_mode_params{
		prompt: "I-search:",
		on_apply: func(*buffer) { cancel() },
		on_cancel: cancel,
	})
	return m
}

func (m *isearch_mode) search_forward() {
	v := m.godit.active.leaf
	v.finalize_action_group()
	v.last_vcommand = vcommand_move_cursor_forward

	cursor, ok := m.last_loc.search_forward(m.last_word)
	if !ok {
		v.move_cursor_end_of_file()
		m.last_loc = v.cursor
		v.set_tags()
	} else {
		m.last_loc = cursor
		v.set_tags(view_tag{
			beg_line:   cursor.line_num,
			beg_offset: cursor.boffset,
			end_line:   cursor.line_num,
			end_offset: cursor.boffset + len(m.last_word),
			fg:         termbox.ColorCyan,
			bg:         termbox.ColorMagenta,
		})
		cursor.boffset += len(m.last_word)
		v.move_cursor_to(cursor)
	}
	v.center_view_on_cursor()
	v.dirty = dirty_everything
	v.highlight_bytes = m.last_word
}

func (m *isearch_mode) on_key(ev *termbox.Event) {
	v := m.godit.active.leaf
	if ev.Key == termbox.KeyCtrlS {
		if m.last_loc.eol() && m.last_loc.last_line() {
			m.last_loc = cursor_location{
				line:     v.buf.first_line,
				line_num: 1,
				boffset:  0,
			}
		} else {
			m.last_loc.boffset += len(m.last_word)
		}
		m.search_forward()
	} else {
		m.line_edit_mode.on_key(ev)
	}

	new_word := m.linebuf.first_line.data
	if bytes.Equal(new_word, m.last_word) {
		return
	}
	m.last_word = copy_byte_slice(m.last_word, new_word)
	m.search_forward()
}

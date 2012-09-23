package main

import (
	"bytes"
	"github.com/nsf/termbox-go"
	"unicode/utf8"
)

var isearch_last_word = make([]byte, 0, 32)

type isearch_mode struct {
	*line_edit_mode
	last_word []byte
	last_loc  cursor_location

	backward bool
	failing  bool
	wrapped  bool

	prompt_isearch []byte
	prompt_failing []byte
	prompt_wrapped []byte
}

func init_isearch_mode(g *godit, backward bool) *isearch_mode {
	v := g.active.leaf
	m := new(isearch_mode)
	m.last_word = make([]byte, 0, 32)
	m.last_loc = v.cursor
	m.backward = backward
	m.prepare_prompts()
	cancel := func() {
		v.highlight_bytes = nil
		v.set_tags()
		v.dirty = dirty_everything
	}
	m.line_edit_mode = init_line_edit_mode(g, line_edit_mode_params{
		on_apply:  func(*buffer) { cancel() },
		on_cancel: cancel,
		ac_decide: default_ac_decide,
	})
	m.set_prompt(m.prompt_isearch)
	return m
}

func (m *isearch_mode) prepare_prompts() {
	if m.backward {
		m.prompt_isearch = []byte("I-search backward:")
		m.prompt_failing = []byte("Failing I-search backward:")
		m.prompt_wrapped = []byte("Wrapped I-search backward:")
	} else {
		m.prompt_isearch = []byte("I-search:")
		m.prompt_failing = []byte("Failing I-search:")
		m.prompt_wrapped = []byte("Wrapped I-search:")
	}
}

func (m *isearch_mode) set_prompt(prompt []byte) {
	m.prompt = prompt
	m.prompt_w = utf8.RuneCount(m.prompt)
}

func (m *isearch_mode) search(next bool) {
	v := m.godit.active.leaf
	v.finalize_action_group()
	v.last_vcommand = vcommand_move_cursor_forward

	var (
		cursor cursor_location
		ok     bool
	)
	if m.backward {
		if !next {
			cursor, ok = m.last_loc.search_forward(m.last_word)
			if !ok || cursor != m.last_loc {
				cursor, ok = m.last_loc.search_backward(m.last_word)
			}
		} else {
			cursor, ok = m.last_loc.search_backward(m.last_word)
		}
	} else {
		if next && !m.wrapped {
			m.last_loc.boffset += len(m.last_word)
		}
		cursor, ok = m.last_loc.search_forward(m.last_word)
	}
	if !ok {
		v.set_tags()
		m.set_prompt(m.prompt_failing)
		m.failing = true
		m.wrapped = false
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
		if !m.backward {
			cursor.boffset += len(m.last_word)
		}
		v.move_cursor_to(cursor)
		if m.wrapped {
			m.set_prompt(m.prompt_wrapped)
			m.wrapped = false
		} else {
			m.set_prompt(m.prompt_isearch)
		}
		m.failing = false
	}
	v.center_view_on_cursor()
	v.dirty = dirty_everything
	v.highlight_bytes = m.last_word
}

func (m *isearch_mode) restore_previous_isearch_maybe() {
	lw := m.godit.isearch_last_word
	if len(lw) == 0 {
		return
	}

	v := m.lineview
	c := v.cursor
	v.action_insert(c, clone_byte_slice(lw))
	c.boffset += len(lw)
	v.move_cursor_to(c)
	v.dirty = dirty_everything
	v.finalize_action_group()
}

func (m *isearch_mode) wrap_location() cursor_location {
	v := m.godit.active.leaf
	if m.backward {
		return cursor_location{
			line:     v.buf.last_line,
			line_num: v.buf.lines_n,
			boffset:  len(v.buf.last_line.data),
		}
	}

	return cursor_location{
		line:     v.buf.first_line,
		line_num: 1,
		boffset:  0,
	}
}

func (m *isearch_mode) advance_search() {
	if m.failing {
		m.last_loc = m.wrap_location()
		m.failing = false
		m.wrapped = true
	}

	if len(m.last_word) == 0 {
		m.restore_previous_isearch_maybe()
	}
	m.search(true)
}

func (m *isearch_mode) on_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlR:
		if !m.backward {
			m.backward = true
			m.prepare_prompts()
		}
		m.advance_search()
	case termbox.KeyCtrlS:
		if m.backward {
			m.backward = false
			m.prepare_prompts()
		}
		m.advance_search()
	default:
		m.line_edit_mode.on_key(ev)
	}

	new_word := m.linebuf.first_line.data
	if bytes.Equal(new_word, m.last_word) {
		return
	}
	m.last_word = copy_byte_slice(m.last_word, new_word)
	m.godit.isearch_last_word = copy_byte_slice(m.godit.isearch_last_word, new_word)
	m.search(false)
}

package main

import (
	"bytes"
)

//----------------------------------------------------------------------------
// action
//
// A single entity of undo/redo history. All changes to contents of a buffer
// must be initiated by an action.
//----------------------------------------------------------------------------

type action_type int

const (
	action_insert action_type = 1
	action_delete action_type = -1
)

type action struct {
	what   action_type
	data   []byte
	cursor cursor_location
	lines  []*line
}

func (a *action) apply(v *view) {
	a.do(v, a.what)
}

func (a *action) revert(v *view) {
	a.do(v, -a.what)
}

func (a *action) insert_line(line, prev *line, v *view) {
	bi := prev
	ai := prev.next

	// 'bi' is always a non-nil line
	bi.next = line
	line.prev = bi

	// 'ai' could be nil (means we're inserting a new last line)
	if ai == nil {
		v.buf.last_line = line
	} else {
		ai.prev = line
	}
	line.next = ai
}

func (a *action) delete_line(line *line, v *view) {
	bi := line.prev
	ai := line.next
	if ai != nil {
		ai.prev = bi
	} else {
		v.buf.last_line = bi
	}
	if bi != nil {
		bi.next = ai
	} else {
		v.buf.first_line = ai
	}
	line.data = line.data[:0]
}

func (a *action) insert(v *view) {
	var data_chunk []byte
	nline := 0
	offset := a.cursor.boffset
	line := a.cursor.line
	iter_lines(a.data, func(data []byte) {
		if data[0] == '\n' {
			v.buf.bytes_n++
			v.buf.lines_n++

			if offset < len(line.data) {
				// a case where we insert at the middle of the
				// line, need to save that chunk for later
				// insertion at the end of the operation
				data_chunk = line.data[offset:]
				line.data = line.data[:offset]
			}
			// insert a line
			a.insert_line(a.lines[nline], line, v)
			line = a.lines[nline]
			nline++
			offset = 0
		} else {
			v.buf.bytes_n += len(data)

			// insert a chunk of data
			line.data = insert_bytes(line.data, offset, data)
			offset += len(data)
		}
	})
	if data_chunk != nil {
		line.data = append(line.data, data_chunk...)
	}
}

func (a *action) delete(v *view) {
	nline := 0
	offset := a.cursor.boffset
	line := a.cursor.line
	iter_lines(a.data, func(data []byte) {
		if data[0] == '\n' {
			v.buf.bytes_n--
			v.buf.lines_n--

			// append the contents of the deleted line the current line
			line.data = append(line.data, a.lines[nline].data...)
			// delete a line
			a.delete_line(a.lines[nline], v)
			nline++
		} else {
			v.buf.bytes_n -= len(data)

			// delete a chunk of data
			copy(line.data[offset:], line.data[offset+len(data):])
			line.data = line.data[:len(line.data)-len(data)]
		}
	})
}

func (a *action) do(v *view, what action_type) {
	switch what {
	case action_insert:
		a.insert(v)
		v.on_insert_adjust_top_line(a)
		v.buf.other_views(v, func(v *view) {
			v.on_insert(a)
		})
		if v.buf.is_mark_set() {
			v.buf.mark.on_insert_adjust(a)
		}
	case action_delete:
		a.delete(v)
		v.on_delete_adjust_top_line(a)
		v.buf.other_views(v, func(v *view) {
			v.on_delete(a)
		})
		if v.buf.is_mark_set() {
			v.buf.mark.on_delete_adjust(a)
		}
	}
	v.dirty = dirty_everything

	// any change to the buffer causes words cache invalidation
	v.buf.words_cache_valid = false
}

func (a *action) last_line() *line {
	return a.lines[len(a.lines)-1]
}

func (a *action) last_line_affection_len() int {
	i := bytes.LastIndex(a.data, []byte{'\n'})
	if i == -1 {
		return len(a.data)
	}

	return len(a.data) - i - 1
}

func (a *action) first_line_affection_len() int {
	i := bytes.Index(a.data, []byte{'\n'})
	if i == -1 {
		return len(a.data)
	}

	return i
}

// returns the range of deleted lines, the first and the last one
func (a *action) deleted_lines() (int, int) {
	first := a.cursor.line_num + 1
	last := first + len(a.lines) - 1
	return first, last
}

func (a *action) try_merge(b *action) bool {
	if a.what != b.what {
		// can only merge actions of the same type
		return false
	}

	if a.cursor.line_num != b.cursor.line_num {
		return false
	}

	if a.cursor.boffset == b.cursor.boffset {
		pa, pb := a, b
		if a.what == action_insert {
			// on insertion merge as 'ba', on deletion as 'ab'
			pa, pb = pb, pa
		}
		pa.data = append(pa.data, pb.data...)
		pa.lines = append(pa.lines, pb.lines...)
		*a = *pa
		return true
	}

	// different boffsets, try to restore the sequence
	pa, pb := a, b
	if pb.cursor.boffset < pa.cursor.boffset {
		pa, pb = pb, pa
	}
	if pa.cursor.boffset+len(pa.data) == pb.cursor.boffset {
		pa.data = append(pa.data, pb.data...)
		pa.lines = append(pa.lines, pb.lines...)
		*a = *pa
		return true
	}
	return false
}

//----------------------------------------------------------------------------
// action group
//----------------------------------------------------------------------------

type action_group struct {
	actions []action
	next    *action_group
	prev    *action_group
	before  cursor_location
	after   cursor_location
}

func (ag *action_group) append(a *action) {
	if len(ag.actions) != 0 {
		// Oh, we have something in the group already, let's try to
		// merge this action with the last one.
		last := &ag.actions[len(ag.actions)-1]
		if last.try_merge(a) {
			return
		}
	}
	ag.actions = append(ag.actions, *a)
}

// Valid only as long as no new actions were added to the action group.
func (ag *action_group) last_action() *action {
	if len(ag.actions) == 0 {
		return nil
	}
	return &ag.actions[len(ag.actions)-1]
}

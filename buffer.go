package main

import (
	"io"
	"unicode/utf8"
	"os"
	"path/filepath"
	"fmt"
	"bufio"
)

//----------------------------------------------------------------------------
// line
//----------------------------------------------------------------------------

type line struct {
	data []byte
	next *line
	prev *line
}

// Find a visual offset for a given byte offset
func (l *line) voffset(boffset int) (vo int) {
	data := l.data[:boffset]
	for len(data) > 0 {
		r, rlen := utf8.DecodeRune(data)
		data = data[rlen:]
		if r == '\t' {
			vo += tabstop_length - vo%tabstop_length
		} else {
			vo += 1
		}
	}
	return
}

// Find a visual and a character offset for a given byte offset
func (l *line) voffset_coffset(boffset int) (vo, co int) {
	data := l.data[:boffset]
	for len(data) > 0 {
		r, rlen := utf8.DecodeRune(data)
		data = data[rlen:]
		co += 1
		if r == '\t' {
			vo += tabstop_length - vo%tabstop_length
		} else {
			vo += 1
		}
	}
	return
}

// Find a set of closest offsets for a given visual offset
func (l *line) find_closest_offsets(voffset int) (bo, co, vo int) {
	data := l.data
	for len(data) > 0 {
		var vodif int
		r, rlen := utf8.DecodeRune(data)
		data = data[rlen:]

		if r == '\t' {
			vodif = tabstop_length - vo%tabstop_length
		} else {
			vodif = 1
		}

		if vo+vodif > voffset {
			return
		}

		bo += rlen
		co += 1
		vo += vodif
	}
	return
}

//----------------------------------------------------------------------------
// buffer
//----------------------------------------------------------------------------

type buffer struct {
	views      []*view
	first_line *line
	last_line  *line
	loc        view_location
	lines_n    int
	history    *action_group
	mark       cursor_location

	// absoulte path if there is any, empty line otherwise
	path string

	// buffer name (displayed in the status line)
	name string
}

func new_buffer() *buffer {
	b := new(buffer)
	l := new(line)
	l.next = nil
	l.prev = nil
	b.first_line = l
	b.last_line = l
	b.loc = view_location{
		top_line:     l,
		top_line_num: 1,
		cursor: cursor_location{
			line:     l,
			line_num: 1,
		},
	}
	b.init_history()
	return b
}

func new_buffer_from_file(filename string) (*buffer, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf, err := new_buffer_from_reader(f)
	if err != nil {
		return nil, err
	}

	buf.name = filename
	buf.path, err = filepath.Abs(filename)
	if err != nil {
		return nil, err
	}

	return buf, err
}

func new_buffer_from_reader(r io.Reader) (*buffer, error) {
	var err error
	var prevline *line

	br := bufio.NewReader(r)
	l := new(line)
	b := new(buffer)
	b.loc = view_location{
		top_line:     l,
		top_line_num: 1,
		cursor: cursor_location{
			line:     l,
			line_num: 1,
		},
	}
	b.init_history()
	b.lines_n = 1
	b.first_line = l
	for {
		l.data, err = br.ReadBytes('\n')
		if err != nil {
			// last line was read
			break
		} else {
			// cut off the '\n' character
			l.data = l.data[:len(l.data)-1]
		}

		b.lines_n++
		l.next = new(line)
		l.prev = prevline
		prevline = l
		l = l.next
	}
	l.prev = prevline
	b.last_line = l

	// io.EOF is not an error
	if err == io.EOF {
		err = nil
	}

	return b, err
}

func (b *buffer) add_view(v *view) {
	b.views = append(b.views, v)
}

func (b *buffer) delete_view(v *view) {
	vi := -1
	for i, n := 0, len(b.views); i < n; i++ {
		if b.views[i] == v {
			vi = i
			break
		}
	}

	if vi != -1 {
		lasti := len(b.views) - 1
		b.views[vi], b.views[lasti] = b.views[lasti], b.views[vi]
		b.views = b.views[:lasti]
	}
}

func (b *buffer) other_views(v *view, cb func(*view)) {
	for _, ov := range b.views {
		if v == ov {
			continue
		}
		cb(ov)
	}
}

func (b *buffer) init_history() {
	// the trick here is that I set 'sentinel' as 'history', it is required
	// to maintain an invariant, where 'history' is a sentinel or is not
	// empty

	sentinel := new(action_group)
	first := new(action_group)
	sentinel.next = first
	first.prev = sentinel
	b.history = sentinel
}

func (b *buffer) is_mark_set() bool {
	return b.mark.line != nil
}

func (b *buffer) dump_history() {
	cur := b.history
	for cur.prev != nil {
		cur = cur.prev
	}

	p := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, format, args...)
	}

	i := 0
	for cur != nil {
		p("action group %d: %d actions\n", i, len(cur.actions))
		for _, a := range cur.actions {
			switch a.what {
			case action_insert:
				p(" + insert")
			case action_delete:
				p(" - delete")
			}
			p(" (%2d,%2d):%q\n", a.cursor.line_num,
				a.cursor.boffset, string(a.data))
		}
		cur = cur.next
		i++
	}
}

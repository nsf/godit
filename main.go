package main

import (
	"bufio"
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"io"
	"os"
	"unicode/utf8"
)

var _ termbox.Cell
var _ tulib.Rect

const (
	tabstop_length            = 8
	view_vertical_threshold   = 5
	view_horizontal_threshold = 10
)

//----------------------------------------------------------------------------
// view_location
//
// This structure represents a view location in the buffer. It needs to be
// separated from the view, because it's also being saved by the buffer (in case
// if at the moment buffer has no views attached to it).
//----------------------------------------------------------------------------

type view_location struct {
	top_line        *line
	cursor_line     *line
	top_line_num    int
	cursor_line_num int

	// Various cursor offsets from the beginning of the line:
	// 1. in bytes
	// 2. in characters
	// 3. in visual cells
	// An example would be the '\t' character, which gives 1 byte offset, 1
	// character offset, but 'tabstop_length' visual cells offset.
	cursor_boffset int
	cursor_coffset int
	cursor_voffset int

	// This offset is different from these three above, because it's the
	// amount of visual cells you need to skip, before starting to show the
	// contents of the cursor line. The value stays as long as the cursor is
	// within the same line. When cursor jumps from one line to another, the
	// value is recalculated. The logic behind this variable is somewhat
	// close to the one behind the 'top_line' variable.
	line_voffset int

	// this one is used for choosing the best location while traversing
	// vertically, every time 'cursor_voffset' changes due to horizontal
	// movement, this one must be changed as well
	last_cursor_voffset int
}

//----------------------------------------------------------------------------
// view
//----------------------------------------------------------------------------

type view struct {
	buf   *buffer // currently displayed buffer
	uibuf tulib.Buffer
	loc   view_location
}

func new_view(w, h int) *view {
	v := new(view)
	v.uibuf = tulib.NewBuffer(w, h)
	return v
}

func (v *view) attach(b *buffer) {
	if v.buf != nil {
		v.buf.delete_view(v)
	}

	v.buf = b
	v.loc = b.loc
	b.add_view(v)
}

func (v *view) detach() {
	if v.buf != nil {
		v.buf.delete_view(v)
		v.buf = nil
	}
}

// Resize the 'v.uibuf', adjusting things accordingly.
func (v *view) resize(w, h int) {
	v.uibuf.Resize(w, h)
	v.adjust_line_voffset()
	v.adjust_top_line()
}

// This function is similar to what happens inside 'redraw', but it contains a
// certain amount of specific code related to 'loc.line_voffset'. You shouldn't
// use it directly, call 'redraw' instead.
func (v *view) draw_cursor_line(line *line, coff int) {
	x := 0
	tabstop := 0
	linedata := line.data
	for {
		rx := x - v.loc.line_voffset
		if len(linedata) == 0 {
			break
		}

		if x == tabstop {
			tabstop += tabstop_length
		}

		if rx >= v.uibuf.Width {
			last := coff + v.uibuf.Width - 1
			v.uibuf.Cells[last].Ch = '→'
			break
		}

		r, rlen := utf8.DecodeRune(linedata)
		if r == '\t' {
			// fill with spaces to the next tabstop
			for ; x < tabstop; x++ {
				rx := x - v.loc.line_voffset
				if rx >= v.uibuf.Width {
					break
				}

				if rx >= 0 {
					v.uibuf.Cells[coff+rx].Ch = ' '
				}
			}
		} else {
			if rx >= 0 {
				v.uibuf.Cells[coff+rx].Ch = r
			}
			x++
		}
		linedata = linedata[rlen:]
	}

	if v.loc.line_voffset != 0 {
		v.uibuf.Cells[coff].Ch = '←'
	}
}

// Redraw the current view to the 'v.uibuf'.
func (v *view) redraw() {
	v.uibuf.Fill(v.uibuf.Rect(), termbox.Cell{
		Ch: ' ',
		Fg: termbox.ColorDefault,
		Bg: termbox.ColorDefault,
	})

	line := v.loc.top_line
	coff := 0
	for y := 0; y < v.uibuf.Height; y++ {
		if line == nil {
			break
		}

		if line == v.loc.cursor_line {
			// special case, cursor line
			v.draw_cursor_line(line, coff)
			coff += v.uibuf.Width
			line = line.next
			continue
		}

		x := 0
		tabstop := 0
		linedata := line.data
		for {
			if len(linedata) == 0 {
				break
			}

			// advance tab stop to the next closest position
			if x == tabstop {
				tabstop += tabstop_length
			}

			if x >= v.uibuf.Width {
				last := coff + v.uibuf.Width - 1
				v.uibuf.Cells[last].Ch = '→'
				break
			}
			r, rlen := utf8.DecodeRune(linedata)
			if r == '\t' {
				// fill with spaces to the next tabstop
				for ; x < tabstop; x++ {
					if x >= v.uibuf.Width {
						break
					}

					v.uibuf.Cells[coff+x].Ch = ' '
				}
			} else {
				v.uibuf.Cells[coff+x].Ch = r
				x++
			}
			linedata = linedata[rlen:]
		}
		coff += v.uibuf.Width
		line = line.next
	}
}

// Move top line 'n' times forward or backward.
func (v *view) move_top_line_n_times(n int) {
	if n == 0 {
		return
	}

	top := v.loc.top_line
	for top.prev != nil && n < 0 {
		top = top.prev
		v.loc.top_line_num--
		n++
	}
	for top.next != nil && n > 0 {
		top = top.next
		v.loc.top_line_num++
		n--
	}
	v.loc.top_line = top
}

// Move cursor line 'n' times forward or backward.
func (v *view) move_cursor_line_n_times(n int) {
	if n == 0 {
		return
	}

	cursor := v.loc.cursor_line
	for cursor.prev != nil && n < 0 {
		cursor = cursor.prev
		v.loc.cursor_line_num--
		n++
	}
	for cursor.next != nil && n > 0 {
		cursor = cursor.next
		v.loc.cursor_line_num++
		n--
	}
	v.loc.cursor_line = cursor
}

// When 'top_line' was changed, call this function to possibly adjust the
// 'cursor_line'.
func (v *view) adjust_cursor_line() {
	cursor := v.loc.cursor_line
	co := v.loc.cursor_line_num - v.loc.top_line_num
	h := v.uibuf.Height

	if cursor.next != nil && co < view_vertical_threshold {
		v.move_cursor_line_n_times(view_vertical_threshold - co)
	}

	if cursor.prev != nil && co >= h-view_vertical_threshold {
		v.move_cursor_line_n_times((h - view_vertical_threshold) - co - 1)
	}

	cursor = v.loc.cursor_line
	bo, co, vo := cursor.find_closest_offsets(v.loc.last_cursor_voffset)
	v.loc.cursor_boffset = bo
	v.loc.cursor_coffset = co
	v.loc.cursor_voffset = vo
	v.loc.line_voffset = 0
	v.adjust_line_voffset()
}

// When 'cursor_line' was changed, call this function to possibly adjust the
// 'top_line'.
func (v *view) adjust_top_line() {
	top := v.loc.top_line
	co := v.loc.cursor_line_num - v.loc.top_line_num
	h := v.uibuf.Height

	if top.next != nil && co >= h-view_vertical_threshold {
		v.move_top_line_n_times(co - (h - view_vertical_threshold) + 1)
	}

	if top.prev != nil && co < view_vertical_threshold {
		v.move_top_line_n_times(co - view_vertical_threshold)
	}
}

// When 'cursor_voffset' was changed usually > 0, then call this function to
// possibly adjust 'line_voffset'.
func (v *view) adjust_line_voffset() {
	w := v.uibuf.Width
	vo := v.loc.line_voffset
	cvo := v.loc.cursor_voffset
	threshold := w - 1
	if vo != 0 {
		threshold -= view_horizontal_threshold - 1
	}

	if cvo-vo >= threshold {
		vo = cvo + (view_horizontal_threshold - w + 1)
	}

	if vo != 0 && cvo-vo < view_horizontal_threshold {
		vo = cvo - view_horizontal_threshold
		if vo < 0 {
			vo = 0
		}
	}

	v.loc.line_voffset = vo
}

func (v *view) cursor_position() (int, int) {
	y := v.loc.cursor_line_num - v.loc.top_line_num
	x := v.loc.cursor_voffset - v.loc.line_voffset
	return x, y
}

// Move cursor to the 'boffset' position in the 'line'. Obviously 'line' must be
// from the attached buffer. If 'boffset' < 0, use 'last_cursor_voffset'.
func (v *view) move_cursor_to(line *line, line_num int, boffset int) {
	curline := v.loc.cursor_line
	if line != curline {
		goto otherline
	}

	// quick path 1: same line, boffset == v.loc.cursor_boffset
	if boffset == v.loc.cursor_boffset || boffset < 0 {
		return
	}

	// quick path 2: same line, boffset > v.loc.cursor_boffset
	if boffset > v.loc.cursor_boffset {
		// move one character forward at a time
		for boffset != v.loc.cursor_boffset {
			r, rlen := utf8.DecodeRune(curline.data[v.loc.cursor_boffset:])
			v.loc.cursor_boffset += rlen
			v.loc.cursor_coffset += 1
			if r == '\t' {
				v.loc.cursor_voffset += tabstop_length -
					v.loc.cursor_voffset%tabstop_length
			} else {
				v.loc.cursor_voffset += 1
			}
		}
		v.loc.last_cursor_voffset = v.loc.cursor_voffset
		v.adjust_line_voffset()
		return
	}

	// quick path 3: same line, boffset == 0
	if boffset == 0 {
		v.loc.cursor_boffset = 0
		v.loc.cursor_coffset = 0
		v.loc.cursor_voffset = 0
		v.loc.last_cursor_voffset = v.loc.cursor_voffset
		v.adjust_line_voffset()
		return
	}

	// quick path 3: same line, boffset < v.loc.cursor_boffset
	if boffset < v.loc.cursor_boffset {
		// move one character back at a time, and if one or more tabs
		// were met, recalculate 'cursor_voffset'
		for boffset != v.loc.cursor_boffset {
			r, rlen := utf8.DecodeLastRune(curline.data[:v.loc.cursor_boffset])
			v.loc.cursor_boffset -= rlen
			v.loc.cursor_coffset -= 1
			if r == '\t' {
				// mark 'cursor_voffset' for recalculation
				v.loc.cursor_voffset = -1
			} else {
				v.loc.cursor_voffset -= 1
			}
		}
		if v.loc.cursor_voffset < 0 {
			v.loc.cursor_voffset = curline.voffset(boffset)
		}
		v.loc.last_cursor_voffset = v.loc.cursor_voffset
		v.adjust_line_voffset()
		return
	}

otherline:
	v.loc.cursor_line = line
	v.loc.cursor_line_num = line_num
	if boffset < 0 {
		bo, co, vo := line.find_closest_offsets(v.loc.last_cursor_voffset)
		v.loc.cursor_boffset = bo
		v.loc.cursor_coffset = co
		v.loc.cursor_voffset = vo
	} else {
		voffset, coffset := v.loc.cursor_line.voffset_coffset(boffset)
		v.loc.cursor_boffset = boffset
		v.loc.cursor_coffset = coffset
		v.loc.cursor_voffset = voffset
		v.loc.last_cursor_voffset = v.loc.cursor_voffset
	}
	v.loc.line_voffset = 0
	v.adjust_line_voffset()
	v.adjust_top_line()
}

// Move cursor one character forward.
func (v *view) move_cursor_forward() {
	line := v.loc.cursor_line
	_, rlen := utf8.DecodeRune(line.data[v.loc.cursor_boffset:])
	v.move_cursor_to(line, v.loc.cursor_line_num, v.loc.cursor_boffset+rlen)
}

// Move cursor one character backward.
func (v *view) move_cursor_backward() {
	line := v.loc.cursor_line
	_, rlen := utf8.DecodeLastRune(line.data[:v.loc.cursor_boffset])
	v.move_cursor_to(line, v.loc.cursor_line_num, v.loc.cursor_boffset-rlen)
}

// Move cursor to the next line.
func (v *view) move_cursor_next_line() {
	line := v.loc.cursor_line
	if line.next != nil {
		v.move_cursor_to(line.next, v.loc.cursor_line_num+1, -1)
	}
}

// Move cursor to the previous line.
func (v *view) move_cursor_prev_line() {
	line := v.loc.cursor_line
	if line.prev != nil {
		v.move_cursor_to(line.prev, v.loc.cursor_line_num-1, -1)
	}
}

// Move cursor to the beginning of the line.
func (v *view) move_cursor_beginning_of_line() {
	v.move_cursor_to(v.loc.cursor_line, v.loc.cursor_line_num, 0)
}

// Move cursor to the end of the line.
func (v *view) move_cursor_end_of_line() {
	line := v.loc.cursor_line
	v.move_cursor_to(line, v.loc.cursor_line_num, len(line.data))
}

// Move cursor to the beginning of the file (buffer).
func (v *view) move_cursor_beginning_of_file() {
	v.move_cursor_to(v.buf.first_line, 1, 0)
}

// Move cursor to the enf of the file (buffer).
func (v *view) move_cursor_end_of_file() {
	v.move_cursor_to(v.buf.last_line, v.buf.lines_n, len(v.buf.last_line.data))
}

// Move view 'n' lines forward or backward.
func (v *view) move_view_n_lines(n int) {
	v.move_top_line_n_times(n)
	v.adjust_cursor_line()
}

// Check if it's possible to move view 'n' lines forward or backward.
func (v *view) can_move_top_line_n_times(n int) bool {
	if n == 0 {
		return true
	}

	top := v.loc.top_line
	for top.prev != nil && n < 0 {
		top = top.prev
		n++
	}
	for top.next != nil && n > 0 {
		top = top.next
		n--
	}

	if n != 0 {
		return false
	}
	return true
}

// Move view 'n' lines forward or backward only if it's possible.
func (v *view) maybe_move_view_n_lines(n int) {
	if v.can_move_top_line_n_times(n) {
		v.move_view_n_lines(n)
	}
}

// Insert a rune 'r' at the current cursor position, advance cursor one character forward.
func (v *view) insert_rune(r rune) {
	var data [utf8.UTFMax]byte
	len := utf8.EncodeRune(data[:], r)
	v.buf.undo.insert(v, v.loc.cursor_line, v.loc.cursor_boffset, data[:len])
	v.move_cursor_to(v.loc.cursor_line, v.loc.cursor_line_num,
		v.loc.cursor_boffset+len)
}

// If at the EOL, simply insert a new line, otherwise move contents of the
// current line (from the cursor to the end of the line) to the newly created
// line.
func (v *view) new_line() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	if bo < len(line.data) {
		data := copy_byte_slice(line.data, bo, len(line.data))
		v.buf.undo.delete(v, line, bo, len(data))
		nl := v.buf.undo.insert_line(v, line)
		v.buf.undo.insert(v, nl, 0, data)
		v.move_cursor_to(nl, v.loc.cursor_line_num+1, 0)
	} else {
		nl := v.buf.undo.insert_line(v, line)
		v.move_cursor_to(nl, v.loc.cursor_line_num+1, 0)
	}
	v.buf.undo.finalize_action_group(v)
}

// If at the beginning of the line, move contents of the current line to the end
// of the previous line. Otherwise, erase one character backward.
func (v *view) backspace() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	if bo == 0 {
		if line.prev == nil {
			// beginning of the file
			return
		}
		// move the contents of the current line to the previous line
		var data []byte
		if len(line.data) > 0 {
			data = copy_byte_slice(line.data, 0, len(line.data))
			v.buf.undo.delete(v, line, 0, len(line.data))
		}
		v.buf.undo.delete_line(v, line)
		if data != nil {
			v.buf.undo.insert(v, line.prev,
				len(line.prev.data), data)
		}
		v.move_cursor_to(line.prev, v.loc.cursor_line_num-1,
			len(line.prev.data)-len(data))
		v.buf.undo.finalize_action_group(v)
		return
	}

	_, rlen := utf8.DecodeLastRune(line.data[:bo])
	v.buf.undo.delete(v, line, bo-rlen, rlen)
	v.move_cursor_to(line, v.loc.cursor_line_num, bo-rlen)
	v.buf.undo.finalize_action_group(v)
}

// If at the EOL, move contents of the next line to the end of the current line,
// erasing the next line after that. Otherwise, delete one character under the
// cursor.
func (v *view) delete() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	if bo == len(line.data) {
		if line.next == nil {
			// end of the file
			return
		}
		// move contents of the next line to the current line
		var data []byte
		if len(line.next.data) > 0 {
			data = copy_byte_slice(line.next.data, 0,
				len(line.next.data))
			v.buf.undo.delete(v, line.next, 0,
				len(line.next.data))
		}
		v.buf.undo.delete_line(v, line.next)
		if data != nil {
			v.buf.undo.insert(v, line, len(line.data), data)
		}
		v.buf.undo.finalize_action_group(v)
		return
	}

	_, rlen := utf8.DecodeRune(line.data[bo:])
	v.buf.undo.delete(v, line, bo, rlen)
	v.buf.undo.finalize_action_group(v)
}

// If not at the EOL, remove contents of the current line from the cursor to the
// end. Otherwise behave like 'delete'.
func (v *view) kill_line() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	if bo < len(line.data) {
		// kill data from the cursor to the EOL
		v.buf.undo.delete(v, line, bo, len(line.data)-bo)
		v.buf.undo.finalize_action_group(v)
		return
	}
	v.delete()
}

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
	undo       undo
}

func new_buffer() *buffer {
	b := new(buffer)
	l := new(line)
	l.next = nil
	l.prev = nil
	b.first_line = l
	b.last_line = l
	b.loc = view_location{
		top_line:        l,
		cursor_line:     l,
		top_line_num:    1,
		cursor_line_num: 1,
	}
	b.undo.init()
	return b
}

func new_buffer_from_reader(r io.Reader) (*buffer, error) {
	var err error
	var prevline *line

	br := bufio.NewReader(r)
	l := new(line)
	b := new(buffer)
	b.loc = view_location{
		top_line:        l,
		cursor_line:     l,
		top_line_num:    1,
		cursor_line_num: 1,
	}
	b.undo.init()
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

//----------------------------------------------------------------------------
// undo
//----------------------------------------------------------------------------

type action_type int

const (
	action_insert      action_type = 1
	action_insert_line action_type = 2
	action_delete      action_type = -1
	action_delete_line action_type = -2
)

type action struct {
	what   action_type
	data   []byte
	offset int
	line   *line
}

func (a *action) apply(v *view) {
	a.do(v, a.what)
}

func (a *action) revert(v *view) {
	a.do(v, -a.what)
}

func (a *action) do(v *view, what action_type) {
	switch what {
	case action_insert:
		d := a.line.data
		nl := len(d) + len(a.data)
		d = grow_byte_slice(d, nl)
		d = d[:nl]
		copy(d[a.offset+len(a.data):], d[a.offset:])
		copy(d[a.offset:], a.data)
		a.line.data = d
		// TODO: invalidate cursor from all the views of the buffer
		// except 'v'
	case action_delete:
		d := a.line.data
		copy(d[a.offset:], d[a.offset+len(a.data):])
		d = d[:len(d)-len(a.data)]
		a.line.data = d
		// TODO: invalidate cursor from all the views of the buffer
		// except 'v'
	case action_insert_line:
		p := a.line.prev
		if p == nil {
			n := v.buf.first_line
			v.buf.first_line = a.line
			a.line.prev = nil
			a.line.next = n
		} else {
			n := p.next
			p.next = a.line
			a.line.prev = p
			if n != nil {
				a.line.next = n
				n.prev = a.line
			}
		}
	case action_delete_line:
		p := a.line.prev
		n := a.line.next
		if p == nil && n == nil {
			// impossible to remove the last line
			break
		}

		if n != nil {
			n.prev = p
		}
		if p != nil {
			p.next = n
		}
	}
}

type action_group struct {
	actions                []action
	next                   *action_group
	prev                   *action_group
	before_cursor_line     *line
	before_cursor_line_num int
	before_cursor_boffset  int
	after_cursor_line      *line
	after_cursor_line_num  int
	after_cursor_boffset   int
}

type undo struct {
	cur *action_group
}

func (u *undo) init() {
	sentinel := new(action_group)
	first := new(action_group)
	sentinel.next = first
	first.prev = sentinel
	u.cur = sentinel

	// the trick here is that I set 'sentinel' as 'u.cur', it is required to
	// maintain an invariant, where 'u.cur' is a sentinel or is not empty
}

func (u *undo) maybe_next_action_group(v *view) {
	// if there are no actions and we're not at the sentinel, it means we're
	// on the tip, don't move further
	if len(u.cur.actions) == 0 && u.cur.prev != nil {
		return
	}

	// no need to move
	if u.cur.next == nil {
		return
	}

	prev := u.cur
	u.cur = u.cur.next
	u.cur.prev = prev
	u.cur.next = nil
	u.cur.actions = nil
	u.cur.before_cursor_line = v.loc.cursor_line
	u.cur.before_cursor_line_num = v.loc.cursor_line_num
	u.cur.before_cursor_boffset = v.loc.cursor_boffset
}

func (u *undo) finalize_action_group(v *view) {
	// finalize only if we're at the tip of the undo history, this function
	// will be called mainly after each cursor movement and actions alike
	// (that are supposed to finalize action group)
	if u.cur.next == nil {
		u.cur.next = new(action_group)
		u.cur.after_cursor_line = v.loc.cursor_line
		u.cur.after_cursor_line_num = v.loc.cursor_line_num
		u.cur.after_cursor_boffset = v.loc.cursor_boffset
	}
}

func (u *undo) undo(v *view) {
	if u.cur.prev == nil {
		// we're at the sentinel, no more things to undo
		return
	}

	// undo invariant tells us 'len(u.cur.actions) != 0' in case if this is
	// not a sentinel, revert the actions in the current action group
	for i := len(u.cur.actions) - 1; i >= 0; i-- {
		a := &u.cur.actions[i]
		a.revert(v)
	}
	v.move_cursor_to(u.cur.before_cursor_line, u.cur.before_cursor_line_num,
		u.cur.before_cursor_boffset)
	u.cur = u.cur.prev
}

func (u *undo) redo(v *view) {
	if u.cur.next == nil || len(u.cur.next.actions) == 0 {
		// at the tip, nothing to redo
		return
	}

	u.cur = u.cur.next
	for i := range u.cur.actions {
		a := &u.cur.actions[i]
		a.apply(v)
	}
	v.move_cursor_to(u.cur.after_cursor_line, u.cur.after_cursor_line_num,
		u.cur.after_cursor_boffset)
}

func (u *undo) append(a *action) {
	// TODO: merge
	u.cur.actions = append(u.cur.actions, *a)
}

func (u *undo) insert(v *view, line *line, offset int, data []byte) {
	u.maybe_next_action_group(v)
	a := action{
		what:   action_insert,
		data:   data,
		offset: offset,
		line:   line,
	}
	a.apply(v)
	u.append(&a)
}

func (u *undo) delete(v *view, line *line, offset int, nbytes int) {
	u.maybe_next_action_group(v)
	d := copy_byte_slice(line.data, offset, offset+nbytes)
	a := action{
		what:   action_delete,
		data:   d,
		offset: offset,
		line:   line,
	}
	a.apply(v)
	u.append(&a)
}

func (u *undo) insert_line(v *view, after *line) *line {
	u.maybe_next_action_group(v)
	a := action{
		what: action_insert_line,
		line: &line{prev: after},
	}
	a.apply(v)
	u.append(&a)
	return a.line
}

func (u *undo) delete_line(v *view, line *line) {
	u.maybe_next_action_group(v)
	a := action{
		what: action_delete_line,
		line: line,
	}
	a.apply(v)
	u.append(&a)
}

func grow_byte_slice(s []byte, desired_cap int) []byte {
	if cap(s) < desired_cap {
		ns := make([]byte, len(s), desired_cap)
		copy(ns, s)
		return ns
	}
	return s
}

func copy_byte_slice(s []byte, b, e int) []byte {
	c := make([]byte, e-b)
	copy(c, s[b:e])
	return c
}

func process_alt_ch(ch rune, v *view) {
	switch ch {
	case 'v':
		v.buf.undo.finalize_action_group(v)
		v.move_view_n_lines(-v.uibuf.Height / 2)
	case '<':
		v.buf.undo.finalize_action_group(v)
		v.move_cursor_beginning_of_file()
	case '>':
		v.buf.undo.finalize_action_group(v)
		v.move_cursor_end_of_file()
	}
}

func main() {
	if len(os.Args) != 2 {
		println("usage: godit <file>")
		return
	}
	f, _ := os.Open(os.Args[1])
	defer f.Close()
	b, _ := new_buffer_from_reader(f)

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputAlt)

	w, h := termbox.Size()
	v := new_view(w, h)
	termbox.SetCursor(v.cursor_position())
	v.attach(b)
	v.redraw()
	copy(termbox.CellBuffer(), v.uibuf.Cells)
	termbox.Flush()

	for {
		ev := termbox.PollEvent()
		switch ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyCtrlX:
				return
			case termbox.KeyCtrlF, termbox.KeyArrowRight:
				v.buf.undo.finalize_action_group(v)
				v.move_cursor_forward()
			case termbox.KeyCtrlB, termbox.KeyArrowLeft:
				v.buf.undo.finalize_action_group(v)
				v.move_cursor_backward()
			case termbox.KeyCtrlN, termbox.KeyArrowDown:
				v.buf.undo.finalize_action_group(v)
				v.move_cursor_next_line()
			case termbox.KeyCtrlP, termbox.KeyArrowUp:
				v.buf.undo.finalize_action_group(v)
				v.move_cursor_prev_line()
			case termbox.KeyCtrlE, termbox.KeyEnd:
				v.buf.undo.finalize_action_group(v)
				v.move_cursor_end_of_line()
			case termbox.KeyCtrlA, termbox.KeyHome:
				v.buf.undo.finalize_action_group(v)
				v.move_cursor_beginning_of_line()
			case termbox.KeyCtrlV, termbox.KeyPgdn:
				v.buf.undo.finalize_action_group(v)
				v.maybe_move_view_n_lines(v.uibuf.Height / 2)
			case termbox.KeyCtrlSlash:
				v.buf.undo.finalize_action_group(v)
				v.buf.undo.undo(v)
			case termbox.KeySpace:
				v.insert_rune(' ')
			case termbox.KeyEnter, termbox.KeyCtrlJ:
				v.buf.undo.finalize_action_group(v)
				v.new_line()
			case termbox.KeyBackspace, termbox.KeyBackspace2:
				v.buf.undo.finalize_action_group(v)
				v.backspace()
			case termbox.KeyDelete, termbox.KeyCtrlD:
				v.buf.undo.finalize_action_group(v)
				v.delete()
			case termbox.KeyCtrlK:
				v.buf.undo.finalize_action_group(v)
				v.kill_line()
			case termbox.KeyPgup:
				v.buf.undo.finalize_action_group(v)
				v.move_view_n_lines(-v.uibuf.Height / 2)
			case termbox.KeyCtrlR:
				v.buf.undo.finalize_action_group(v)
				v.buf.undo.redo(v)
			}

			if ev.Mod&termbox.ModAlt != 0 {
				process_alt_ch(ev.Ch, v)
			} else if ev.Ch != 0 {
				v.insert_rune(ev.Ch)
			}

			termbox.SetCursor(v.cursor_position())
			v.redraw()
			copy(termbox.CellBuffer(), v.uibuf.Cells)
			termbox.Flush()
		case termbox.EventResize:
			termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
			v.resize(ev.Width, ev.Height)
			termbox.SetCursor(v.cursor_position())
			v.redraw()
			copy(termbox.CellBuffer(), v.uibuf.Cells)
			termbox.Flush()
		}
	}
}

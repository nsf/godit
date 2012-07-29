package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"io"
	"os"
	"path/filepath"
	"unicode"
	"unicode/utf8"
)

const (
	tabstop_length            = 8
	view_vertical_threshold   = 5
	view_horizontal_threshold = 10
)

func grow_byte_slice(s []byte, desired_cap int) []byte {
	if cap(s) < desired_cap {
		ns := make([]byte, len(s), desired_cap)
		copy(ns, s)
		return ns
	}
	return s
}

func insert_bytes(s []byte, offset int, data []byte) []byte {
	n := len(s) + len(data)
	s = grow_byte_slice(s, n)
	s = s[:n]
	copy(s[offset+len(data):], s[offset:])
	copy(s[offset:], data)
	return s
}

func copy_byte_slice(s []byte, b, e int) []byte {
	c := make([]byte, e-b)
	copy(c, s[b:e])
	return c
}

func is_word(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// Function will iterate 'data' contents, calling 'cb' on some data or on '\n',
// but never both. For example, given this data: "\n123\n123\n\n", it will call
// 'cb' 6 times: ['\n', '123', '\n', '123', '\n', '\n']
func iter_lines(data []byte, cb func([]byte)) {
	offset := 0
	for {
		if offset == len(data) {
			return
		}

		i := bytes.IndexByte(data[offset:], '\n')
		switch i {
		case -1:
			cb(data[offset:])
			return
		case 0:
			cb(data[offset:offset+1])
			offset++
			continue
		}

		cb(data[offset:offset+i])
		cb(data[offset+i:offset+i+1])
		offset += i+1
	}
}



//----------------------------------------------------------------------------
// cursor location
//----------------------------------------------------------------------------

type cursor_location struct {
	line     *line
	line_num int
	boffset  int
}

func (c *cursor_location) rune_under() (rune, int) {
	return utf8.DecodeRune(c.line.data[c.boffset:])
}

func (c *cursor_location) rune_before() (rune, int) {
	return utf8.DecodeLastRune(c.line.data[:c.boffset])
}

func (c *cursor_location) first_line() bool {
	return c.line.prev == nil
}

func (c *cursor_location) last_line() bool {
	return c.line.next == nil
}

// end of line
func (c *cursor_location) eol() bool {
	return c.boffset == len(c.line.data)
}

// beginning of line
func (c *cursor_location) bol() bool {
	return c.boffset == 0
}

// returns the distance between two locations in bytes
func (a cursor_location) distance(b cursor_location) int {
	if b.line_num < a.line_num {
		a, b = b, a
	} else if a.line_num == b.line_num && b.boffset < a.boffset {
		a, b = b, a
	}

	n := 0
	for a.line != b.line {
		n += len(a.line.data) - a.boffset + 1
		a.line = a.line.next
		a.boffset = 0
	}
	n += b.boffset - a.boffset
	return n
}

func (c *cursor_location) extract_bytes(n int) []byte {
	var buf bytes.Buffer
	offset := c.boffset
	line := c.line
	for n > 0 {
		switch {
		case offset < len(line.data):
			nb := len(line.data) - offset
			if n < nb {
				nb = n
			}
			buf.Write(line.data[offset:offset+nb])
			n -= nb
			offset += nb
		case offset == len(line.data):
			buf.WriteByte('\n')
			offset = 0
			line = line.next
			n -= 1
		default:
			panic("unreachable")
		}
	}
	return buf.Bytes()
}

func (c *cursor_location) move_one_rune_forward() {
	if c.last_line() && c.eol() {
		return
	}

	if c.eol() {
		c.line = c.line.next
		c.line_num++
		c.boffset = 0
	} else {
		_, rlen := c.rune_under()
		c.boffset += rlen
	}
}

func (c *cursor_location) move_one_rune_backward() {
	if c.first_line() && c.bol() {
		return
	}

	if c.bol() {
		c.line = c.line.prev
		c.line_num--
		c.boffset = len(c.line.data)
	} else {
		_, rlen := c.rune_before()
		c.boffset -= rlen
	}
}

func (c *cursor_location) move_beginning_of_line() {
	c.boffset = 0
}

func (c *cursor_location) move_end_of_line() {
	c.boffset = len(c.line.data)
}

// returns true if the move was successful, false if EOF reached.
func (c *cursor_location) move_one_word_forward() bool {
	// move cursor forward until the first word rune is met
	for {
		if c.eol() {
			if c.last_line() {
				return false
			} else {
				c.line = c.line.next
				c.line_num++
				c.boffset = 0
				continue
			}
		}

		r, rlen := c.rune_under()
		for !is_word(r) && !c.eol() {
			c.boffset += rlen
			r, rlen = c.rune_under()
		}

		if c.eol() {
			continue
		}
		break
	}

	// now the cursor is under the word rune, skip all of them
	r, rlen := c.rune_under()
	for is_word(r) && !c.eol() {
		c.boffset += rlen
		r, rlen = c.rune_under()
	}

	return true
}

// returns true if the move was successful, false if BOF reached.
func (c *cursor_location) move_one_word_backward() bool {
	// move cursor backward while previous rune is not a word rune
	for {
		if c.bol() {
			if c.first_line() {
				return false
			} else {
				c.line = c.line.prev
				c.line_num--
				c.boffset = len(c.line.data)
				continue
			}
		}

		r, rlen := c.rune_before()
		for !is_word(r) && !c.bol() {
			c.boffset -= rlen
			r, rlen = c.rune_before()
		}

		if c.bol() {
			continue
		}
		break
	}

	// now the rune behind the cursor is a word rune, while it's true, move
	// backwards
	r, rlen := c.rune_before()
	for is_word(r) && !c.bol() {
		c.boffset -= rlen
		r, rlen = c.rune_before()
	}

	return true
}

//----------------------------------------------------------------------------
// view location
//
// This structure represents a view location in the buffer. It needs to be
// separated from the view, because it's also being saved by the buffer (in case
// if at the moment buffer has no views attached to it).
//----------------------------------------------------------------------------

type view_location struct {
	cursor       cursor_location
	top_line     *line
	top_line_num int

	// Various cursor offsets from the beginning of the line:
	// 1. in characters
	// 2. in visual cells
	// An example would be the '\t' character, which gives 1 character
	// offset, but 'tabstop_length' visual cells offset.
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
// dirty flag
//----------------------------------------------------------------------------

type dirty_flag int

const (
	dirty_contents dirty_flag = (1 << iota)
	dirty_status

	dirty_everything = dirty_contents | dirty_status
)

//----------------------------------------------------------------------------
// view
//----------------------------------------------------------------------------

type view struct {
	parent  *godit       // view is owned by a godit instance
	status  bytes.Buffer // temporary buffer for status bar text
	buf     *buffer      // currently displayed buffer
	uibuf   tulib.Buffer
	loc     view_location
	dirty   dirty_flag
	oneline bool
}

func new_view(parent *godit, buf *buffer) *view {
	v := new(view)
	v.parent = parent
	v.uibuf = tulib.NewBuffer(1, 1)
	v.attach(buf)
	return v
}

func (v *view) attach(b *buffer) {
	if v.buf == b {
		return
	}

	v.detach()
	v.buf = b
	v.loc = b.loc
	b.add_view(v)
	v.dirty = dirty_everything
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
	v.dirty = dirty_everything
}

func (v *view) height() int {
	if !v.oneline {
		return v.uibuf.Height - 1
	}
	return v.uibuf.Height
}

func (v *view) vertical_threshold() int {
	max_v_threshold := (v.height() - 1) / 2
	if view_vertical_threshold > max_v_threshold {
		return max_v_threshold
	}
	return view_vertical_threshold
}

func (v *view) horizontal_threshold() int {
	max_h_threshold := (v.width() - 1) / 2
	if view_horizontal_threshold > max_h_threshold {
		return max_h_threshold
	}
	return view_horizontal_threshold
}

func (v *view) width() int {
	// TODO: perhaps if I want to draw line numbers, I will hack it there
	return v.uibuf.Width
}

// Returns true if the line number 'line_num' is in the view.
func (v *view) in_view(line_num int) bool {
	if line_num < v.loc.top_line_num {
		return false
	}
	if line_num >= v.loc.top_line_num+v.height() {
		return false
	}
	return true
}

// This function is similar to what happens inside 'draw', but it contains a
// certain amount of specific code related to 'loc.line_voffset'. You shouldn't
// use it directly, call 'draw' instead.
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

func (v *view) draw_contents() {
	// clear the buffer
	v.uibuf.Fill(v.uibuf.Rect, termbox.Cell{
		Ch: ' ',
		Fg: termbox.ColorDefault,
		Bg: termbox.ColorDefault,
	})

	// draw lines
	line := v.loc.top_line
	coff := 0
	for y, h := 0, v.height(); y < h; y++ {
		if line == nil {
			break
		}

		if line == v.loc.cursor.line {
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

func (v *view) draw_status() {
	if v.oneline {
		return
	}

	// draw status bar
	lp := tulib.DefaultLabelParams
	lp.Bg = termbox.AttrReverse
	lp.Fg = termbox.AttrReverse | termbox.AttrBold
	v.uibuf.Fill(tulib.Rect{0, v.height(), v.uibuf.Width, 1}, termbox.Cell{
		Fg: termbox.AttrReverse,
		Bg: termbox.AttrReverse,
		Ch: '─',
	})
	fmt.Fprintf(&v.status, "  %s  ", v.buf.name)
	v.uibuf.DrawLabel(tulib.Rect{3, v.height(), v.uibuf.Width, 1},
		&lp, v.status.Bytes())

	namel := v.status.Len()
	lp.Fg = termbox.AttrReverse
	v.status.Reset()
	fmt.Fprintf(&v.status, "(%d, %d)  ", v.loc.cursor.line_num, v.loc.cursor_voffset)
	v.uibuf.DrawLabel(tulib.Rect{3 + namel, v.height(), v.uibuf.Width, 1},
		&lp, v.status.Bytes())
	v.status.Reset()
}

// Draw the current view to the 'v.uibuf'.
func (v *view) draw() {
	if v.dirty&dirty_contents != 0 {
		v.dirty &^= dirty_contents
		v.draw_contents()
	}

	if v.dirty&dirty_status != 0 {
		v.dirty &^= dirty_status
		v.draw_status()
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

	cursor := v.loc.cursor.line
	for cursor.prev != nil && n < 0 {
		cursor = cursor.prev
		v.loc.cursor.line_num--
		n++
	}
	for cursor.next != nil && n > 0 {
		cursor = cursor.next
		v.loc.cursor.line_num++
		n--
	}
	v.loc.cursor.line = cursor
}

// When 'top_line' was changed, call this function to possibly adjust the
// 'cursor_line'.
func (v *view) adjust_cursor_line() {
	vt := v.vertical_threshold()
	cursor := v.loc.cursor.line
	co := v.loc.cursor.line_num - v.loc.top_line_num
	h := v.height()

	if cursor.next != nil && co < vt {
		v.move_cursor_line_n_times(vt - co)
	}

	if cursor.prev != nil && co >= h-vt {
		v.move_cursor_line_n_times((h - vt) - co - 1)
	}

	if cursor != v.loc.cursor.line {
		cursor = v.loc.cursor.line
		bo, co, vo := cursor.find_closest_offsets(v.loc.last_cursor_voffset)
		v.loc.cursor.boffset = bo
		v.loc.cursor_coffset = co
		v.loc.cursor_voffset = vo
		v.loc.line_voffset = 0
		v.adjust_line_voffset()
		v.dirty = dirty_everything
	}
}

// When 'cursor_line' was changed, call this function to possibly adjust the
// 'top_line'.
func (v *view) adjust_top_line() {
	vt := v.vertical_threshold()
	top := v.loc.top_line
	co := v.loc.cursor.line_num - v.loc.top_line_num
	h := v.height()

	if top.next != nil && co >= h-vt {
		v.move_top_line_n_times(co - (h - vt) + 1)
		v.dirty = dirty_everything
	}

	if top.prev != nil && co < vt {
		v.move_top_line_n_times(co - vt)
		v.dirty = dirty_everything
	}
}

// When 'cursor_voffset' was changed usually > 0, then call this function to
// possibly adjust 'line_voffset'.
func (v *view) adjust_line_voffset() {
	ht := v.horizontal_threshold()
	w := v.uibuf.Width
	vo := v.loc.line_voffset
	cvo := v.loc.cursor_voffset
	threshold := w - 1
	if vo != 0 {
		threshold -= ht - 1
	}

	if cvo-vo >= threshold {
		vo = cvo + (ht - w + 1)
	}

	if vo != 0 && cvo-vo < ht {
		vo = cvo - ht
		if vo < 0 {
			vo = 0
		}
	}

	if v.loc.line_voffset != vo {
		v.loc.line_voffset = vo
		v.dirty = dirty_everything
	}
}

func (v *view) cursor_position() (int, int) {
	y := v.loc.cursor.line_num - v.loc.top_line_num
	x := v.loc.cursor_voffset - v.loc.line_voffset
	return x, y
}

// Move cursor to the 'boffset' position in the 'line'. Obviously 'line' must be
// from the attached buffer. If 'boffset' < 0, use 'last_cursor_voffset'. Keep
// in mind that there is no need to maintain connections between lines (e.g. for
// moving from a deleted line to another line).
func (v *view) move_cursor_to(c cursor_location) {
	v.dirty |= dirty_status
	if c.boffset < 0 {
		bo, co, vo := c.line.find_closest_offsets(v.loc.last_cursor_voffset)
		v.loc.cursor.boffset = bo
		v.loc.cursor_coffset = co
		v.loc.cursor_voffset = vo
	} else {
		vo, co := c.line.voffset_coffset(c.boffset)
		v.loc.cursor.boffset = c.boffset
		v.loc.cursor_coffset = co
		v.loc.cursor_voffset = vo
	}
	if c.line == v.loc.cursor.line {
		v.loc.last_cursor_voffset = v.loc.cursor_voffset
	} else {
		v.loc.line_voffset = 0
	}
	v.loc.cursor.line = c.line
	v.loc.cursor.line_num = c.line_num
	v.adjust_line_voffset()
	v.adjust_top_line()
}

// Move cursor one character forward.
func (v *view) move_cursor_forward() {
	c := v.loc.cursor
	if c.last_line() && c.eol() {
		v.parent.set_status("End of buffer")
		return
	}

	c.move_one_rune_forward()
	v.move_cursor_to(c)
}

// Move cursor one character backward.
func (v *view) move_cursor_backward() {
	c := v.loc.cursor
	if c.first_line() && c.bol() {
		v.parent.set_status("Beginning of buffer")
		return
	}

	c.move_one_rune_backward()
	v.move_cursor_to(c)
}

// Move cursor to the next line.
func (v *view) move_cursor_next_line() {
	c := v.loc.cursor
	if !c.last_line() {
		c = cursor_location{c.line.next, c.line_num + 1, -1}
		v.move_cursor_to(c)
	} else {
		v.parent.set_status("End of buffer")
	}
}

// Move cursor to the previous line.
func (v *view) move_cursor_prev_line() {
	c := v.loc.cursor
	if !c.first_line() {
		c = cursor_location{c.line.prev, c.line_num - 1, -1}
		v.move_cursor_to(c)
	} else {
		v.parent.set_status("Beginning of buffer")
	}
}

// Move cursor to the beginning of the line.
func (v *view) move_cursor_beginning_of_line() {
	c := v.loc.cursor
	c.move_beginning_of_line()
	v.move_cursor_to(c)
}

// Move cursor to the end of the line.
func (v *view) move_cursor_end_of_line() {
	c := v.loc.cursor
	c.move_end_of_line()
	v.move_cursor_to(c)
}

// Move cursor to the beginning of the file (buffer).
func (v *view) move_cursor_beginning_of_file() {
	c := cursor_location{v.buf.first_line, 1, 0}
	v.move_cursor_to(c)
}

// Move cursor to the end of the file (buffer).
func (v *view) move_cursor_end_of_file() {
	c := cursor_location{v.buf.last_line, v.buf.lines_n, len(v.buf.last_line.data)}
	v.move_cursor_to(c)
}

// Move cursor to the end of the next (or current) word.
func (v *view) move_cursor_word_forward() {
	c := v.loc.cursor
	ok := c.move_one_word_forward()
	v.move_cursor_to(c)
	if !ok {
		v.parent.set_status("End of buffer")
	}
}

func (v *view) move_cursor_word_backward() {
	c := v.loc.cursor
	ok := c.move_one_word_backward()
	v.move_cursor_to(c)
	if !ok {
		v.parent.set_status("Beginning of buffer")
	}
}

// Move view 'n' lines forward or backward.
func (v *view) move_view_n_lines(n int) {
	prevtop := v.loc.top_line_num
	v.move_top_line_n_times(n)
	v.adjust_cursor_line()
	if prevtop != v.loc.top_line_num {
		v.dirty = dirty_everything
	}
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

func (v *view) maybe_next_action_group() {
	b := v.buf
	if b.history.next == nil {
		// no need to move
		return
	}

	prev := b.history
	b.history = b.history.next
	b.history.prev = prev
	b.history.next = nil
	b.history.actions = nil
	b.history.before = v.loc.cursor
}

func (v *view) finalize_action_group() {
	b := v.buf
	// finalize only if we're at the tip of the undo history, this function
	// will be called mainly after each cursor movement and actions alike
	// (that are supposed to finalize action group)
	if b.history.next == nil {
		b.history.next = new(action_group)
		b.history.after = v.loc.cursor
	}
}

func (v *view) undo() {
	b := v.buf
	if b.history.prev == nil {
		// we're at the sentinel, no more things to undo
		return
	}

	// undo action causes finalization, always
	v.finalize_action_group()

	v.move_cursor_to(b.history.after)
	// undo invariant tells us 'len(b.history.actions) != 0' in case if this is
	// not a sentinel, revert the actions in the current action group
	for i := len(b.history.actions) - 1; i >= 0; i-- {
		a := &b.history.actions[i]
		a.revert(v)
	}
	v.move_cursor_to(b.history.before)
	v.loc.last_cursor_voffset = v.loc.cursor_voffset
	b.history = b.history.prev
}

func (v *view) redo() {
	b := v.buf
	if b.history.next == nil {
		// open group, obviously, can't move forward
		return
	}
	if len(b.history.next.actions) == 0 {
		// last finalized group, moving to the next group breaks the
		// invariant and doesn't make sense (nothing to redo)
		return
	}

	// move one entry forward, and redo all its actions
	b.history = b.history.next
	v.move_cursor_to(b.history.before)
	for i := range b.history.actions {
		a := &b.history.actions[i]
		a.apply(v)
	}
	v.move_cursor_to(b.history.after)
	v.loc.last_cursor_voffset = v.loc.cursor_voffset
}

func (v *view) action_insert(c cursor_location, data []byte) {
	v.maybe_next_action_group()
	a := action{
		what:   action_insert,
		data:   data,
		cursor: c,
		lines:  make([]*line, bytes.Count(data, []byte{'\n'})),
	}
	for i := range a.lines {
		a.lines[i] = new(line)
	}
	a.apply(v)
	v.buf.history.append(&a)
}

func (v *view) action_delete(c cursor_location, nbytes int) {
	v.maybe_next_action_group()
	d := c.extract_bytes(nbytes)
	a := action{
		what:   action_delete,
		data:   d,
		cursor: c,
		lines:  make([]*line, bytes.Count(d, []byte{'\n'})),
	}
	for i := range a.lines {
		a.lines[i] = c.line.next
		c.line = c.line.next
	}
	a.apply(v)
	v.buf.history.append(&a)
}

// Insert a rune 'r' at the current cursor position, advance cursor one character forward.
func (v *view) insert_rune(r rune) {
	var data [utf8.UTFMax]byte
	len := utf8.EncodeRune(data[:], r)
	c := v.loc.cursor
	v.action_insert(c, data[:len])
	if r == '\n' {
		c.line = c.line.next
		c.line_num++
		c.boffset = 0
	} else {
		c.boffset += len
	}
	v.move_cursor_to(c)
	v.dirty = dirty_everything
}

// If at the beginning of the line, move contents of the current line to the end
// of the previous line. Otherwise, erase one character backward.
func (v *view) delete_rune_backward() {
	c := v.loc.cursor
	if c.bol() {
		if c.first_line() {
			// beginning of the file
			v.parent.set_status("Beginning of buffer")
			return
		}
		c.line = c.line.prev
		c.line_num--
		c.boffset = len(c.line.data)
		v.action_delete(c, 1)
		v.move_cursor_to(c)
		v.dirty = dirty_everything
		return
	}

	_, rlen := c.rune_before()
	c.boffset -= rlen
	v.action_delete(c, rlen)
	v.move_cursor_to(c)
	v.dirty = dirty_everything
}

// If at the EOL, move contents of the next line to the end of the current line,
// erasing the next line after that. Otherwise, delete one character under the
// cursor.
func (v *view) delete_rune() {
	c := v.loc.cursor
	if c.eol() {
		if c.last_line() {
			// end of the file
			v.parent.set_status("End of buffer")
			return
		}
		v.action_delete(c, 1)
		v.dirty = dirty_everything
		return
	}

	_, rlen := c.rune_under()
	v.action_delete(c, rlen)
	v.dirty = dirty_everything
}

// If not at the EOL, remove contents of the current line from the cursor to the
// end. Otherwise behave like 'delete'.
func (v *view) kill_line() {
	c := v.loc.cursor
	if !c.eol() {
		// kill data from the cursor to the EOL
		v.action_delete(c, len(c.line.data)-c.boffset)
		v.dirty = dirty_everything
		return
	}
	v.delete_rune()
}

func (v *view) kill_word() {
	c1 := v.loc.cursor
	c2 := c1
	c2.move_one_word_forward()
	d := c1.distance(c2)
	if d > 0 {
		v.action_delete(c1, d)
	}
}

func (v *view) on_insert(a *action) {
	if v.loc.top_line_num+v.height() <= a.cursor.line_num {
		// inserted something below the view, don't care
		return
	}
	if a.cursor.line_num < v.loc.top_line_num {
		// inserted something above the view
		if len(a.lines) > 0 {
			// inserted one or more lines, adjust line numbers
			v.loc.top_line_num += len(a.lines)
			v.loc.cursor.line_num += len(a.lines)
			v.dirty |= dirty_status
		}
		return
	}
	if a.cursor.line_num > v.loc.cursor.line_num {
		// inserted something in the view, but below the cursor, simply
		// mark everything as dirty
		v.dirty = dirty_everything
		return
	}
	if a.cursor.line_num < v.loc.cursor.line_num {
		// inserted something above the cursor, adjust it and mark
		// everything as dirty
		v.loc.cursor.line_num += len(a.lines)
		v.adjust_top_line()
		v.dirty = dirty_everything
		return
	}

	// the last case is insertion at the cursor line, it's the most
	// complicated case
	c := v.loc.cursor
	if a.cursor.boffset < c.boffset {
		// insertion before the cursor, move cursor together with
		// insertion point
		if len(a.lines) == 0 {
			// no lines were inserted, simply adjust the offset
			c.boffset += len(a.data)
			v.move_cursor_to(c)
		} else {
			// one or more lines were inserted, adjust cursor
			// respectively
			c.line = a.last_line()
			c.line_num += len(a.lines)
			c.boffset = a.last_line_affection_len() +
				c.boffset - a.cursor.boffset
			v.move_cursor_to(c)

			// update 'last_cursor_voffset' manually here, because it
			// won't be updated automatically by 'move_cursor_to'
			v.loc.last_cursor_voffset = v.loc.cursor_voffset
		}
	}
	v.dirty = dirty_everything
}

func (v *view) on_delete(a *action) {
	if v.loc.top_line_num+v.height() <= a.cursor.line_num {
		// deleted something below the view, don't care
		return
	}
	if a.cursor.line_num < v.loc.top_line_num {
		// deletion above the top line
		if len(a.lines) == 0 {
			return
		}

		topnum := v.loc.top_line_num
		first, last := a.deleted_lines()
		if first <= topnum && topnum <= last {
			// deleted the top line, adjust the pointers
			if a.cursor.line.next != nil {
				v.loc.top_line = a.cursor.line.next
				v.loc.top_line_num = a.cursor.line_num+1
			} else {
				v.loc.top_line = a.cursor.line
				v.loc.top_line_num = a.cursor.line_num
			}
		} else {
			// no need to worry
			v.loc.top_line_num -= len(a.lines)
			v.loc.cursor.line_num -= len(a.lines)
			v.dirty |= dirty_status
			return
		}
	}
	if a.cursor.line_num > v.loc.cursor.line_num {
		// deleted something in the view, but below the cursor, simply
		// mark everything as dirty
		v.dirty = dirty_everything
		return
	}
	if a.cursor.line_num < v.loc.cursor.line_num {
		// deletion above the cursor line, may touch the cursor location
		v.dirty = dirty_everything
		if len(a.lines) == 0 {
			// no lines were deleted, no things to adjust
			return
		}

		c := v.loc.cursor
		first, last := a.deleted_lines()
		if first <= c.line_num && c.line_num <= last {
			// deleted the cursor line, see how much it affects it
			n := 0
			if last == c.line_num {
				n = c.boffset - a.last_line_affection_len()
				if n < 0 {
					n = 0
				}
			}
			c = a.cursor
			c.boffset += n
			v.move_cursor_to(c)
		} else {
			// phew.. no worries
			v.loc.cursor.line_num -= len(a.lines)
			return
		}
	}

	// the last case is deletion on the cursor line, see what was deleted
	v.dirty = dirty_everything
	c := v.loc.cursor
	if a.cursor.boffset >= c.boffset {
		// deleted something after cursor, don't care
		return
	}

	n := c.boffset - (a.cursor.boffset + a.first_line_affection_len())
	if n < 0 {
		n = 0
	}
	c.boffset = a.cursor.boffset + n
	v.move_cursor_to(c)
}

func (v *view) on_vcommand(cmd vcommand, arg rune) {
	cmdclass := cmd.class()
	if cmdclass != v.parent.lastcmdclass {
		v.parent.lastcmdclass = cmdclass
		v.finalize_action_group()
	}

	switch cmd {
	case vcommand_move_cursor_forward:
		v.move_cursor_forward()
	case vcommand_move_cursor_backward:
		v.move_cursor_backward()
	case vcommand_move_cursor_word_forward:
		v.move_cursor_word_forward()
	case vcommand_move_cursor_word_backward:
		v.move_cursor_word_backward()
	case vcommand_move_cursor_next_line:
		v.move_cursor_next_line()
	case vcommand_move_cursor_prev_line:
		v.move_cursor_prev_line()
	case vcommand_move_cursor_beginning_of_line:
		v.move_cursor_beginning_of_line()
	case vcommand_move_cursor_end_of_line:
		v.move_cursor_end_of_line()
	case vcommand_move_cursor_beginning_of_file:
		v.move_cursor_beginning_of_file()
	case vcommand_move_cursor_end_of_file:
		v.move_cursor_end_of_file()
	case vcommand_move_view_half_forward:
		v.maybe_move_view_n_lines(v.height() / 2)
	case vcommand_move_view_half_backward:
		v.move_view_n_lines(-v.height() / 2)
	case vcommand_insert_rune:
		v.insert_rune(arg)
	case vcommand_delete_rune_backward:
		v.delete_rune_backward()
	case vcommand_delete_rune:
		v.delete_rune()
	case vcommand_kill_line:
		v.kill_line()
	case vcommand_kill_word:
		v.kill_word()
	case vcommand_undo:
		v.undo()
	case vcommand_redo:
		v.redo()
	}
}

func (v *view) on_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlF, termbox.KeyArrowRight:
		v.on_vcommand(vcommand_move_cursor_forward, 0)
	case termbox.KeyCtrlB, termbox.KeyArrowLeft:
		v.on_vcommand(vcommand_move_cursor_backward, 0)
	case termbox.KeyCtrlN, termbox.KeyArrowDown:
		v.on_vcommand(vcommand_move_cursor_next_line, 0)
	case termbox.KeyCtrlP, termbox.KeyArrowUp:
		v.on_vcommand(vcommand_move_cursor_prev_line, 0)
	case termbox.KeyCtrlE, termbox.KeyEnd:
		v.on_vcommand(vcommand_move_cursor_end_of_line, 0)
	case termbox.KeyCtrlA, termbox.KeyHome:
		v.on_vcommand(vcommand_move_cursor_beginning_of_line, 0)
	case termbox.KeyCtrlV, termbox.KeyPgdn:
		v.on_vcommand(vcommand_move_view_half_forward, 0)
	case termbox.KeyCtrlSlash:
		v.on_vcommand(vcommand_undo, 0)
	case termbox.KeySpace:
		v.on_vcommand(vcommand_insert_rune, ' ')
	case termbox.KeyEnter, termbox.KeyCtrlJ:
		v.on_vcommand(vcommand_insert_rune, '\n')
	case termbox.KeyBackspace, termbox.KeyBackspace2:
		v.on_vcommand(vcommand_delete_rune_backward, 0)
	case termbox.KeyDelete, termbox.KeyCtrlD:
		v.on_vcommand(vcommand_delete_rune, 0)
	case termbox.KeyCtrlK:
		v.on_vcommand(vcommand_kill_line, 0)
	case termbox.KeyPgup:
		v.on_vcommand(vcommand_move_view_half_backward, 0)
	case termbox.KeyCtrlR:
		v.on_vcommand(vcommand_redo, 0)
	case termbox.KeyTab:
		v.on_vcommand(vcommand_insert_rune, '\t')
	}

	if ev.Mod&termbox.ModAlt != 0 {
		switch ev.Ch {
		case 'v':
			v.on_vcommand(vcommand_move_view_half_backward, 0)
		case '<':
			v.on_vcommand(vcommand_move_cursor_beginning_of_file, 0)
		case '>':
			v.on_vcommand(vcommand_move_cursor_end_of_file, 0)
		case 'f':
			v.on_vcommand(vcommand_move_cursor_word_forward, 0)
		case 'b':
			v.on_vcommand(vcommand_move_cursor_word_backward, 0)
		case 'd':
			v.on_vcommand(vcommand_kill_word, 0)
		}
	} else if ev.Ch != 0 {
		v.on_vcommand(vcommand_insert_rune, ev.Ch)
	}

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
	history    *action_group

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

//----------------------------------------------------------------------------
// action & action groups
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
	v.buf.lines_n++
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
	v.buf.lines_n--
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
			// append the contents of the deleted line the current line
			line.data = append(line.data, a.lines[nline].data...)
			// delete a line
			a.delete_line(a.lines[nline], v)
			nline++
		} else {
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
		v.buf.other_views(v, func(v *view) {
			v.on_insert(a)
		})
	case action_delete:
		a.delete(v)
		v.buf.other_views(v, func(v *view) {
			v.on_delete(a)
		})
	}
	v.dirty = dirty_everything
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
	if pa.cursor.boffset + len(pa.data) == pb.cursor.boffset {
		pa.data = append(pa.data, pb.data...)
		pa.lines = append(pa.lines, pb.lines...)
		*a = *pa
		return true
	}
	return false
}

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

//----------------------------------------------------------------------------
// view_tree
//----------------------------------------------------------------------------

type view_tree struct {
	// At the same time only one of these groups can be valid:
	// 1) 'left', 'right' and 'split'
	// 2) 'top', 'bottom' and 'split'
	// 3) 'leaf'
	left   *view_tree
	top    *view_tree
	right  *view_tree
	bottom *view_tree
	leaf   *view
	split  float32
	pos    tulib.Rect // updated with 'resize' call
}

func new_view_tree_leaf(v *view) *view_tree {
	return &view_tree{
		leaf: v,
	}
}

func (v *view_tree) split_vertically() {
	top := v.leaf
	bottom := new_view(top.parent, top.buf)
	*v = view_tree{
		top:    new_view_tree_leaf(top),
		bottom: new_view_tree_leaf(bottom),
		split:  0.5,
	}
}

func (v *view_tree) split_horizontally() {
	left := v.leaf
	right := new_view(left.parent, left.buf)
	*v = view_tree{
		left:  new_view_tree_leaf(left),
		right: new_view_tree_leaf(right),
		split: 0.5,
	}
}

func (v *view_tree) draw() {
	if v.leaf != nil {
		v.leaf.draw()
		return
	}

	if v.left != nil {
		v.left.draw()
		v.right.draw()
	} else {
		v.top.draw()
		v.bottom.draw()
	}
}

func (v *view_tree) resize(pos tulib.Rect) {
	v.pos = pos
	if v.leaf != nil {
		v.leaf.resize(pos.Width, pos.Height)
		return
	}

	if v.left != nil {
		// horizontal split, use 'w'
		w := pos.Width
		w-- // reserve one line for splitter
		lw := int(float32(w) * v.split)
		rw := w - lw
		v.left.resize(tulib.Rect{pos.X, pos.Y, lw, pos.Height})
		v.right.resize(tulib.Rect{pos.X + lw + 1, pos.Y, rw, pos.Height})
	} else {
		// vertical split, use 'h', no need to reserve one line for
		// splitter, because splitters are part of the buffer's output
		// (their status bars act like a splitter)
		h := pos.Height
		th := int(float32(h) * v.split)
		bh := h - th
		v.top.resize(tulib.Rect{pos.X, pos.Y, pos.Width, th})
		v.bottom.resize(tulib.Rect{pos.X, pos.Y + th, pos.Width, bh})
	}
}

//----------------------------------------------------------------------------
// view commands
//----------------------------------------------------------------------------

type vcommand_class int

const (
	vcommand_class_none vcommand_class = iota
	vcommand_class_movement
	vcommand_class_insertion
	vcommand_class_deletion
	vcommand_class_history
)

type vcommand int

const (
	// movement commands (finalize undo action group)
	_vcommand_movement_beg vcommand = iota
	vcommand_move_cursor_forward
	vcommand_move_cursor_backward
	vcommand_move_cursor_word_forward
	vcommand_move_cursor_word_backward
	vcommand_move_cursor_next_line
	vcommand_move_cursor_prev_line
	vcommand_move_cursor_beginning_of_line
	vcommand_move_cursor_end_of_line
	vcommand_move_cursor_beginning_of_file
	vcommand_move_cursor_end_of_file
	vcommand_move_view_half_forward
	vcommand_move_view_half_backward
	_vcommand_movement_end

	// insertion commands
	_vcommand_insertion_beg
	vcommand_insert_rune
	_vcommand_insertion_end

	// deletion commands
	_vcommand_deletion_beg
	vcommand_delete_rune_backward
	vcommand_delete_rune
	vcommand_kill_line
	vcommand_kill_word
	_vcommand_deletion_end

	// history commands (undo/redo)
	_vcommand_history_beg
	vcommand_undo
	vcommand_redo
	_vcommand_history_end
)

func (c vcommand) class() vcommand_class {
	switch {
	case c > _vcommand_movement_beg && c < _vcommand_movement_end:
		return vcommand_class_movement
	case c > _vcommand_insertion_beg && c < _vcommand_insertion_end:
		return vcommand_class_insertion
	case c > _vcommand_deletion_beg && c < _vcommand_deletion_end:
		return vcommand_class_deletion
	case c > _vcommand_history_beg && c < _vcommand_history_end:
		return vcommand_class_history
	}
	return vcommand_class_none
}

//----------------------------------------------------------------------------
// godit
//
// Main top-level structure, that handles views composition, status bar and
// input messaging. Also it's the spot where keyboard macros are implemented.
//----------------------------------------------------------------------------

type godit struct {
	uibuf        tulib.Buffer
	active       *view_tree // this one is always a leaf node
	views        *view_tree // a root node
	buffers      []*buffer
	lastcmdclass vcommand_class
	statusbuf    bytes.Buffer
	quitflag     bool
	overlay      overlay_mode
}

func new_godit(filenames []string) *godit {
	g := new(godit)
	g.buffers = make([]*buffer, 0, 20)
	for _, filename := range filenames {
		buf, err := new_buffer_from_file(filename)
		if err != nil {
			buf = new_buffer()
			buf.name = filename
		}
		g.buffers = append(g.buffers, buf)
	}
	if len(g.buffers) == 0 {
		buf := new_buffer()
		buf.name = "*new*"
		g.buffers = append(g.buffers, buf)
	}
	if g.buffers[0].path == "" {
		g.set_status("(New file)")
	}
	g.views = new_view_tree_leaf(new_view(g, g.buffers[0]))
	g.active = g.views
	return g
}

func (g *godit) set_status(format string, args ...interface{}) {
	g.statusbuf.Reset()
	fmt.Fprintf(&g.statusbuf, format, args...)
}

func (g *godit) split_horizontally() {
	g.active.split_horizontally()
	g.active = g.active.left
	g.resize()
}

func (g *godit) split_vertically() {
	g.active.split_vertically()
	g.active = g.active.top
	g.resize()
}

// Call it manually only when views layout has changed.
func (g *godit) resize() {
	g.uibuf = tulib.TermboxBuffer()
	views_area := g.uibuf.Rect
	views_area.Height -= 1 // reserve space for command line
	g.views.resize(views_area)
}

func (g *godit) draw() {
	// draw everything
	g.views.draw()
	g.composite_recursively(g.views)
	g.draw_status()

	// draw overlay if any
	if g.overlay != nil {
		g.overlay.draw()
	}

	// update cursor position
	if g.overlay == nil || !g.overlay.needs_cursor() {
		termbox.SetCursor(g.cursor_position())
	}
}

func (g *godit) draw_status() {
	lp := tulib.DefaultLabelParams
	r := g.uibuf.Rect
	r.Y = r.Height - 1
	r.Height = 1
	g.uibuf.Fill(r, termbox.Cell{Fg: lp.Fg, Bg: lp.Bg, Ch: ' '})
	g.uibuf.DrawLabel(r, &lp, g.statusbuf.Bytes())
}

func (g *godit) composite_recursively(v *view_tree) {
	if v.leaf != nil {
		g.uibuf.Blit(v.pos, 0, 0, &v.leaf.uibuf)
		return
	}

	if v.left != nil {
		g.composite_recursively(v.left)
		g.composite_recursively(v.right)
		splitter := v.right.pos
		splitter.X -= 1
		splitter.Width = 1
		g.uibuf.Fill(splitter, termbox.Cell{
			Fg: termbox.AttrReverse,
			Bg: termbox.AttrReverse,
			Ch: '│',
		})
	} else {
		g.composite_recursively(v.top)
		g.composite_recursively(v.bottom)
	}
}

func (g *godit) cursor_position() (int, int) {
	x, y := g.active.leaf.cursor_position()
	return g.active.pos.X + x, g.active.pos.Y + y
}

func (g *godit) on_sys_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlG:
		g.set_overlay_mode(nil)
		g.set_status("Quit")
	case termbox.KeyF1:
		g.buffers[0].dump_history()
	}
}

func (g *godit) on_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlX:
		g.set_overlay_mode(extended_mode{godit: g})
	default:
		g.active.leaf.on_key(ev)
	}
}

func (g *godit) handle_event(ev *termbox.Event) bool {
	switch ev.Type {
	case termbox.EventKey:
		g.set_status("") // reset status on every key event
		g.on_sys_key(ev)
		if g.overlay != nil {
			g.overlay.on_key(ev)
		} else {
			g.on_key(ev)
		}

		if g.quitflag {
			return false
		}

		g.draw()
		termbox.Flush()
	case termbox.EventResize:
		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
		g.resize()
		if g.overlay != nil {
			g.overlay.on_resize(ev)
		}
		g.draw()
		termbox.Flush()
	}
	return true
}

func (g *godit) set_overlay_mode(m overlay_mode) {
	if g.overlay != nil {
		g.overlay.exit()
	}
	if m != nil {
		m.init()
	}
	g.overlay = m
}

//----------------------------------------------------------------------------
// overlay mode
//----------------------------------------------------------------------------

type overlay_mode interface {
	needs_cursor() bool
	init()
	exit()
	draw()
	on_resize(ev *termbox.Event)
	on_key(ev *termbox.Event)
}

type default_overlay_mode struct{}

func (default_overlay_mode) needs_cursor() bool          { return false }
func (default_overlay_mode) init()                       {}
func (default_overlay_mode) exit()                       {}
func (default_overlay_mode) draw()                       {}
func (default_overlay_mode) on_resize(ev *termbox.Event) {}
func (default_overlay_mode) on_key(ev *termbox.Event)    {}

//----------------------------------------------------------------------------
// extended mode
//----------------------------------------------------------------------------

type extended_mode struct {
	godit *godit
	default_overlay_mode
}

func (e extended_mode) init() {
	e.godit.set_status("C-x")
}

func (e extended_mode) on_key(ev *termbox.Event) {
	g := e.godit

	switch ev.Key {
	case termbox.KeyCtrlC:
		g.quitflag = true
	}

	switch ev.Ch {
	case '2':
		g.split_vertically()
	case '3':
		g.split_horizontally()
	case 'o':
		if g.views.left == g.active {
			g.active = g.views.right
		} else if g.views.right == g.active {
			g.active = g.views.left
		}
	default:
		goto undefined
	}

	g.set_overlay_mode(nil)
	return
undefined:
	g.set_status("C-x %s is undefined", tulib.KeyToString(ev.Key, ev.Ch, ev.Mod))
	g.set_overlay_mode(nil)
}

func main() {
	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputAlt)

	godit := new_godit(os.Args[1:])
	godit.resize()
	godit.draw()
	termbox.SetCursor(godit.cursor_position())
	termbox.Flush()

	for {
		ev := termbox.PollEvent()
		ok := godit.handle_event(&ev)
		if !ok {
			return
		}
	}
}

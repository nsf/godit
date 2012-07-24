package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"io"
	"log"
	"os"
	"path/filepath"
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

func copy_byte_slice(s []byte, b, e int) []byte {
	c := make([]byte, e-b)
	copy(c, s[b:e])
	return c
}

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

func (v *view) redraw_contents() {
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

func (v *view) redraw_status() {
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
	fmt.Fprintf(&v.status, "(%d, %d)  ", v.loc.cursor_line_num, v.loc.cursor_voffset)
	v.uibuf.DrawLabel(tulib.Rect{3 + namel, v.height(), v.uibuf.Width, 1},
		&lp, v.status.Bytes())
	v.status.Reset()
}

// Redraw the current view to the 'v.uibuf'.
func (v *view) redraw() {
	if v.dirty&dirty_contents != 0 {
		v.dirty &^= dirty_contents
		v.redraw_contents()
	}

	if v.dirty&dirty_status != 0 {
		v.dirty &^= dirty_status
		v.redraw_status()
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
	vt := v.vertical_threshold()
	cursor := v.loc.cursor_line
	co := v.loc.cursor_line_num - v.loc.top_line_num
	h := v.height()

	if cursor.next != nil && co < vt {
		v.move_cursor_line_n_times(vt - co)
	}

	if cursor.prev != nil && co >= h-vt {
		v.move_cursor_line_n_times((h - vt) - co - 1)
	}

	if cursor != v.loc.cursor_line {
		cursor = v.loc.cursor_line
		bo, co, vo := cursor.find_closest_offsets(v.loc.last_cursor_voffset)
		v.loc.cursor_boffset = bo
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
	co := v.loc.cursor_line_num - v.loc.top_line_num
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
	y := v.loc.cursor_line_num - v.loc.top_line_num
	x := v.loc.cursor_voffset - v.loc.line_voffset
	return x, y
}

// Move cursor to the 'boffset' position in the 'line'. Obviously 'line' must be
// from the attached buffer. If 'boffset' < 0, use 'last_cursor_voffset'.
func (v *view) move_cursor_to(line *line, line_num int, boffset int) {
	v.dirty |= dirty_status
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
	} else {
		v.parent.set_status("End of buffer")
	}
}

// Move cursor to the previous line.
func (v *view) move_cursor_prev_line() {
	line := v.loc.cursor_line
	if line.prev != nil {
		v.move_cursor_to(line.prev, v.loc.cursor_line_num-1, -1)
	} else {
		v.parent.set_status("Beginning of buffer")
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
	b.history.before_cursor_line = v.loc.cursor_line
	b.history.before_cursor_line_num = v.loc.cursor_line_num
	b.history.before_cursor_boffset = v.loc.cursor_boffset
}

func (v *view) finalize_action_group() {
	b := v.buf
	// finalize only if we're at the tip of the undo history, this function
	// will be called mainly after each cursor movement and actions alike
	// (that are supposed to finalize action group)
	if b.history.next == nil {
		b.history.next = new(action_group)
		b.history.after_cursor_line = v.loc.cursor_line
		b.history.after_cursor_line_num = v.loc.cursor_line_num
		b.history.after_cursor_boffset = v.loc.cursor_boffset
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

	// undo invariant tells us 'len(b.history.actions) != 0' in case if this is
	// not a sentinel, revert the actions in the current action group
	for i := len(b.history.actions) - 1; i >= 0; i-- {
		a := &b.history.actions[i]
		a.revert(v)
	}
	v.move_cursor_to(b.history.before_cursor_line, b.history.before_cursor_line_num,
		b.history.before_cursor_boffset)
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
	for i := range b.history.actions {
		a := &b.history.actions[i]
		a.apply(v)
	}
	v.move_cursor_to(b.history.after_cursor_line, b.history.after_cursor_line_num,
		b.history.after_cursor_boffset)
}

func (v *view) action_insert(line *line, line_num, offset int, data []byte) {
	v.maybe_next_action_group()
	a := action{
		what:     action_insert,
		data:     data,
		offset:   offset,
		line:     line,
		line_num: line_num,
	}
	a.apply(v)
	v.buf.history.append(&a)
}

func (v *view) action_delete(line *line, line_num, offset, nbytes int) {
	v.maybe_next_action_group()
	d := copy_byte_slice(line.data, offset, offset+nbytes)
	a := action{
		what:     action_delete,
		data:     d,
		offset:   offset,
		line:     line,
		line_num: line_num,
	}
	a.apply(v)
	v.buf.history.append(&a)
}

func (v *view) action_insert_line(after *line, line_num int) *line {
	v.maybe_next_action_group()
	a := action{
		what:     action_insert_line,
		line:     &line{prev: after},
		line_num: line_num,
	}
	a.apply(v)
	v.buf.history.append(&a)
	return a.line
}

func (v *view) action_delete_line(line *line, line_num int) {
	v.maybe_next_action_group()
	a := action{
		what:     action_delete_line,
		line:     line,
		line_num: line_num,
	}
	a.apply(v)
	v.buf.history.append(&a)
}

// Insert a rune 'r' at the current cursor position, advance cursor one character forward.
func (v *view) insert_rune(r rune) {
	var data [utf8.UTFMax]byte
	len := utf8.EncodeRune(data[:], r)
	v.action_insert(v.loc.cursor_line, v.loc.cursor_line_num,
		v.loc.cursor_boffset, data[:len])
	v.move_cursor_to(v.loc.cursor_line, v.loc.cursor_line_num,
		v.loc.cursor_boffset+len)
	v.dirty = dirty_everything
}

// If at the EOL, simply insert a new line, otherwise move contents of the
// current line (from the cursor to the end of the line) to the newly created
// line.
func (v *view) new_line() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	line_num := v.loc.cursor_line_num
	if bo < len(line.data) {
		data := copy_byte_slice(line.data, bo, len(line.data))
		v.action_delete(line, line_num, bo, len(data))
		nl := v.action_insert_line(line, line_num+1)
		v.action_insert(nl, line_num+1, 0, data)
		v.move_cursor_to(nl, line_num+1, 0)
	} else {
		nl := v.action_insert_line(line, line_num+1)
		v.move_cursor_to(nl, line_num+1, 0)
	}
	v.dirty = dirty_everything
}

// If at the beginning of the line, move contents of the current line to the end
// of the previous line. Otherwise, erase one character backward.
func (v *view) delete_rune_backward() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	line_num := v.loc.cursor_line_num
	if bo == 0 {
		if line.prev == nil {
			// beginning of the file
			return
		}
		// move the contents of the current line to the previous line
		var data []byte
		if len(line.data) > 0 {
			data = copy_byte_slice(line.data, 0, len(line.data))
			v.action_delete(line, line_num, 0, len(line.data))
		}
		v.action_delete_line(line, line_num)
		if data != nil {
			v.action_insert(line.prev, line_num-1, len(line.prev.data), data)
		}
		v.move_cursor_to(line.prev, line_num-1, len(line.prev.data)-len(data))
		v.dirty = dirty_everything
		return
	}

	_, rlen := utf8.DecodeLastRune(line.data[:bo])
	v.action_delete(line, line_num, bo-rlen, rlen)
	v.move_cursor_to(line, line_num, bo-rlen)
	v.dirty = dirty_everything
}

// If at the EOL, move contents of the next line to the end of the current line,
// erasing the next line after that. Otherwise, delete one character under the
// cursor.
func (v *view) delete_rune() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	line_num := v.loc.cursor_line_num
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
			v.action_delete(line.next, line_num+1, 0, len(line.next.data))
		}
		v.action_delete_line(line.next, line_num+1)
		if data != nil {
			v.action_insert(line, line_num, len(line.data), data)
		}
		v.dirty = dirty_everything
		return
	}

	_, rlen := utf8.DecodeRune(line.data[bo:])
	v.action_delete(line, line_num, bo, rlen)
	v.dirty = dirty_everything
}

// If not at the EOL, remove contents of the current line from the cursor to the
// end. Otherwise behave like 'delete'.
func (v *view) kill_line() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	line_num := v.loc.cursor_line_num
	if bo < len(line.data) {
		// kill data from the cursor to the EOL
		v.action_delete(line, line_num, bo, len(line.data)-bo)
		v.dirty = dirty_everything
		return
	}
	v.delete_rune()
}

func (v *view) restore_cursor_from_boffset() {
	voffset, coffset := v.loc.cursor_line.voffset_coffset(v.loc.cursor_boffset)
	v.loc.cursor_coffset = coffset
	v.loc.cursor_voffset = voffset
	v.loc.last_cursor_voffset = v.loc.cursor_voffset
}

func (v *view) on_insert(line *line, line_num int) {
	v.buf.other_views(v, func(v *view) {
		if v.in_view(line_num) {
			v.dirty = dirty_everything
		}

		if v.loc.cursor_line != line {
			return
		}

		v.restore_cursor_from_boffset()
		v.adjust_line_voffset()
		v.dirty |= dirty_status
	})
}

func (v *view) on_delete(line *line, line_num int) {
	v.buf.other_views(v, func(v *view) {
		if v.in_view(line_num) {
			v.dirty = dirty_everything
		}

		if v.loc.cursor_line != line {
			return
		}

		if len(line.data) < v.loc.cursor_boffset {
			v.loc.cursor_boffset = len(line.data)
		}

		v.restore_cursor_from_boffset()
		v.adjust_line_voffset()
		v.dirty |= dirty_status
	})
}

func (v *view) on_insert_line(line *line, line_num int) {
	v.buf.other_views(v, func(v *view) {
		if v.loc.top_line_num+v.height() <= line_num {
			// inserted line is somewhere below the view, don't care
			return
		}

		if line_num <= v.loc.top_line_num {
			// line was inserted somewhere before the top line, adjust it
			v.loc.top_line_num++
			v.loc.cursor_line_num++
			v.dirty |= dirty_status
			return
		}

		if line_num > v.loc.cursor_line_num {
			// line is below the top line and cursor line, but still
			// is in the view, mark view as dirty, return
			v.dirty = dirty_everything
			return
		}

		// line was inserted somewhere before the cursor, but
		// after the top line, adjust it
		v.loc.cursor_line_num++
		v.adjust_top_line()
		v.dirty = dirty_everything
	})
}

func (v *view) on_delete_line(line *line, line_num int) {
	v.buf.other_views(v, func(v *view) {
		if v.in_view(line_num) {
			v.dirty = dirty_everything
		}

		if v.loc.top_line == line {
			if line.next != nil {
				v.loc.top_line = line.next
			} else {
				v.loc.top_line = line.prev
				v.loc.top_line_num--
			}
		} else if line_num < v.loc.top_line_num {
			v.loc.top_line_num--
			v.loc.cursor_line_num--
			v.dirty |= dirty_status
			return
		}

		if v.loc.cursor_line == line {
			if line.next != nil {
				v.loc.cursor_line = line.next
				v.loc.cursor_boffset = 0
				v.loc.cursor_coffset = 0
				v.loc.cursor_voffset = 0
				v.loc.last_cursor_voffset = 0
			} else {
				v.loc.cursor_line = line.prev
				v.loc.cursor_line_num--
				v.loc.cursor_boffset = len(line.prev.data)
				v.restore_cursor_from_boffset()
				v.adjust_line_voffset()
				v.adjust_top_line()
			}
			v.dirty |= dirty_status
		} else if line_num < v.loc.cursor_line_num {
			v.loc.cursor_line_num--
			v.dirty |= dirty_status
		}
	})
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
		top_line:        l,
		cursor_line:     l,
		top_line_num:    1,
		cursor_line_num: 1,
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
		top_line:        l,
		cursor_line:     l,
		top_line_num:    1,
		cursor_line_num: 1,
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
	ag := b.history
	for ag.prev != nil {
		ag = ag.prev
	}
	i := 0
	for ag != nil {
		log.Printf("action group %d, %d entries\n", i, len(ag.actions))
		for i := range ag.actions {
			a := &ag.actions[i]
			switch a.what {
			case action_insert:
				log.Printf("\tinsert %p, %d:%d (%s)\n",
					a.line, a.offset, len(a.data), string(a.data))
			case action_delete:
				log.Printf("\tdelete %p, %d:%d (%s)\n",
					a.line, a.offset, len(a.data), string(a.data))
			case action_insert_line:
				log.Printf("\tinsert line %p\n", a.line)
			case action_delete_line:
				log.Printf("\tdelete line %p\n", a.line)
			}
		}
		ag = ag.next
	}
}

//----------------------------------------------------------------------------
// action & action groups
//----------------------------------------------------------------------------

type action_type int

const (
	action_insert      action_type = 1
	action_insert_line action_type = 2
	action_delete      action_type = -1
	action_delete_line action_type = -2
)

type action struct {
	what     action_type
	data     []byte
	offset   int
	line     *line
	line_num int
}

func (a *action) try_merge(b *action) bool {
	if a.what != b.what {
		// we can only merge things which have the same action type
		return false
	}
	if a.line != b.line {
		// we can only merge things which are on the same line
		return false
	}

	// TODO compressing "delete_rune" actions is broken
	switch a.what {
	case action_insert, action_delete:
		if a.offset+len(a.data) == b.offset {
			a.data = append(a.data, b.data...)
			return true
		}
		if b.offset+len(b.data) == a.offset {
			*a, *b = *b, *a
			a.data = append(a.data, b.data...)
			return true
		}
	}
	return false
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
		v.on_insert(a.line, a.line_num)
	case action_delete:
		d := a.line.data
		copy(d[a.offset:], d[a.offset+len(a.data):])
		d = d[:len(d)-len(a.data)]
		a.line.data = d
		v.on_delete(a.line, a.line_num)
	case action_insert_line:
		var bi, ai *line // before insertion and after insertion lines
		v.buf.lines_n++
		bi = a.line.prev
		if bi == nil {
			// inserting the first line
			// bi == nil
			// ai == v.buf.first_line

			ai = v.buf.first_line
			v.buf.first_line = a.line
		} else {
			ai = bi.next
			if ai == nil {
				// inserting the last line
				// bi == v.buf.last_line
				// ai == nil

				v.buf.last_line = a.line
			}
		}

		if bi != nil {
			bi.next = a.line
		}
		if ai != nil {
			ai.prev = a.line
		}
		a.line.prev = bi
		a.line.next = ai

		v.on_insert_line(a.line, a.line_num)
	case action_delete_line:
		v.buf.lines_n--
		bi := a.line.prev
		ai := a.line.next
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
		v.on_delete_line(a.line, a.line_num)
	}
	v.dirty = dirty_everything
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

func (v *view_tree) redraw() {
	if v.leaf != nil {
		v.leaf.redraw()
		return
	}

	if v.left != nil {
		v.left.redraw()
		v.right.redraw()
	} else {
		v.top.redraw()
		v.bottom.redraw()
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
	vcommand_new_line
	_vcommand_insertion_end

	// deletion commands
	_vcommand_deletion_beg
	vcommand_delete_rune_backward
	vcommand_delete_rune
	vcommand_kill_line
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
}

func (g *godit) split_vertically() {
	g.active.split_vertically()
	g.active = g.active.top
}

// Call it manually only when views layout has changed.
func (g *godit) resize() {
	g.uibuf = tulib.TermboxBuffer()
	views_area := g.uibuf.Rect
	views_area.Height -= 1 // reserve space for command line
	g.views.resize(views_area)
}

func (g *godit) redraw() {
	w, h := termbox.Size()
	if w != g.uibuf.Width || h != g.uibuf.Height {
		g.resize()
	}
	g.views.redraw()
	g.composite_recursively(g.views)
	g.draw_status()
}

func (g *godit) draw_status() {
	lp := tulib.DefaultLabelParams
	r := g.uibuf.Rect
	r.Y = r.Height-1
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

func (g *godit) handle_command(cmd vcommand, arg rune) {
	v := g.active.leaf

	class := cmd.class()
	if class != g.lastcmdclass {
		g.lastcmdclass = class
		v.finalize_action_group()
	}

	switch cmd {
	case vcommand_move_cursor_forward:
		v.move_cursor_forward()
	case vcommand_move_cursor_backward:
		v.move_cursor_backward()
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
	case vcommand_new_line:
		v.new_line()
	case vcommand_delete_rune_backward:
		v.delete_rune_backward()
	case vcommand_delete_rune:
		v.delete_rune()
	case vcommand_kill_line:
		v.kill_line()
	case vcommand_undo:
		v.undo()
	case vcommand_redo:
		v.redo()
	}
}

func (g *godit) handle_event(ev *termbox.Event) bool {
	switch ev.Type {
	case termbox.EventKey:
		g.set_status("") // reset status on every key event
		switch ev.Key {
		case termbox.KeyCtrlX:
			return false
		case termbox.KeyCtrlF, termbox.KeyArrowRight:
			g.handle_command(vcommand_move_cursor_forward, 0)
		case termbox.KeyCtrlB, termbox.KeyArrowLeft:
			g.handle_command(vcommand_move_cursor_backward, 0)
		case termbox.KeyCtrlN, termbox.KeyArrowDown:
			g.handle_command(vcommand_move_cursor_next_line, 0)
		case termbox.KeyCtrlP, termbox.KeyArrowUp:
			g.handle_command(vcommand_move_cursor_prev_line, 0)
		case termbox.KeyCtrlE, termbox.KeyEnd:
			g.handle_command(vcommand_move_cursor_end_of_line, 0)
		case termbox.KeyCtrlA, termbox.KeyHome:
			g.handle_command(vcommand_move_cursor_beginning_of_line, 0)
		case termbox.KeyCtrlV, termbox.KeyPgdn:
			g.handle_command(vcommand_move_view_half_forward, 0)
		case termbox.KeyCtrlSlash:
			g.handle_command(vcommand_undo, 0)
		case termbox.KeySpace:
			g.handle_command(vcommand_insert_rune, ' ')
		case termbox.KeyEnter, termbox.KeyCtrlJ:
			g.handle_command(vcommand_new_line, 0)
		case termbox.KeyBackspace, termbox.KeyBackspace2:
			g.handle_command(vcommand_delete_rune_backward, 0)
		case termbox.KeyDelete, termbox.KeyCtrlD:
			g.handle_command(vcommand_delete_rune, 0)
		case termbox.KeyCtrlK:
			g.handle_command(vcommand_kill_line, 0)
		case termbox.KeyPgup:
			g.handle_command(vcommand_move_view_half_backward, 0)
		case termbox.KeyCtrlR:
			g.handle_command(vcommand_redo, 0)
		case termbox.KeyTab:
			g.handle_command(vcommand_insert_rune, '\t')
		case termbox.KeyF1:
			g.active.leaf.buf.dump_history()
		case termbox.KeyF2:
			g.split_horizontally()
			g.resize()
		case termbox.KeyF3:
			g.split_vertically()
			g.resize()
		case termbox.KeyF4:
			if g.views.left == g.active {
				g.active = g.views.right
			} else if g.views.right == g.active {
				g.active = g.views.left
			}
		}

		if ev.Mod&termbox.ModAlt != 0 {
			switch ev.Ch {
			case 'v':
				g.handle_command(vcommand_move_view_half_backward, 0)
			case '<':
				g.handle_command(vcommand_move_cursor_beginning_of_file, 0)
			case '>':
				g.handle_command(vcommand_move_cursor_end_of_file, 0)
			}
		} else if ev.Ch != 0 {
			g.handle_command(vcommand_insert_rune, ev.Ch)
		}

		g.redraw()
		termbox.SetCursor(g.cursor_position())
		termbox.Flush()
	case termbox.EventResize:
		// clear the backbuffer, to apply the new size
		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
		g.redraw()
		termbox.SetCursor(g.cursor_position())
		termbox.Flush()
	}
	return true
}

func main() {
	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputAlt)

	godit := new_godit(os.Args[1:])
	godit.redraw()
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

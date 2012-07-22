package main

import (
	"bufio"
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
	dirty bool
}

func new_view(w, h int) *view {
	v := new(view)
	v.uibuf = tulib.NewBuffer(w, h)
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
	v.dirty = true
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
	if v.buf != nil {
		v.adjust_line_voffset()
		v.adjust_top_line()
		v.dirty = true
	}
}

func (v *view) height() int {
	return v.uibuf.Height - 1
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

// returns:
// -1 if the line is somewhere above the view
//  0 if the line is in the view
// +1 if the line is somewhere below the view
func (v *view) in_view(line_num int) int {
	if line_num < v.loc.top_line_num {
		return -1
	}
	if line_num >= v.loc.top_line_num + v.height() {
		return 1
	}
	return 0
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
	if !v.dirty {
		return
	}
	v.dirty = false

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

	// draw status bar
	lp := tulib.DefaultLabelParams
	lp.Bg = termbox.AttrReverse
	lp.Fg = termbox.AttrReverse | termbox.AttrBold
	v.uibuf.Fill(tulib.Rect{0, v.height(), v.uibuf.Width, 1}, termbox.Cell{
		Fg: termbox.AttrReverse,
		Bg: termbox.AttrReverse,
		Ch: '─',
	})
	v.uibuf.DrawLabel(tulib.Rect{3, v.height(), v.uibuf.Width, 1},
		&lp, "  "+v.buf.name+"  ")
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
		v.dirty = true
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
		v.dirty = true
	}

	if top.prev != nil && co < vt {
		v.move_top_line_n_times(co - vt)
		v.dirty = true
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
		v.dirty = true
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
	prevtop := v.loc.top_line_num
	v.move_top_line_n_times(n)
	v.adjust_cursor_line()
	if prevtop != v.loc.top_line_num {
		v.dirty = true
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

// Shortcut for v.buf.undo.finalize_action_group(v)
func (v *view) finalize_action_group() {
	v.buf.undo.finalize_action_group(v)
}

// Shortcut for v.buf.undo.insert(v, ...)
func (v *view) insert(line *line, line_num, offset int, data []byte) {
	v.buf.undo.insert(v, line, line_num, offset, data)
}

// Shortcut for v.buf.undo.delete(v, ...)
func (v *view) delete(line *line, line_num, offset, nbytes int) {
	v.buf.undo.delete(v, line, line_num, offset, nbytes)
}

// Shortcut for v.buf.undo.insert_line(v, ...)
func (v *view) insert_line(after *line, line_num int) *line {
	return v.buf.undo.insert_line(v, after, line_num)
}

// Shortcut for v.buf.undo.delete_line(v, ...)
func (v *view) delete_line(line *line, line_num int) {
	v.buf.undo.delete_line(v, line, line_num)
}

// Insert a rune 'r' at the current cursor position, advance cursor one character forward.
func (v *view) insert_rune(r rune) {
	var data [utf8.UTFMax]byte
	len := utf8.EncodeRune(data[:], r)
	v.insert(v.loc.cursor_line, v.loc.cursor_line_num,
		v.loc.cursor_boffset, data[:len])
	v.move_cursor_to(v.loc.cursor_line, v.loc.cursor_line_num,
		v.loc.cursor_boffset+len)
	v.dirty = true
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
		v.delete(line, line_num, bo, len(data))
		nl := v.insert_line(line, line_num+1)
		v.insert(nl, line_num+1, 0, data)
		v.move_cursor_to(nl, line_num+1, 0)
	} else {
		nl := v.insert_line(line, line_num+1)
		v.move_cursor_to(nl, line_num+1, 0)
	}
	v.dirty = true
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
			v.delete(line, line_num, 0, len(line.data))
		}
		v.delete_line(line, line_num)
		if data != nil {
			v.insert(line.prev, line_num-1, len(line.prev.data), data)
		}
		v.move_cursor_to(line.prev, line_num-1, len(line.prev.data)-len(data))
		v.dirty = true
		return
	}

	_, rlen := utf8.DecodeLastRune(line.data[:bo])
	v.delete(line, line_num, bo-rlen, rlen)
	v.move_cursor_to(line, line_num, bo-rlen)
	v.dirty = true
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
			v.delete(line.next, line_num + 1, 0, len(line.next.data))
		}
		v.delete_line(line.next, line_num + 1)
		if data != nil {
			v.insert(line, line_num, len(line.data), data)
		}
		v.dirty = true
		return
	}

	_, rlen := utf8.DecodeRune(line.data[bo:])
	v.delete(line, line_num, bo, rlen)
	v.dirty = true
}

// If not at the EOL, remove contents of the current line from the cursor to the
// end. Otherwise behave like 'delete'.
func (v *view) kill_line() {
	bo := v.loc.cursor_boffset
	line := v.loc.cursor_line
	line_num := v.loc.cursor_line_num
	if bo < len(line.data) {
		// kill data from the cursor to the EOL
		v.delete(line, line_num, bo, len(line.data)-bo)
		v.dirty = true
		return
	}
	v.delete_rune()
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
	b.undo.init()
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

func (b *buffer) other_views(v *view, cb func(*view)) {
	for _, ov := range b.views {
		if v == ov {
			continue
		}
		cb(ov)
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
	case action_delete:
		d := a.line.data
		copy(d[a.offset:], d[a.offset+len(a.data):])
		d = d[:len(d)-len(a.data)]
		a.line.data = d
	case action_insert_line:
		v.buf.lines_n++
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
		v.buf.lines_n--
		p := a.line.prev
		n := a.line.next
		if n != nil {
			n.prev = p
		}
		if p != nil {
			p.next = n
		}
	}
	v.dirty = true
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

	// undo action causes finalization, always
	u.finalize_action_group(v)

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
	if u.cur.next == nil {
		// open group, obviously, can't move forward
		return
	}
	if len(u.cur.next.actions) == 0 {
		// last finalized group, moving to the next group breaks the
		// invariant and doesn't make sense (nothing to redo)
		return
	}

	// move one entry forward, and redo all its actions
	u.cur = u.cur.next
	for i := range u.cur.actions {
		a := &u.cur.actions[i]
		a.apply(v)
	}
	v.move_cursor_to(u.cur.after_cursor_line, u.cur.after_cursor_line_num,
		u.cur.after_cursor_boffset)
}

func (u *undo) append(a *action) {
	if len(u.cur.actions) != 0 {
		// Oh, we have something in the group already, let's try to
		// merge this action with the last one.
		last := &u.cur.actions[len(u.cur.actions)-1]
		if last.try_merge(a) {
			return
		}
	}
	u.cur.actions = append(u.cur.actions, *a)
}

func (u *undo) insert(v *view, line *line, line_num int, offset int, data []byte) {
	u.maybe_next_action_group(v)
	a := action{
		what:     action_insert,
		data:     data,
		offset:   offset,
		line:     line,
		line_num: line_num,
	}
	a.apply(v)
	u.append(&a)
}

func (u *undo) delete(v *view, line *line, line_num int, offset int, nbytes int) {
	u.maybe_next_action_group(v)
	d := copy_byte_slice(line.data, offset, offset+nbytes)
	a := action{
		what:     action_delete,
		data:     d,
		offset:   offset,
		line:     line,
		line_num: line_num,
	}
	a.apply(v)
	u.append(&a)
}

func (u *undo) insert_line(v *view, after *line, line_num int) *line {
	u.maybe_next_action_group(v)
	a := action{
		what:     action_insert_line,
		line:     &line{prev: after},
		line_num: line_num,
	}
	a.apply(v)
	u.append(&a)
	return a.line
}

func (u *undo) delete_line(v *view, line *line, line_num int) {
	u.maybe_next_action_group(v)
	a := action{
		what:     action_delete_line,
		line:     line,
		line_num: line_num,
	}
	a.apply(v)
	u.append(&a)
}

func (u *undo) dump_history() {
	ag := u.cur
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
	bottom := new_view(1, 1)
	bottom.attach(top.buf)
	*v = view_tree{
		top:    new_view_tree_leaf(top),
		bottom: new_view_tree_leaf(bottom),
		split:  0.5,
	}
}

func (v *view_tree) split_horizontally() {
	left := v.leaf
	right := new_view(1, 1)
	right.attach(left.buf)
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
// godit
//
// Main top-level structure, that handles views composition, command line and
// input messaging. Also it's the spot where keyboard macros are implemented.
//----------------------------------------------------------------------------

type godit struct {
	uibuf   tulib.Buffer
	active  *view_tree // this one is always a leaf node
	views   *view_tree // a root node
	buffers []*buffer
}

func new_godit() *godit {
	g := new(godit)
	g.views = new_view_tree_leaf(new_view(1, 1))
	g.active = g.views
	g.buffers = make([]*buffer, 0, 20)
	g.resize()
	return g
}

func (g *godit) split_horizontally() {
	g.active.split_horizontally()
	g.active = g.active.left
}

func (g *godit) split_vertically() {
	g.active.split_vertically()
	g.active = g.active.top
}

func (g *godit) resize() {
	g.uibuf = tulib.TermboxBuffer()
	g.views.resize(g.uibuf.Rect)
}

func (g *godit) open_file(filename string) {
	// TODO: use g.buffers
	buf, err := new_buffer_from_file(filename)
	if err != nil {
		panic(err)
	}

	g.active.leaf.attach(buf)
}

func (g *godit) redraw() {
	g.views.redraw()
	g.redraw_recursive(g.views)
}

func (g *godit) redraw_recursive(v *view_tree) {
	if v.leaf != nil {
		g.uibuf.Blit(v.pos, 0, 0, &v.leaf.uibuf)
		return
	}

	if v.left != nil {
		g.redraw_recursive(v.left)
		g.redraw_recursive(v.right)
		splitter := v.right.pos
		splitter.X -= 1
		splitter.Width = 1
		g.uibuf.Fill(splitter, termbox.Cell{
			Fg: termbox.AttrReverse,
			Bg: termbox.AttrReverse,
			Ch: '│',
		})
	} else {
		g.redraw_recursive(v.top)
		g.redraw_recursive(v.bottom)
	}
}

func (g *godit) cursor_position() (int, int) {
	x, y := g.active.leaf.cursor_position()
	return g.active.pos.X + x, g.active.pos.Y + y
}

func handle_alt_ch(ch rune, v *view) {
	switch ch {
	case 'v':
		v.finalize_action_group()
		v.move_view_n_lines(-v.height() / 2)
	case '<':
		v.finalize_action_group()
		v.move_cursor_beginning_of_file()
	case '>':
		v.finalize_action_group()
		v.move_cursor_end_of_file()
	}
}

func (g *godit) handle_event(ev *termbox.Event) bool {
	v := g.active.leaf
	switch ev.Type {
	case termbox.EventKey:
		switch ev.Key {
		case termbox.KeyCtrlX:
			return false
		case termbox.KeyCtrlF, termbox.KeyArrowRight:
			v.finalize_action_group()
			v.move_cursor_forward()
		case termbox.KeyCtrlB, termbox.KeyArrowLeft:
			v.finalize_action_group()
			v.move_cursor_backward()
		case termbox.KeyCtrlN, termbox.KeyArrowDown:
			v.finalize_action_group()
			v.move_cursor_next_line()
		case termbox.KeyCtrlP, termbox.KeyArrowUp:
			v.finalize_action_group()
			v.move_cursor_prev_line()
		case termbox.KeyCtrlE, termbox.KeyEnd:
			v.finalize_action_group()
			v.move_cursor_end_of_line()
		case termbox.KeyCtrlA, termbox.KeyHome:
			v.finalize_action_group()
			v.move_cursor_beginning_of_line()
		case termbox.KeyCtrlV, termbox.KeyPgdn:
			v.finalize_action_group()
			v.maybe_move_view_n_lines(v.height() / 2)
		case termbox.KeyCtrlSlash:
			v.buf.undo.undo(v)
		case termbox.KeySpace:
			v.insert_rune(' ')
		case termbox.KeyEnter, termbox.KeyCtrlJ:
			v.finalize_action_group()
			v.new_line()
		case termbox.KeyBackspace, termbox.KeyBackspace2:
			v.finalize_action_group()
			v.delete_rune_backward()
		case termbox.KeyDelete, termbox.KeyCtrlD:
			v.finalize_action_group()
			v.delete_rune()
		case termbox.KeyCtrlK:
			v.finalize_action_group()
			v.kill_line()
		case termbox.KeyPgup:
			v.finalize_action_group()
			v.move_view_n_lines(-v.height() / 2)
		case termbox.KeyCtrlR:
			v.buf.undo.redo(v)
		case termbox.KeyF1:
			v.buf.undo.dump_history()
		case termbox.KeyF2:
			g.split_horizontally()
			g.resize()
		case termbox.KeyF3:
			g.split_vertically()
			g.resize()
		}

		if ev.Mod&termbox.ModAlt != 0 {
			handle_alt_ch(ev.Ch, v)
		} else if ev.Ch != 0 {
			v.insert_rune(ev.Ch)
		}

		g.redraw()
		termbox.SetCursor(g.cursor_position())
		termbox.Flush()
	case termbox.EventResize:
		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
		g.resize()
		g.redraw()
		termbox.SetCursor(g.cursor_position())
		termbox.Flush()
	}
	return true
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

func main() {
	if len(os.Args) != 2 {
		println("usage: godit <file>")
		return
	}

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputAlt)

	godit := new_godit()
	godit.open_file(os.Args[1])
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

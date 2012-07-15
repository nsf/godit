package main

import (
	"bufio"
	"fmt"
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

// This function is similar to what happens inside 'redraw', but it contains
// a certain amount of specific code related to 'loc.line_voffset'.
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

func (v *view) move_cursor_forward() {
	line := v.loc.cursor_line
	if v.loc.cursor_boffset >= len(line.data) {
		// move to the beginning of the next line if possible
		if line.next != nil {
			v.loc.cursor_line = line.next
			v.loc.cursor_boffset = 0
			v.loc.cursor_coffset = 0
			v.loc.cursor_voffset = 0
			v.loc.last_cursor_voffset = 0
			v.loc.cursor_line_num++
			v.loc.line_voffset = 0
			v.adjust_top_line()
		}
		return
	}

	r, rlen := utf8.DecodeRune(line.data[v.loc.cursor_boffset:])
	v.loc.cursor_boffset += rlen
	v.loc.cursor_coffset += 1
	if r == '\t' {
		next_tabstop := tabstop_length -
			v.loc.cursor_voffset%tabstop_length
		v.loc.cursor_voffset += next_tabstop
	} else {
		v.loc.cursor_voffset += 1
	}
	v.loc.last_cursor_voffset = v.loc.cursor_voffset
	v.adjust_line_voffset()
}

func (v *view) move_cursor_backward() {
	line := v.loc.cursor_line
	if v.loc.cursor_boffset == 0 {
		// move to the end of the next line if possible
		if line.prev != nil {
			cl, vl := line.prev.lengths()
			v.loc.cursor_line = line.prev
			v.loc.cursor_boffset = len(line.prev.data)
			v.loc.cursor_coffset = cl
			v.loc.cursor_voffset = vl
			v.loc.last_cursor_voffset = v.loc.cursor_voffset
			v.loc.cursor_line_num--
			v.loc.line_voffset = 0
			v.adjust_line_voffset()
			v.adjust_top_line()
		}
		return
	}

	r, rlen := utf8.DecodeLastRune(line.data[:v.loc.cursor_boffset])
	v.loc.cursor_boffset -= rlen
	v.loc.cursor_coffset -= 1
	if r == '\t' {
		// that's fucked up a bit, but we can't really stride tabstops
		// backwards
		v.loc.cursor_voffset = line.voffset(v.loc.cursor_boffset)
	} else {
		v.loc.cursor_voffset -= 1
	}
	v.loc.last_cursor_voffset = v.loc.cursor_voffset
	v.adjust_line_voffset()
}

func (v *view) move_cursor_next_line() {
	line := v.loc.cursor_line
	if line.next != nil {
		bo, co, vo := line.next.find_closest_offsets(v.loc.last_cursor_voffset)
		v.loc.cursor_line = line.next
		v.loc.cursor_boffset = bo
		v.loc.cursor_coffset = co
		v.loc.cursor_voffset = vo
		v.loc.cursor_line_num++
		v.loc.line_voffset = 0
		v.adjust_line_voffset()
		v.adjust_top_line()
	}
}

func (v *view) move_cursor_prev_line() {
	line := v.loc.cursor_line
	if line.prev != nil {
		bo, co, vo := line.prev.find_closest_offsets(v.loc.last_cursor_voffset)
		v.loc.cursor_line = line.prev
		v.loc.cursor_boffset = bo
		v.loc.cursor_coffset = co
		v.loc.cursor_voffset = vo
		v.loc.cursor_line_num--
		v.loc.line_voffset = 0
		v.adjust_line_voffset()
		v.adjust_top_line()
	}
}

func (v *view) move_cursor_beginning_of_line() {
	v.loc.cursor_boffset = 0
	v.loc.cursor_coffset = 0
	v.loc.cursor_voffset = 0
	v.loc.last_cursor_voffset = 0
	v.loc.line_voffset = 0
}

func (v *view) move_cursor_end_of_line() {
	line := v.loc.cursor_line
	cl, vl := line.lengths()
	v.loc.cursor_boffset = len(line.data)
	v.loc.cursor_coffset = cl
	v.loc.cursor_voffset = vl
	v.loc.last_cursor_voffset = vl
	v.loc.line_voffset = 0
	v.adjust_line_voffset()
}

func (v *view) move_view_n_lines(n int) {
	v.move_top_line_n_times(n)
	v.adjust_cursor_line()

	line := v.loc.cursor_line
	bo, co, vo := line.find_closest_offsets(v.loc.last_cursor_voffset)
	v.loc.cursor_boffset = bo
	v.loc.cursor_coffset = co
	v.loc.cursor_voffset = vo
	v.loc.line_voffset = 0
	v.adjust_line_voffset()
}

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

func (v *view) maybe_move_view_n_lines(n int) {
	if v.can_move_top_line_n_times(n) {
		v.move_view_n_lines(n)
	}
}

func (v *view) move_cursor_end_of_file() {
	v.loc.cursor_line = v.buf.last_line
	v.loc.cursor_line_num = v.buf.lines_n
	v.adjust_top_line()
	v.move_cursor_end_of_line()
}

func (v *view) move_cursor_beginning_of_file() {
	v.loc.cursor_line = v.buf.first_line
	v.loc.cursor_line_num = 1
	v.adjust_top_line()
	v.move_cursor_beginning_of_line()
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

// Returns character and visual lengths of the 'line.data', the byte length can
// be found using 'len(l.data)' formula.
func (l *line) lengths() (cl, vl int) {
	data := l.data
	for len(data) > 0 {
		r, rlen := utf8.DecodeRune(data)
		data = data[rlen:]

		cl++
		if r == '\t' {
			vl += tabstop_length - vl%tabstop_length
		} else {
			vl += 1
		}
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

func print_lines(l *line) {
	i := 1
	for ; l != nil; l = l.next {
		fmt.Printf("%3d (%p) (p: %10p, n: %10p, l: %2d, c: %2d):  %s\n",
			i, l, l.prev, l.next, len(l.data), cap(l.data), string(l.data))
		i++
	}
}

func process_alt_ch(ch rune, v *view) {
	switch ch {
	case 'v':
		v.move_view_n_lines(-v.uibuf.Height / 2)
	case '<':
		v.move_cursor_beginning_of_file()
	case '>':
		v.move_cursor_end_of_file()
	}
}

func main() {
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
			case termbox.KeyCtrlF:
				v.move_cursor_forward()
			case termbox.KeyCtrlB:
				v.move_cursor_backward()
			case termbox.KeyCtrlN:
				v.move_cursor_next_line()
			case termbox.KeyCtrlP:
				v.move_cursor_prev_line()
			case termbox.KeyCtrlE:
				v.move_cursor_end_of_line()
			case termbox.KeyCtrlA:
				v.move_cursor_beginning_of_line()
			case termbox.KeyCtrlV:
				v.maybe_move_view_n_lines(v.uibuf.Height / 2)
			}

			if ev.Mod&termbox.ModAlt != 0 {
				process_alt_ch(ev.Ch, v)
			}

			termbox.SetCursor(v.cursor_position())
			v.redraw()
			copy(termbox.CellBuffer(), v.uibuf.Cells)
			termbox.Flush()
		}
	}
}

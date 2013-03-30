package main

import (
	"bytes"
	"fmt"
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"os"
	"strings"
	"unicode/utf8"
)

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
// byte_range
//----------------------------------------------------------------------------

type byte_range struct {
	begin int
	end   int
}

func (r byte_range) includes(offset int) bool {
	return r.begin <= offset && r.end > offset
}

const hl_fg = termbox.ColorCyan
const hl_bg = termbox.ColorBlue

//----------------------------------------------------------------------------
// view tags
//----------------------------------------------------------------------------

type view_tag struct {
	beg_line   int
	beg_offset int
	end_line   int
	end_offset int
	fg         termbox.Attribute
	bg         termbox.Attribute
}

func (t *view_tag) includes(line, offset int) bool {
	if line < t.beg_line || line > t.end_line {
		return false
	}
	if line == t.beg_line && offset < t.beg_offset {
		return false
	}
	if line == t.end_line && offset >= t.end_offset {
		return false
	}
	return true
}

var default_view_tag = view_tag{
	fg: termbox.ColorDefault,
	bg: termbox.ColorDefault,
}

//----------------------------------------------------------------------------
// view context
//----------------------------------------------------------------------------

type view_context struct {
	set_status  func(format string, args ...interface{})
	kill_buffer *[]byte
	buffers     *[]*buffer
}

//----------------------------------------------------------------------------
// default autocompletion type decision function
//----------------------------------------------------------------------------

func default_ac_decide(view *view) ac_func {
	if strings.HasSuffix(view.buf.path, ".go") {
		return gocode_ac
	}
	return local_ac
}

//----------------------------------------------------------------------------
// view
//
// Think of it as a window. It draws contents from a portion of a buffer into
// 'uibuf' and maintains things like cursor position.
//----------------------------------------------------------------------------

type view struct {
	view_location
	ctx              view_context
	tmpbuf           bytes.Buffer // temporary buffer for status bar text
	buf              *buffer      // currently displayed buffer
	uibuf            tulib.Buffer
	dirty            dirty_flag
	oneline          bool
	ac               *autocompl
	last_vcommand    vcommand
	ac_decide        ac_decide_func
	highlight_bytes  []byte
	highlight_ranges []byte_range
	tags             []view_tag
}

func new_view(ctx view_context, buf *buffer) *view {
	v := new(view)
	v.ctx = ctx
	v.uibuf = tulib.NewBuffer(1, 1)
	v.attach(buf)
	v.ac_decide = default_ac_decide
	v.highlight_ranges = make([]byte_range, 0, 10)
	v.tags = make([]view_tag, 0, 10)
	return v
}

func (v *view) activate() {
	v.last_vcommand = vcommand_none
}

func (v *view) deactivate() {
	// on deactivation discard autocompl
	v.ac = nil
}

func (v *view) attach(b *buffer) {
	if v.buf == b {
		return
	}

	v.ac = nil
	if v.buf != nil {
		v.detach()
	}
	v.buf = b
	v.view_location = b.loc
	b.add_view(v)
	v.dirty = dirty_everything
}

func (v *view) detach() {
	v.buf.delete_view(v)
	v.buf = nil
}

func (v *view) init_autocompl() {
	if v.ac_decide == nil {
		return
	}

	ac_func := v.ac_decide(v)
	if ac_func == nil {
		return
	}

	v.ac = new_autocompl(ac_func, v)
	if v.ac != nil && len(v.ac.actual_proposals()) == 1 {
		v.ac.finalize(v)
		v.ac = nil
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

func (v *view) draw_line(line *line, line_num, coff, line_voffset int) {
	x := 0
	tabstop := 0
	bx := 0
	data := line.data

	if len(v.highlight_bytes) > 0 {
		v.find_highlight_ranges_for_line(data)
	}
	for {
		rx := x - line_voffset
		if len(data) == 0 {
			break
		}

		if x == tabstop {
			tabstop += tabstop_length
		}

		if rx >= v.uibuf.Width {
			last := coff + v.uibuf.Width - 1
			v.uibuf.Cells[last] = termbox.Cell{
				Ch: '→',
				Fg: termbox.ColorDefault,
				Bg: termbox.ColorDefault,
			}
			break
		}

		r, rlen := utf8.DecodeRune(data)
		switch {
		case r == '\t':
			// fill with spaces to the next tabstop
			for ; x < tabstop; x++ {
				rx := x - line_voffset
				if rx >= v.uibuf.Width {
					break
				}

				if rx >= 0 {
					v.uibuf.Cells[coff+rx] = v.make_cell(
						line_num, bx, ' ')
				}
			}
		case r < 32:
			// invisible chars like ^R or ^@
			if rx >= 0 {
				v.uibuf.Cells[coff+rx] = termbox.Cell{
					Ch: '^',
					Fg: termbox.ColorRed,
					Bg: termbox.ColorDefault,
				}
			}
			x++
			rx = x - line_voffset
			if rx >= v.uibuf.Width {
				break
			}
			if rx >= 0 {
				v.uibuf.Cells[coff+rx] = termbox.Cell{
					Ch: invisible_rune_table[r],
					Fg: termbox.ColorRed,
					Bg: termbox.ColorDefault,
				}
			}
			x++
		default:
			if rx >= 0 {
				v.uibuf.Cells[coff+rx] = v.make_cell(
					line_num, bx, r)
			}
			x++
		}
		data = data[rlen:]
		bx += rlen
	}

	if line_voffset != 0 {
		v.uibuf.Cells[coff] = termbox.Cell{
			Ch: '←',
			Fg: termbox.ColorDefault,
			Bg: termbox.ColorDefault,
		}
	}
}

func (v *view) draw_contents() {
	if len(v.highlight_bytes) == 0 {
		v.highlight_ranges = v.highlight_ranges[:0]
	}

	// clear the buffer
	v.uibuf.Fill(v.uibuf.Rect, termbox.Cell{
		Ch: ' ',
		Fg: termbox.ColorDefault,
		Bg: termbox.ColorDefault,
	})

	if v.uibuf.Width == 0 || v.uibuf.Height == 0 {
		return
	}

	// draw lines
	line := v.top_line
	coff := 0
	for y, h := 0, v.height(); y < h; y++ {
		if line == nil {
			break
		}

		if line == v.cursor.line {
			// special case, cursor line
			v.draw_line(line, v.top_line_num+y, coff, v.line_voffset)
		} else {
			v.draw_line(line, v.top_line_num+y, coff, 0)
		}

		coff += v.uibuf.Width
		line = line.next
	}
}

func (v *view) draw_status() {
	if v.oneline {
		return
	}

	// fill background with '─'
	lp := tulib.DefaultLabelParams
	lp.Bg = termbox.AttrReverse
	lp.Fg = termbox.AttrReverse | termbox.AttrBold
	v.uibuf.Fill(tulib.Rect{0, v.height(), v.uibuf.Width, 1}, termbox.Cell{
		Fg: termbox.AttrReverse,
		Bg: termbox.AttrReverse,
		Ch: '─',
	})

	// on disk sync status
	if !v.buf.synced_with_disk() {
		cell := termbox.Cell{
			Fg: termbox.AttrReverse,
			Bg: termbox.AttrReverse,
			Ch: '*',
		}
		v.uibuf.Set(1, v.height(), cell)
		v.uibuf.Set(2, v.height(), cell)
	}

	// filename
	fmt.Fprintf(&v.tmpbuf, "  %s  ", v.buf.name)
	v.uibuf.DrawLabel(tulib.Rect{5, v.height(), v.uibuf.Width, 1},
		&lp, v.tmpbuf.Bytes())
	namel := v.tmpbuf.Len()
	lp.Fg = termbox.AttrReverse
	v.tmpbuf.Reset()
	fmt.Fprintf(&v.tmpbuf, "(%d, %d)  ", v.cursor.line_num, v.cursor_voffset)
	v.uibuf.DrawLabel(tulib.Rect{5 + namel, v.height(), v.uibuf.Width, 1},
		&lp, v.tmpbuf.Bytes())
	v.tmpbuf.Reset()
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

// Center view on the cursor.
func (v *view) center_view_on_cursor() {
	v.top_line = v.cursor.line
	v.top_line_num = v.cursor.line_num
	v.move_top_line_n_times(-v.height() / 2)
	v.dirty = dirty_everything
}

func (v *view) move_cursor_to_line(n int) {
	v.move_cursor_beginning_of_file()
	v.move_cursor_line_n_times(n - 1)
	v.center_view_on_cursor()
}

// Move top line 'n' times forward or backward.
func (v *view) move_top_line_n_times(n int) {
	if n == 0 {
		return
	}

	top := v.top_line
	for top.prev != nil && n < 0 {
		top = top.prev
		v.top_line_num--
		n++
	}
	for top.next != nil && n > 0 {
		top = top.next
		v.top_line_num++
		n--
	}
	v.top_line = top
}

// Move cursor line 'n' times forward or backward.
func (v *view) move_cursor_line_n_times(n int) {
	if n == 0 {
		return
	}

	cursor := v.cursor.line
	for cursor.prev != nil && n < 0 {
		cursor = cursor.prev
		v.cursor.line_num--
		n++
	}
	for cursor.next != nil && n > 0 {
		cursor = cursor.next
		v.cursor.line_num++
		n--
	}
	v.cursor.line = cursor
}

// When 'top_line' was changed, call this function to possibly adjust the
// 'cursor_line'.
func (v *view) adjust_cursor_line() {
	vt := v.vertical_threshold()
	cursor := v.cursor.line
	co := v.cursor.line_num - v.top_line_num
	h := v.height()

	if cursor.next != nil && co < vt {
		v.move_cursor_line_n_times(vt - co)
	}

	if cursor.prev != nil && co >= h-vt {
		v.move_cursor_line_n_times((h - vt) - co - 1)
	}

	if cursor != v.cursor.line {
		cursor = v.cursor.line
		bo, co, vo := cursor.find_closest_offsets(v.last_cursor_voffset)
		v.cursor.boffset = bo
		v.cursor_coffset = co
		v.cursor_voffset = vo
		v.line_voffset = 0
		v.adjust_line_voffset()
		v.dirty = dirty_everything
	}
}

// When 'cursor_line' was changed, call this function to possibly adjust the
// 'top_line'.
func (v *view) adjust_top_line() {
	vt := v.vertical_threshold()
	top := v.top_line
	co := v.cursor.line_num - v.top_line_num
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
	vo := v.line_voffset
	cvo := v.cursor_voffset
	threshold := w - 1
	if vo != 0 {
		threshold = w - ht
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

	if v.line_voffset != vo {
		v.line_voffset = vo
		v.dirty = dirty_everything
	}
}

func (v *view) cursor_position() (int, int) {
	y := v.cursor.line_num - v.top_line_num
	x := v.cursor_voffset - v.line_voffset
	return x, y
}

func (v *view) cursor_position_for(cursor cursor_location) (int, int) {
	y := cursor.line_num - v.top_line_num
	x := cursor.voffset() - v.line_voffset
	return x, y
}

// Move cursor to the 'boffset' position in the 'line'. Obviously 'line' must be
// from the attached buffer. If 'boffset' < 0, use 'last_cursor_voffset'. Keep
// in mind that there is no need to maintain connections between lines (e.g. for
// moving from a deleted line to another line).
func (v *view) move_cursor_to(c cursor_location) {
	v.dirty |= dirty_status
	if c.boffset < 0 {
		bo, co, vo := c.line.find_closest_offsets(v.last_cursor_voffset)
		v.cursor.boffset = bo
		v.cursor_coffset = co
		v.cursor_voffset = vo
	} else {
		vo, co := c.voffset_coffset()
		v.cursor.boffset = c.boffset
		v.cursor_coffset = co
		v.cursor_voffset = vo
	}

	if c.boffset >= 0 {
		v.last_cursor_voffset = v.cursor_voffset
	}

	if c.line != v.cursor.line {
		if v.line_voffset != 0 {
			v.dirty = dirty_everything
		}
		v.line_voffset = 0
	}
	v.cursor.line = c.line
	v.cursor.line_num = c.line_num
	v.adjust_line_voffset()
	v.adjust_top_line()

	if v.ac != nil {
		// update autocompletion on every cursor move
		ok := v.ac.update(v.cursor)
		if !ok {
			v.ac = nil
		}
	}
}

// Move cursor one character forward.
func (v *view) move_cursor_forward() {
	c := v.cursor
	if c.last_line() && c.eol() {
		v.ctx.set_status("End of buffer")
		return
	}

	c.move_one_rune_forward()
	v.move_cursor_to(c)
}

// Move cursor one character backward.
func (v *view) move_cursor_backward() {
	c := v.cursor
	if c.first_line() && c.bol() {
		v.ctx.set_status("Beginning of buffer")
		return
	}

	c.move_one_rune_backward()
	v.move_cursor_to(c)
}

// Move cursor to the next line.
func (v *view) move_cursor_next_line() {
	c := v.cursor
	if !c.last_line() {
		c = cursor_location{c.line.next, c.line_num + 1, -1}
		v.move_cursor_to(c)
	} else {
		v.ctx.set_status("End of buffer")
	}
}

// Move cursor to the previous line.
func (v *view) move_cursor_prev_line() {
	c := v.cursor
	if !c.first_line() {
		c = cursor_location{c.line.prev, c.line_num - 1, -1}
		v.move_cursor_to(c)
	} else {
		v.ctx.set_status("Beginning of buffer")
	}
}

// Move cursor to the beginning of the line.
func (v *view) move_cursor_beginning_of_line() {
	c := v.cursor
	c.move_beginning_of_line()
	v.move_cursor_to(c)
}

// Move cursor to the end of the line.
func (v *view) move_cursor_end_of_line() {
	c := v.cursor
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
	c := v.cursor
	ok := c.move_one_word_forward()
	v.move_cursor_to(c)
	if !ok {
		v.ctx.set_status("End of buffer")
	}
}

func (v *view) move_cursor_word_backward() {
	c := v.cursor
	ok := c.move_one_word_backward()
	v.move_cursor_to(c)
	if !ok {
		v.ctx.set_status("Beginning of buffer")
	}
}

// Move view 'n' lines forward or backward.
func (v *view) move_view_n_lines(n int) {
	prevtop := v.top_line_num
	v.move_top_line_n_times(n)
	if prevtop != v.top_line_num {
		v.adjust_cursor_line()
		v.dirty = dirty_everything
	}
}

// Check if it's possible to move view 'n' lines forward or backward.
func (v *view) can_move_top_line_n_times(n int) bool {
	if n == 0 {
		return true
	}

	top := v.top_line
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
	b.history.before = v.cursor
}

func (v *view) finalize_action_group() {
	b := v.buf
	// finalize only if we're at the tip of the undo history, this function
	// will be called mainly after each cursor movement and actions alike
	// (that are supposed to finalize action group)
	if b.history.next == nil {
		b.history.next = new(action_group)
		b.history.after = v.cursor
	}
}

func (v *view) undo() {
	b := v.buf
	if b.history.prev == nil {
		// we're at the sentinel, no more things to undo
		v.ctx.set_status("No further undo information")
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
	v.move_cursor_to(b.history.before)
	v.last_cursor_voffset = v.cursor_voffset
	b.history = b.history.prev
	v.ctx.set_status("Undo!")
}

func (v *view) redo() {
	b := v.buf
	if b.history.next == nil {
		// open group, obviously, can't move forward
		v.ctx.set_status("No further redo information")
		return
	}
	if len(b.history.next.actions) == 0 {
		// last finalized group, moving to the next group breaks the
		// invariant and doesn't make sense (nothing to redo)
		v.ctx.set_status("No further redo information")
		return
	}

	// move one entry forward, and redo all its actions
	b.history = b.history.next
	for i := range b.history.actions {
		a := &b.history.actions[i]
		a.apply(v)
	}
	v.move_cursor_to(b.history.after)
	v.last_cursor_voffset = v.cursor_voffset
	v.ctx.set_status("Redo!")
}

func (v *view) action_insert(c cursor_location, data []byte) {
	if v.oneline {
		data = bytes.Replace(data, []byte{'\n'}, nil, -1)
	}

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
	l := utf8.EncodeRune(data[:], r)
	c := v.cursor
	if r == '\n' || r == '\r' {
		v.action_insert(c, []byte{'\n'})
		prev := c.line
		c.line = c.line.next
		c.line_num++
		c.boffset = 0

		if r == '\n' {
			i := index_first_non_space(prev.data)
			if i > 0 {
				autoindent := clone_byte_slice(prev.data[:i])
				v.action_insert(c, autoindent)
				c.boffset += len(autoindent)
			}
		}
	} else {
		v.action_insert(c, data[:l])
		c.boffset += l
	}
	v.move_cursor_to(c)
	v.dirty = dirty_everything
}

// If at the beginning of the line, move contents of the current line to the end
// of the previous line. Otherwise, erase one character backward.
func (v *view) delete_rune_backward() {
	c := v.cursor
	if c.bol() {
		if c.first_line() {
			// beginning of the file
			v.ctx.set_status("Beginning of buffer")
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
	c := v.cursor
	if c.eol() {
		if c.last_line() {
			// end of the file
			v.ctx.set_status("End of buffer")
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
	c := v.cursor
	if !c.eol() {
		// kill data from the cursor to the EOL
		len := len(c.line.data) - c.boffset
		v.append_to_kill_buffer(c, len)
		v.action_delete(c, len)
		v.dirty = dirty_everything
		return
	}
	v.append_to_kill_buffer(c, 1)
	v.delete_rune()
}

func (v *view) kill_word() {
	c1 := v.cursor
	c2 := c1
	c2.move_one_word_forward()
	d := c1.distance(c2)
	if d > 0 {
		v.append_to_kill_buffer(c1, d)
		v.action_delete(c1, d)
	}
}

func (v *view) kill_word_backward() {
	c2 := v.cursor
	c1 := c2
	c1.move_one_word_backward()
	d := c1.distance(c2)
	if d > 0 {
		v.prepend_to_kill_buffer(c1, d)
		v.action_delete(c1, d)
		v.move_cursor_to(c1)
	}
}

func (v *view) kill_region() {
	if !v.buf.is_mark_set() {
		v.ctx.set_status("The mark is not set now, so there is no region")
		return
	}

	c1 := v.cursor
	c2 := v.buf.mark
	d := c1.distance(c2)
	switch {
	case d == 0:
		return
	case d < 0:
		d = -d
		v.append_to_kill_buffer(c2, d)
		v.action_delete(c2, d)
		v.move_cursor_to(c2)
	default:
		v.append_to_kill_buffer(c1, d)
		v.action_delete(c1, d)
	}
}

func (v *view) set_mark() {
	v.buf.mark = v.cursor
	v.ctx.set_status("Mark set")
}

func (v *view) swap_cursor_and_mark() {
	if v.buf.is_mark_set() {
		m := v.buf.mark
		v.buf.mark = v.cursor
		v.move_cursor_to(m)
	}
}

func (v *view) on_insert_adjust_top_line(a *action) {
	if a.cursor.line_num < v.top_line_num && len(a.lines) > 0 {
		// inserted one or more lines above the view
		v.top_line_num += len(a.lines)
		v.dirty |= dirty_status
	}
}

func (v *view) on_delete_adjust_top_line(a *action) {
	if a.cursor.line_num < v.top_line_num {
		// deletion above the top line
		if len(a.lines) == 0 {
			return
		}

		topnum := v.top_line_num
		first, last := a.deleted_lines()
		if first <= topnum && topnum <= last {
			// deleted the top line, adjust the pointers
			if a.cursor.line.next != nil {
				v.top_line = a.cursor.line.next
				v.top_line_num = a.cursor.line_num + 1
			} else {
				v.top_line = a.cursor.line
				v.top_line_num = a.cursor.line_num
			}
			v.dirty = dirty_everything
		} else {
			// no need to worry
			v.top_line_num -= len(a.lines)
			v.dirty |= dirty_status
		}
	}
}

func (v *view) on_insert(a *action) {
	v.on_insert_adjust_top_line(a)
	if v.top_line_num+v.height() <= a.cursor.line_num {
		// inserted something below the view, don't care
		return
	}
	if a.cursor.line_num < v.top_line_num {
		// inserted something above the top line
		if len(a.lines) > 0 {
			// inserted one or more lines, adjust line numbers
			v.cursor.line_num += len(a.lines)
			v.dirty |= dirty_status
		}
		return
	}
	c := v.cursor
	c.on_insert_adjust(a)
	v.move_cursor_to(c)
	v.last_cursor_voffset = v.cursor_voffset
	v.dirty = dirty_everything
}

func (v *view) on_delete(a *action) {
	v.on_delete_adjust_top_line(a)
	if v.top_line_num+v.height() <= a.cursor.line_num {
		// deleted something below the view, don't care
		return
	}
	if a.cursor.line_num < v.top_line_num {
		// deletion above the top line
		if len(a.lines) == 0 {
			return
		}

		_, last := a.deleted_lines()
		if last < v.top_line_num {
			// no need to worry
			v.cursor.line_num -= len(a.lines)
			v.dirty |= dirty_status
			return
		}
	}
	c := v.cursor
	c.on_delete_adjust(a)
	v.move_cursor_to(c)
	v.last_cursor_voffset = v.cursor_voffset
	v.dirty = dirty_everything
}

func (v *view) on_vcommand(cmd vcommand, arg rune) {
	last_class := v.last_vcommand.class()
	if cmd.class() != last_class || last_class == vcommand_class_misc {
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
	case vcommand_move_cursor_to_line:
		v.move_cursor_to_line(int(arg))
	case vcommand_move_view_half_forward:
		v.maybe_move_view_n_lines(v.height() / 2)
	case vcommand_move_view_half_backward:
		v.move_view_n_lines(-v.height() / 2)
	case vcommand_set_mark:
		v.set_mark()
	case vcommand_swap_cursor_and_mark:
		v.swap_cursor_and_mark()
	case vcommand_insert_rune:
		v.insert_rune(arg)
	case vcommand_yank:
		v.yank()
	case vcommand_delete_rune_backward:
		v.delete_rune_backward()
	case vcommand_delete_rune:
		v.delete_rune()
	case vcommand_kill_line:
		v.kill_line()
	case vcommand_kill_word:
		v.kill_word()
	case vcommand_kill_word_backward:
		v.kill_word_backward()
	case vcommand_kill_region:
		v.kill_region()
	case vcommand_copy_region:
		v.copy_region()
	case vcommand_undo:
		v.undo()
	case vcommand_redo:
		v.redo()
	case vcommand_autocompl_init:
		v.init_autocompl()
	case vcommand_autocompl_finalize:
		v.ac.finalize(v)
		v.ac = nil
	case vcommand_autocompl_move_cursor_up:
		v.ac.move_cursor_up()
	case vcommand_autocompl_move_cursor_down:
		v.ac.move_cursor_down()
	case vcommand_indent_region:
		v.indent_region()
	case vcommand_deindent_region:
		v.deindent_region()
	case vcommand_region_to_upper:
		v.region_to(bytes.ToUpper)
	case vcommand_region_to_lower:
		v.region_to(bytes.ToLower)
	case vcommand_word_to_upper:
		v.word_to(bytes.ToUpper)
	case vcommand_word_to_title:
		v.word_to(func(s []byte) []byte {
			return bytes.Title(bytes.ToLower(s))
		})
	case vcommand_word_to_lower:
		v.word_to(bytes.ToLower)
	}

	v.last_vcommand = cmd
}

func (v *view) on_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlF, termbox.KeyArrowRight:
		v.on_vcommand(vcommand_move_cursor_forward, 0)
	case termbox.KeyCtrlB, termbox.KeyArrowLeft:
		v.on_vcommand(vcommand_move_cursor_backward, 0)
	case termbox.KeyCtrlN, termbox.KeyArrowDown:
		if v.ac != nil {
			v.on_vcommand(vcommand_autocompl_move_cursor_down, 0)
			break
		}
		v.on_vcommand(vcommand_move_cursor_next_line, 0)
	case termbox.KeyCtrlP, termbox.KeyArrowUp:
		if v.ac != nil {
			v.on_vcommand(vcommand_autocompl_move_cursor_up, 0)
			break
		}
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
		c := '\n'
		if ev.Key == termbox.KeyEnter {
			// we use '\r' for <enter>, because it doesn't cause
			// autoindent
			c = '\r'
		}
		if v.ac != nil {
			v.on_vcommand(vcommand_autocompl_finalize, 0)
		} else {
			v.on_vcommand(vcommand_insert_rune, c)
		}
	case termbox.KeyBackspace, termbox.KeyBackspace2:
		if ev.Mod&termbox.ModAlt != 0 {
			v.on_vcommand(vcommand_kill_word_backward, 0)
		} else {
			v.on_vcommand(vcommand_delete_rune_backward, 0)
		}
	case termbox.KeyDelete, termbox.KeyCtrlD:
		v.on_vcommand(vcommand_delete_rune, 0)
	case termbox.KeyCtrlK:
		v.on_vcommand(vcommand_kill_line, 0)
	case termbox.KeyPgup:
		v.on_vcommand(vcommand_move_view_half_backward, 0)
	case termbox.KeyTab:
		v.on_vcommand(vcommand_insert_rune, '\t')
	case termbox.KeyCtrlSpace:
		if ev.Ch == 0 {
			v.set_mark()
		}
	case termbox.KeyCtrlW:
		v.on_vcommand(vcommand_kill_region, 0)
	case termbox.KeyCtrlY:
		v.on_vcommand(vcommand_yank, 0)
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
		case 'w':
			v.on_vcommand(vcommand_copy_region, 0)
		case 'u':
			v.on_vcommand(vcommand_word_to_upper, 0)
		case 'l':
			v.on_vcommand(vcommand_word_to_lower, 0)
		case 'c':
			v.on_vcommand(vcommand_word_to_title, 0)
		}
	} else if ev.Ch != 0 {
		v.on_vcommand(vcommand_insert_rune, ev.Ch)
	}
}

func (v *view) dump_info() {
	p := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, format, args...)
	}

	p("Top line num: %d\n", v.top_line_num)
}

func (v *view) find_highlight_ranges_for_line(data []byte) {
	v.highlight_ranges = v.highlight_ranges[:0]
	offset := 0
	for {
		i := bytes.Index(data, v.highlight_bytes)
		if i == -1 {
			return
		}

		v.highlight_ranges = append(v.highlight_ranges, byte_range{
			begin: offset + i,
			end:   offset + i + len(v.highlight_bytes),
		})
		data = data[i+len(v.highlight_bytes):]
		offset += i + len(v.highlight_bytes)
	}
}

func (v *view) in_one_of_highlight_ranges(offset int) bool {
	for _, r := range v.highlight_ranges {
		if r.includes(offset) {
			return true
		}
	}
	return false
}

func (v *view) tag(line, offset int) *view_tag {
	for i := range v.tags {
		t := &v.tags[i]
		if t.includes(line, offset) {
			return t
		}
	}
	return &default_view_tag
}

func (v *view) make_cell(line, offset int, ch rune) termbox.Cell {
	tag := v.tag(line, offset)
	if tag != &default_view_tag {
		return termbox.Cell{
			Ch: ch,
			Fg: tag.fg,
			Bg: tag.bg,
		}
	}

	cell := termbox.Cell{
		Ch: ch,
		Fg: tag.fg,
		Bg: tag.bg,
	}
	if v.in_one_of_highlight_ranges(offset) {
		cell.Fg = hl_fg
		cell.Bg = hl_bg
	}
	return cell
}

func (v *view) cleanup_trailing_whitespaces() {
	cursor := cursor_location{
		line:     v.buf.first_line,
		line_num: 1,
		boffset:  0,
	}

	for cursor.line != nil {
		len := len(cursor.line.data)
		i := index_last_non_space(cursor.line.data)
		if i == -1 && len > 0 {
			// the whole string is whitespace
			v.action_delete(cursor, len)
		}
		if i != -1 && i != len-1 {
			// some whitespace at the end
			cursor.boffset = i + 1
			v.action_delete(cursor, len-cursor.boffset)
		}
		cursor.line = cursor.line.next
		cursor.line_num++
		cursor.boffset = 0
	}

	// adjust cursor after changes possibly
	cursor = v.cursor
	if cursor.boffset > len(cursor.line.data) {
		cursor.boffset = len(cursor.line.data)
		v.move_cursor_to(cursor)
	}
}

func (v *view) cleanup_trailing_newlines() {
	cursor := cursor_location{
		line:     v.buf.last_line,
		line_num: v.buf.lines_n,
		boffset:  0,
	}

	for len(cursor.line.data) == 0 {
		prev := cursor.line.prev
		if prev == nil {
			// beginning of the file, stop
			break
		}

		if len(prev.data) > 0 {
			// previous line is not empty, leave one empty line at
			// the end (trailing EOL)
			break
		}

		// adjust view cursor just in case
		if v.cursor.line_num == cursor.line_num {
			v.move_cursor_prev_line()
		}

		cursor.line = prev
		cursor.line_num--
		cursor.boffset = 0
		v.action_delete(cursor, 1)
	}
}

func (v *view) ensure_trailing_eol() {
	cursor := cursor_location{
		line:     v.buf.last_line,
		line_num: v.buf.lines_n,
		boffset:  len(v.buf.last_line.data),
	}
	if len(v.buf.last_line.data) > 0 {
		v.action_insert(cursor, []byte{'\n'})
	}
}

func (v *view) presave_cleanup(raw bool) {
	v.finalize_action_group()
	v.last_vcommand = vcommand_none
	if !raw {
		v.cleanup_trailing_whitespaces()
		v.cleanup_trailing_newlines()
		v.ensure_trailing_eol()
		v.finalize_action_group()
	}
}

func (v *view) append_to_kill_buffer(cursor cursor_location, nbytes int) {
	kb := *v.ctx.kill_buffer

	switch v.last_vcommand {
	case vcommand_kill_word, vcommand_kill_word_backward, vcommand_kill_region, vcommand_kill_line:
	default:
		kb = kb[:0]
	}

	kb = append(kb, cursor.extract_bytes(nbytes)...)
	*v.ctx.kill_buffer = kb
}

func (v *view) prepend_to_kill_buffer(cursor cursor_location, nbytes int) {
	kb := *v.ctx.kill_buffer

	switch v.last_vcommand {
	case vcommand_kill_word, vcommand_kill_word_backward, vcommand_kill_region, vcommand_kill_line:
	default:
		kb = kb[:0]
	}

	kb = append(cursor.extract_bytes(nbytes), kb...)
	*v.ctx.kill_buffer = kb
}

func (v *view) yank() {
	buf := *v.ctx.kill_buffer
	cursor := v.cursor

	if len(buf) == 0 {
		return
	}
	cbuf := clone_byte_slice(buf)
	v.action_insert(cursor, cbuf)
	for len(buf) > 0 {
		_, rlen := utf8.DecodeRune(buf)
		buf = buf[rlen:]
		cursor.move_one_rune_forward()
	}
	v.move_cursor_to(cursor)
}

// shameless copy & paste from kill_region
func (v *view) copy_region() {
	if !v.buf.is_mark_set() {
		v.ctx.set_status("The mark is not set now, so there is no region")
		return
	}

	c1 := v.cursor
	c2 := v.buf.mark
	d := c1.distance(c2)
	switch {
	case d == 0:
		return
	case d < 0:
		d = -d
		v.append_to_kill_buffer(c2, d)
	default:
		v.append_to_kill_buffer(c1, d)
	}
}

// assumes that filtered text has the same length
func (v *view) region_to(filter func([]byte) []byte) {
	if !v.buf.is_mark_set() {
		v.ctx.set_status("The mark is not set now, so there is no region")
		return
	}
	v.filter_text(v.cursor, v.buf.mark, filter)
}

func (v *view) set_tags(tags ...view_tag) {
	v.tags = v.tags[:0]
	if len(tags) == 0 {
		return
	}
	if cap(v.tags) < cap(tags) {
		v.tags = tags
		return
	}
	v.tags = v.tags[:len(tags)]
	copy(v.tags, tags)
}

func (v *view) line_region() (beg, end cursor_location) {
	beg = v.cursor
	end = v.cursor
	if v.buf.is_mark_set() {
		end = v.buf.mark
	}

	if beg.line_num > end.line_num {
		beg, end = end, beg
	}
	beg.boffset = 0
	end.boffset = len(end.line.data)
	return
}

func (v *view) indent_line(line cursor_location) {
	line.boffset = 0
	v.action_insert(line, []byte{'\t'})
	if v.cursor.line == line.line {
		cursor := v.cursor
		cursor.boffset += 1
		v.move_cursor_to(cursor)
	}
}

func (v *view) deindent_line(line cursor_location) {
	line.boffset = 0
	if r, _ := line.rune_under(); r == '\t' {
		v.action_delete(line, 1)
	}
	if v.cursor.line == line.line && v.cursor.boffset > 0 {
		cursor := v.cursor
		cursor.boffset -= 1
		v.move_cursor_to(cursor)
	}
}

func (v *view) indent_region() {
	beg, end := v.line_region()
	for beg.line != end.line {
		v.indent_line(beg)
		beg.line = beg.line.next
		beg.line_num++
	}
	v.indent_line(end)
}

func (v *view) deindent_region() {
	beg, end := v.line_region()
	for beg.line != end.line {
		v.deindent_line(beg)
		beg.line = beg.line.next
		beg.line_num++
	}
	v.deindent_line(end)
}

func (v *view) word_to(filter func([]byte) []byte) {
	c1, c2 := v.cursor, v.cursor
	c2.move_one_word_forward()
	v.filter_text(c1, c2, filter)
	c1.move_one_word_forward()
	v.move_cursor_to(c1)
}

// Filter _must_ return a new slice and shouldn't touch contents of the
// argument, perfect filter examples are: bytes.Title, bytes.ToUpper,
// bytes.ToLower
func (v *view) filter_text(from, to cursor_location, filter func([]byte) []byte) {
	c1, c2 := swap_cursors_maybe(from, to)
	d := c1.distance(c2)
	v.action_delete(c1, d)
	data := filter(v.buf.history.last_action().data)
	v.action_insert(c1, data)
}

func (v *view) fill_region(maxv int, prefix []byte) {
	var buf, out bytes.Buffer
	beg, end := v.line_region()
	data := beg.extract_bytes(beg.distance(end))
	indent := data[:index_first_non_space(data)]
	indent_vlen := vlen(indent, 0)
	prefix_vlen := vlen(prefix, indent_vlen)
	offset := 0
	for {
		// for each line
		// 1. skip whitespace
		offset += index_first_non_space(data[offset:])
		// 2. skip prefix
		if bytes.HasPrefix(data[offset:], prefix) {
			offset += len(prefix)
		}
		// 3. skip more whitespace
		offset += index_first_non_space(data[offset:])
		// append line to the buffer without \n
		i := bytes.Index(data[offset:], []byte("\n"))
		if i == -1 {
			iter_nonspace_words(data[offset:], func(word []byte) {
				buf.Write(word)
				buf.WriteString(" ")
			})
			break
		} else {
			iter_nonspace_words(data[offset:offset+i], func(word []byte) {
				buf.Write(word)
				buf.WriteString(" ")
			})
			offset += i+1
		}
	}
	// just in case if there were unnecessary space at the end, clean it up
	if buf.Len() > 0 && buf.Bytes()[buf.Len()-1] == ' ' {
		buf.Truncate(buf.Len()-1)
	}

	offset = 0
	for {
		data := buf.Bytes()[offset:]
		out.Write(indent)
		if len(prefix) > 0 {
			out.Write(prefix)
			out.WriteString(" ")
		}

		v := indent_vlen + prefix_vlen + 1
		lastspacei := -1
		i := 0
		for i < len(data) {
			r, rlen := utf8.DecodeRune(data[i:])
			if r == ' ' {
				// if the rune is a space and we still haven't found one
				// or we're still before maxv, update the index of the
				// last space before maxv
				if lastspacei == -1 || v < maxv {
					lastspacei = i
				}
			}

			// advance v and i
			v += rune_advance_len(r, v)
			i += rlen

			if lastspacei != -1 && v >= maxv {
				// we've seen last space and now we're past maxv, break
				break
			}
		}

		if i >= len(data) {
			out.Write(data)
			break
		} else {
			out.Write(data[:lastspacei])
			out.WriteString("\n")
			offset += lastspacei+1
		}
	}

	v.action_delete(beg, len(data))
	v.action_insert(beg, out.Bytes())
	v.move_cursor_to(beg)
}

func (v *view) collect_words(slice [][]byte, dups *llrb_tree, ignorecase bool) [][]byte {
	append_word_full := func(prefix, word []byte, clone bool) {
		lword := word
		lprefix := prefix
		if ignorecase {
			lword = bytes.ToLower(word)
			lprefix = bytes.ToLower(prefix)
		}

		if !bytes.HasPrefix(lword, lprefix) {
			return
		}
		ok := dups.insert_maybe(word)
		if ok {
			if clone {
				slice = append(slice, clone_byte_slice(word))
			} else {
				slice = append(slice, word)
			}
		}
	}

	prefix := v.cursor.word_under_cursor()
	if prefix != nil {
		dups.insert_maybe(prefix)
	}

	append_word := func(word []byte) {
		append_word_full(prefix, word, false)
	}
	append_word_clone := func(word []byte) {
		append_word_full(prefix, word, true)
	}

	line := v.cursor.line
	iter_words_backward(line.data[:v.cursor.boffset], append_word_clone)
	line = line.prev
	for line != nil {
		iter_words_backward(line.data, append_word)
		line = line.prev
	}

	line = v.cursor.line
	iter_words(line.data[v.cursor.boffset:], append_word_clone)
	line = line.next
	for line != nil {
		iter_words(line.data, append_word)
		line = line.next
	}
	return slice
}

func (v *view) search_and_replace(word, repl []byte) {
	// assumes mark is set
	c1, c2 := swap_cursors_maybe(v.cursor, v.buf.mark)
	cur := cursor_location{
		line:     c1.line,
		line_num: c1.line_num,
		boffset:  c1.boffset,
	}
	for {
		var end int
		if cur.line == c2.line {
			end = c2.boffset
		} else {
			end = len(cur.line.data)
		}

		i := bytes.Index(cur.line.data[cur.boffset:end], word)
		if i != -1 {
			// match on this line, replace it
			cur.boffset += i
			v.action_delete(cur, len(word))

			// It is safe to use the original 'repl' here, but be
			// very careful with that, it may change. 'repl' comes
			// from 'godit.s_and_r_last_repl', if someone decides
			// to make it mutable, then 'repl' must be copied
			// somewhere in this func.
			v.action_insert(cur, repl)

			// special correction if we're on the same line as 'c2'
			if cur.line == c2.line {
				c2.boffset += len(repl) - len(word)
			}

			if cur.line == v.cursor.line && cur.boffset < v.cursor.boffset {
				c := v.cursor
				c.boffset += len(repl) - len(word)
				v.move_cursor_to(c)
			}

			// continue with the same line
			cur.boffset += len(repl)
			continue
		}

		// nothing on this line found, terminate or continue to the next line
		if cur.line == c2.line {
			break
		}

		cur.line = cur.line.next
		cur.line_num++
		cur.boffset = 0
	}

	v.ctx.set_status("Replaced %s with %s", word, repl)
}

func (v *view) other_buffers(cb func(buf *buffer)) {
	bufs := *v.ctx.buffers
	for _, buf := range bufs {
		if buf == v.buf {
			continue
		}
		cb(buf)
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
	vcommand_class_misc
)

type vcommand int

const (
	vcommand_none vcommand = iota

	// movement commands (finalize undo action group)
	_vcommand_movement_beg
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
	vcommand_move_cursor_to_line
	vcommand_move_view_half_forward
	vcommand_move_view_half_backward
	vcommand_set_mark
	vcommand_swap_cursor_and_mark
	_vcommand_movement_end

	// insertion commands
	_vcommand_insertion_beg
	vcommand_insert_rune
	vcommand_yank
	_vcommand_insertion_end

	// deletion commands
	_vcommand_deletion_beg
	vcommand_delete_rune_backward
	vcommand_delete_rune
	vcommand_kill_line
	vcommand_kill_word
	vcommand_kill_word_backward
	vcommand_kill_region
	_vcommand_deletion_end

	// history commands (undo/redo)
	_vcommand_history_beg
	vcommand_undo
	vcommand_redo
	_vcommand_history_end

	// misc commands
	_vcommand_misc_beg
	vcommand_indent_region
	vcommand_deindent_region
	vcommand_copy_region
	vcommand_region_to_upper
	vcommand_region_to_lower
	vcommand_word_to_upper
	vcommand_word_to_title
	vcommand_word_to_lower
	vcommand_autocompl_init
	vcommand_autocompl_move_cursor_up
	vcommand_autocompl_move_cursor_down
	vcommand_autocompl_finalize
	_vcommand_misc_end
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
	case c > _vcommand_misc_beg && c < _vcommand_misc_end:
		return vcommand_class_misc
	}
	return vcommand_class_none
}

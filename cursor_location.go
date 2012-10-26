package main

import (
	"bytes"
	"unicode/utf8"
)

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
	s := 1
	if b.line_num < a.line_num {
		a, b = b, a
		s = -1
	} else if a.line_num == b.line_num && b.boffset < a.boffset {
		a, b = b, a
		s = -1
	}

	n := 0
	for a.line != b.line {
		n += len(a.line.data) - a.boffset + 1
		a.line = a.line.next
		a.boffset = 0
	}
	n += b.boffset - a.boffset
	return n * s
}

// Find a visual and a character offset for a given cursor
func (c *cursor_location) voffset_coffset() (vo, co int) {
	data := c.line.data[:c.boffset]
	for len(data) > 0 {
		r, rlen := utf8.DecodeRune(data)
		data = data[rlen:]
		co += 1
		vo += rune_advance_len(r, vo)
	}
	return
}

// Find a visual offset for a given cursor
func (c *cursor_location) voffset() (vo int) {
	data := c.line.data[:c.boffset]
	for len(data) > 0 {
		r, rlen := utf8.DecodeRune(data)
		data = data[rlen:]
		vo += rune_advance_len(r, vo)
	}
	return
}

func (c *cursor_location) coffset() (co int) {
	data := c.line.data[:c.boffset]
	for len(data) > 0 {
		_, rlen := utf8.DecodeRune(data)
		data = data[rlen:]
		co += 1
	}
	return
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
			buf.Write(line.data[offset : offset+nb])
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

func (c *cursor_location) word_under_cursor() []byte {
	end, beg := *c, *c
	r, rlen := beg.rune_before()
	if r == utf8.RuneError {
		return nil
	}

	for is_word(r) && !beg.bol() {
		beg.boffset -= rlen
		r, rlen = beg.rune_before()
	}

	if beg.boffset == end.boffset {
		return nil
	}
	return c.line.data[beg.boffset:end.boffset]
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

func (c *cursor_location) on_insert_adjust(a *action) {
	if a.cursor.line_num > c.line_num {
		return
	}
	if a.cursor.line_num < c.line_num {
		// inserted something above the cursor, adjust it
		c.line_num += len(a.lines)
		return
	}

	// insertion on the cursor line
	if a.cursor.boffset < c.boffset {
		// insertion before the cursor, move cursor along with insertion
		if len(a.lines) == 0 {
			// no lines were inserted, simply adjust the offset
			c.boffset += len(a.data)
		} else {
			// one or more lines were inserted, adjust cursor
			// respectively
			c.line = a.last_line()
			c.line_num += len(a.lines)
			c.boffset = a.last_line_affection_len() +
				c.boffset - a.cursor.boffset
		}
	}
}

func (c *cursor_location) on_delete_adjust(a *action) {
	if a.cursor.line_num > c.line_num {
		return
	}
	if a.cursor.line_num < c.line_num {
		// deletion above the cursor line, may touch the cursor location
		if len(a.lines) == 0 {
			// no lines were deleted, no things to adjust
			return
		}

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
			*c = a.cursor
			c.boffset += n
		} else {
			// phew.. no worries
			c.line_num -= len(a.lines)
			return
		}
	}

	// the last case is deletion on the cursor line, see what was deleted
	if a.cursor.boffset >= c.boffset {
		// deleted something after cursor, don't care
		return
	}

	n := c.boffset - (a.cursor.boffset + a.first_line_affection_len())
	if n < 0 {
		n = 0
	}
	c.boffset = a.cursor.boffset + n
}

func (c cursor_location) search_forward(word []byte) (cursor_location, bool) {
	for c.line != nil {
		i := bytes.Index(c.line.data[c.boffset:], word)
		if i != -1 {
			c.boffset += i
			return c, true
		}

		c.line = c.line.next
		c.line_num++
		c.boffset = 0
	}
	return c, false
}

func (c cursor_location) search_backward(word []byte) (cursor_location, bool) {
	for {
		i := bytes.LastIndex(c.line.data[:c.boffset], word)
		if i != -1 {
			c.boffset = i
			return c, true
		}

		c.line = c.line.prev
		if c.line == nil {
			break
		}
		c.line_num--
		c.boffset = len(c.line.data)
	}
	return c, false
}

func swap_cursors_maybe(c1, c2 cursor_location) (r1, r2 cursor_location) {
	if c1.line_num == c2.line_num {
		if c1.boffset > c2.boffset {
			return c2, c1
		} else {
			return c1, c2
		}
	}

	if c1.line_num > c2.line_num {
		return c2, c1
	}
	return c1, c2
}

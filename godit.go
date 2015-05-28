package main

import (
	"bytes"
	"fmt"
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const (
	tabstop_length            = 8
	view_vertical_threshold   = 5
	view_horizontal_threshold = 10
)

// this is a structure which represents a key press, used for keyboard macros
type key_event struct {
	mod termbox.Modifier
	_   [1]byte
	key termbox.Key
	ch  rune
}

func create_key_event(ev *termbox.Event) key_event {
	return key_event{
		mod: ev.Mod,
		key: ev.Key,
		ch:  ev.Ch,
	}
}

func (k key_event) to_termbox_event() termbox.Event {
	return termbox.Event{
		Type: termbox.EventKey,
		Mod:  k.mod,
		Key:  k.key,
		Ch:   k.ch,
	}
}

//----------------------------------------------------------------------------
// godit
//
// Main top-level structure, that handles views composition, status bar and
// input messaging. Also it's the spot where keyboard macros are implemented.
//----------------------------------------------------------------------------

type godit struct {
	uibuf             tulib.Buffer
	active            *view_tree // this one is always a leaf node
	views             *view_tree // a root node
	buffers           []*buffer
	lastcmdclass      vcommand_class
	statusbuf         bytes.Buffer
	quitflag          bool
	overlay           overlay_mode
	termbox_event     chan termbox.Event
	keymacros         []key_event
	recording         bool
	killbuffer        []byte
	isearch_last_word []byte
	s_and_r_last_word []byte
	s_and_r_last_repl []byte
}

func new_godit(filenames []string) *godit {
	g := new(godit)
	g.buffers = make([]*buffer, 0, 20)
	for _, filename := range filenames {
		g.new_buffer_from_file(filename)
	}
	if len(g.buffers) == 0 {
		buf := new_empty_buffer()
		buf.name = g.buffer_name("unnamed")
		g.buffers = append(g.buffers, buf)
	}
	g.views = new_view_tree_leaf(nil, new_view(g.view_context(), g.buffers[0]))
	g.active = g.views
	g.keymacros = make([]key_event, 0, 50)
	g.isearch_last_word = make([]byte, 0, 32)
	return g
}

func (g *godit) kill_buffer(buf *buffer) {
	var replacement *buffer
	views := make([]*view, len(buf.views))
	copy(views, buf.views)

	// find replacement buffer
	if len(views) > 0 {
		for _, gbuf := range g.buffers {
			if gbuf == buf {
				continue
			}
			replacement = gbuf
			break
		}
		if replacement == nil {
			replacement = new_empty_buffer()
			replacement.name = g.buffer_name("unnamed")
			g.buffers = append(g.buffers, replacement)
		}
	}

	// replace the buffer we're killing with replacement one for
	// all the views
	for _, v := range views {
		v.attach(replacement)
	}

	// remove buffer from the list
	bi := -1
	for i, n := 0, len(g.buffers); i < n; i++ {
		if g.buffers[i] == buf {
			bi = i
			break
		}
	}

	if bi == -1 {
		panic("removing non-existent buffer")
	}

	copy(g.buffers[bi:], g.buffers[bi+1:])
	g.buffers = g.buffers[:len(g.buffers)-1]
}

func (g *godit) find_buffer_by_full_path(path string) *buffer {
	for _, buf := range g.buffers {
		if buf.path == path {
			return buf
		}
	}
	return nil
}

func (g *godit) open_buffers_from_pattern(pattern string) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		panic(err)
	}

	var buf *buffer
	for _, match := range matches {
		buf, _ = g.new_buffer_from_file(match)
	}
	if buf == nil {
		buf, _ = g.new_buffer_from_file(pattern)
	}
	if buf == nil {
		buf = new_empty_buffer()
		buf.name = g.buffer_name("unnamed")
	}
	g.active.leaf.attach(buf)
}

func (g *godit) buffer_name_exists(name string) bool {
	for _, buf := range g.buffers {
		if buf.name == name {
			return true
		}
	}
	return false
}

func (g *godit) buffer_name(name string) string {
	if !g.buffer_name_exists(name) {
		return name
	}

	for i := 2; i < 9999; i++ {
		candidate := name + " <" + strconv.Itoa(i) + ">"
		if !g.buffer_name_exists(candidate) {
			return candidate
		}
	}
	panic("too many buffers opened with the same name")
}

func (g *godit) new_buffer_from_file(filename string) (*buffer, error) {
	fullpath := abs_path(filename)
	buf := g.find_buffer_by_full_path(fullpath)
	if buf != nil {
		return buf, nil
	}

	_, err := os.Stat(fullpath)
	if err != nil {
		// assume the file is just not there
		g.set_status("(New file)")
		buf = new_empty_buffer()
	} else {
		f, err := os.Open(fullpath)
		if err != nil {
			g.set_status(err.Error())
			return nil, err
		}
		defer f.Close()
		buf, err = new_buffer(f)
		if err != nil {
			g.set_status(err.Error())
			return nil, err
		}
		buf.path = fullpath
	}

	buf.name = g.buffer_name(filename)
	g.buffers = append(g.buffers, buf)
	return buf, nil
}

func (g *godit) set_status(format string, args ...interface{}) {
	g.statusbuf.Reset()
	fmt.Fprintf(&g.statusbuf, format, args...)
}

func (g *godit) split_horizontally() {
	if g.active.Width == 0 {
		return
	}
	g.active.split_horizontally()
	g.active = g.active.left
	g.resize()
}

func (g *godit) split_vertically() {
	if g.active.Height == 0 {
		return
	}
	g.active.split_vertically()
	g.active = g.active.top
	g.resize()
}

func (g *godit) kill_active_view() {
	p := g.active.parent
	if p == nil {
		return
	}

	pp := p.parent
	sib := g.active.sibling()
	g.active.leaf.deactivate()
	g.active.leaf.detach()

	*p = *sib
	p.parent = pp
	p.reparent()

	g.active = p.first_leaf_node()
	g.active.leaf.activate()
	g.resize()
}

func (g *godit) kill_all_views_but_active() {
	g.views.traverse(func(v *view_tree) {
		if v == g.active {
			return
		}
		if v.leaf != nil {
			v.leaf.detach()
		}
	})
	g.views = g.active
	g.views.parent = nil
	g.resize()
}

// Call it manually only when views layout has changed.
func (g *godit) resize() {
	g.uibuf = tulib.TermboxBuffer()
	views_area := g.uibuf.Rect
	views_area.Height -= 1 // reserve space for command line
	g.views.resize(views_area)
}

func (g *godit) draw_autocompl() {
	view := g.active.leaf
	x, y := g.active.X, g.active.Y
	if view.ac == nil {
		return
	}

	proposals := view.ac.actual_proposals()
	if len(proposals) > 0 {
		cx, cy := view.cursor_position_for(view.ac.origin)
		view.ac.draw_onto(&g.uibuf, x+cx, y+cy)
	}
}

func (g *godit) draw() {
	var overlay_needs_cursor bool
	if g.overlay != nil {
		overlay_needs_cursor = g.overlay.needs_cursor()
	}

	// draw everything
	g.views.draw()
	g.composite_recursively(g.views)
	g.draw_status()

	// draw overlay if any
	if g.overlay != nil {
		g.overlay.draw()
	}

	// draw autocompletion
	if !overlay_needs_cursor {
		g.draw_autocompl()
	}

	// update cursor position
	var cx, cy int
	if overlay_needs_cursor {
		// this can be true, only when g.overlay != nil, see above
		cx, cy = g.overlay.cursor_position()
	} else {
		cx, cy = g.cursor_position()
	}
	termbox.SetCursor(cx, cy)
}

func (g *godit) draw_status() {
	lp := default_label_params
	r := g.uibuf.Rect
	r.Y = r.Height - 1
	r.Height = 1
	g.uibuf.Fill(r, termbox.Cell{Fg: lp.Fg, Bg: lp.Bg, Ch: ' '})
	g.uibuf.DrawLabel(r, &lp, g.statusbuf.Bytes())
}

func (g *godit) composite_recursively(v *view_tree) {
	if v.leaf != nil {
		g.uibuf.Blit(v.Rect, 0, 0, &v.leaf.uibuf)
		return
	}

	if v.left != nil {
		g.composite_recursively(v.left)
		g.composite_recursively(v.right)
		splitter := v.right.Rect
		splitter.X -= 1
		splitter.Width = 1
		splitter.Height -= 1
		g.uibuf.Fill(splitter, termbox.Cell{
			Fg: termbox.AttrReverse,
			Bg: termbox.AttrReverse,
			Ch: '|',
		})
	} else {
		g.composite_recursively(v.top)
		g.composite_recursively(v.bottom)
	}
}

func (g *godit) cursor_position() (int, int) {
	x, y := g.active.leaf.cursor_position()
	return g.active.X + x, g.active.Y + y
}

func (g *godit) on_sys_key(ev *termbox.Event) {
	switch ev.Key {
	case termbox.KeyCtrlG:
		v := g.active.leaf
		v.ac = nil
		g.set_overlay_mode(nil)
		g.set_status("Quit")
	case termbox.KeyCtrlZ:
		suspend(g)
	}
}

func (g *godit) on_alt_key(ev *termbox.Event) bool {
	switch ev.Ch {
	case 'g':
		g.set_overlay_mode(init_line_edit_mode(g, g.goto_line_lemp()))
		return true
	case '/':
		g.set_overlay_mode(init_autocomplete_mode(g))
		return true
	case 'q':
		g.set_overlay_mode(init_fill_region_mode(g))
		return true
	}
	return false
}

func (g *godit) on_key(ev *termbox.Event) {
	v := g.active.leaf
	switch ev.Key {
	case termbox.KeyCtrlX:
		g.set_overlay_mode(init_extended_mode(g))
	case termbox.KeyCtrlS:
		g.set_overlay_mode(init_isearch_mode(g, false))
	case termbox.KeyCtrlR:
		g.set_overlay_mode(init_isearch_mode(g, true))
	default:
		if ev.Mod&termbox.ModAlt != 0 && g.on_alt_key(ev) {
			break
		}
		v.on_key(ev)
	}
}

func (g *godit) main_loop() {
	g.termbox_event = make(chan termbox.Event, 20)
	go func() {
		for {
			g.termbox_event <- termbox.PollEvent()
		}
	}()
	for {
		select {
		case ev := <-g.termbox_event:
			ok := g.handle_event(&ev)
			if !ok {
				return
			}
			g.consume_more_events()
			g.draw()
			termbox.Flush()
		}
	}
}

func (g *godit) consume_more_events() bool {
	for {
		select {
		case ev := <-g.termbox_event:
			ok := g.handle_event(&ev)
			if !ok {
				return false
			}
		default:
			return true
		}
	}
	panic("unreachable")
}

func (g *godit) handle_event(ev *termbox.Event) bool {
	switch ev.Type {
	case termbox.EventKey:
		if g.recording {
			g.keymacros = append(g.keymacros, create_key_event(ev))
		}
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
	case termbox.EventResize:
		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
		g.resize()
		if g.overlay != nil {
			g.overlay.on_resize(ev)
		}
	case termbox.EventError:
		panic(ev.Err)
	}

	// just dump the current view location from the view to the buffer
	// after each event, it's cheap and does what it needs to be done
	v := g.active.leaf
	v.buf.loc = v.view_location
	return true
}

func (g *godit) set_overlay_mode(m overlay_mode) {
	if g.overlay != nil {
		g.overlay.exit()
	}
	g.overlay = m
}

// used by extended mode only
func (g *godit) save_active_buffer(raw bool) {
	v := g.active.leaf
	b := v.buf

	if b.path != "" {
		if b.synced_with_disk() {
			g.set_status("(No changes need to be saved)")
			g.set_overlay_mode(nil)
			return
		}

		v.presave_cleanup(raw)
		err := b.save()
		if err != nil {
			g.set_status(err.Error())
		} else {
			g.set_status("Wrote %s", b.path)
		}
		g.set_overlay_mode(nil)
		return
	}

	g.set_overlay_mode(init_line_edit_mode(g, g.save_as_buffer_lemp(raw)))
}

// "lemp" stands for "line edit mode params"
func (g *godit) switch_buffer_lemp() line_edit_mode_params {
	return line_edit_mode_params{
		ac_decide:      make_godit_buffer_ac_decide(g),
		prompt:         "Buffer:",
		init_autocompl: true,

		on_apply: func(buf *buffer) {
			bufname := string(buf.contents())
			for _, buf := range g.buffers {
				if buf.name == bufname {
					g.active.leaf.attach(buf)
					return
				}
			}
			g.set_status("(Buffer with this name doesn't exist)")
		},
	}
}

// "lemp" stands for "line edit mode params"
func (g *godit) open_buffer_lemp() line_edit_mode_params {
	return line_edit_mode_params{
		ac_decide: filesystem_line_ac_decide,
		prompt:    "Find file:",

		on_apply: func(buf *buffer) {
			pattern := string(buf.contents())
			if pattern == "" {
				g.set_status("(Nothing to open)")
				return
			}
			g.open_buffers_from_pattern(pattern)
		},
	}
}

// "lemp" stands for "line edit mode params"
func (g *godit) save_as_buffer_lemp(raw bool) line_edit_mode_params {
	v := g.active.leaf
	b := v.buf
	return line_edit_mode_params{
		ac_decide:       filesystem_line_ac_decide,
		prompt:          "File to save in:",
		initial_content: b.name,

		on_apply: func(linebuf *buffer) {
			v.presave_cleanup(raw)
			name := string(linebuf.contents())
			fullpath := abs_path(name)
			err := b.save_as(fullpath)
			if err != nil {
				g.set_status(err.Error())
			} else {
				b.name = ""
				b.name = g.buffer_name(name)
				b.path = fullpath
				v.dirty |= dirty_status
				g.set_status("Wrote %s", b.path)
			}
		},
	}
}

// "lemp" stands for "line edit mode params"
func (g *godit) filter_region_lemp() line_edit_mode_params {
	v := g.active.leaf
	return line_edit_mode_params{
		ac_decide: filesystem_line_ac_decide,
		prompt:    "Filter region through:",
		on_apply: func(linebuf *buffer) {
			v.finalize_action_group()
			cmdstr := string(linebuf.contents())
			v.region_to(func(data []byte) []byte {
				// TODO: not portable
				cmd := exec.Command("/bin/sh", "-c", cmdstr)
				in, err := cmd.StdinPipe()
				if err != nil {
					return clone_byte_slice(data)
				}

				in.Write(data)
				in.Close()

				out, err := cmd.Output()
				if err != nil {
					return clone_byte_slice(data)
				}
				return out
			})
			v.finalize_action_group()
		},
	}
}

// "lemp" stands for "line edit mode params"
func (g *godit) goto_line_lemp() line_edit_mode_params {
	v := g.active.leaf
	return line_edit_mode_params{
		prompt: "Goto line:",
		on_apply: func(buf *buffer) {
			numstr := string(buf.contents())
			num, err := strconv.Atoi(numstr)
			if err != nil {
				g.set_status(err.Error())
				return
			}
			v.on_vcommand(vcommand_move_cursor_to_line, rune(num))
		},
	}
}

// "lemp" stands for "line edit mode params"
func (g *godit) search_and_replace_lemp1() line_edit_mode_params {
	var prompt string
	if len(g.s_and_r_last_word) != 0 {
		prompt = fmt.Sprintf("Replace string [%s]:", g.s_and_r_last_word)
	} else {
		prompt = "Replace string:"
	}
	return line_edit_mode_params{
		prompt: prompt,
		on_apply: func(buf *buffer) {
			var word []byte
			contents := buf.contents()
			if len(contents) == 0 {
				if len(g.s_and_r_last_word) != 0 {
					word = g.s_and_r_last_word
				}
			} else {
				word = contents
			}
			if word == nil {
				g.set_status("Nothing to replace")
				return
			}
			g.set_overlay_mode(init_line_edit_mode(g, g.search_and_replace_lemp2(word)))
		},
	}
}

// "lemp" stands for "line edit mode params"
func (g *godit) search_and_replace_lemp2(word []byte) line_edit_mode_params {
	var prompt string
	if len(g.s_and_r_last_repl) != 0 {
		prompt = fmt.Sprintf("Replace string %s with [%s]:", word, g.s_and_r_last_repl)
	} else {
		prompt = fmt.Sprintf("Replace string %s with:", word)
	}
	v := g.active.leaf
	return line_edit_mode_params{
		prompt: prompt,
		on_apply: func(buf *buffer) {
			var repl []byte
			contents := buf.contents()
			if len(contents) == 0 {
				if len(g.s_and_r_last_repl) != 0 {
					repl = g.s_and_r_last_repl
				}
			} else {
				repl = contents
			}
			v.finalize_action_group()
			v.last_vcommand = vcommand_none
			g.active.leaf.search_and_replace(word, repl)
			v.finalize_action_group()
			g.s_and_r_last_word = word
			g.s_and_r_last_repl = repl
		},
	}
}

func (g *godit) stop_recording() {
	if !g.recording {
		g.set_status("Not defining keyboard macro")
		return
	}

	// clean up the current key combo: "C-x )"
	g.recording = false
	g.keymacros = g.keymacros[:len(g.keymacros)-2]
	if len(g.keymacros) == 0 {
		g.set_status("Ignore empty macro")
	} else {
		g.set_status("Keyboard macro defined")
	}
}

func (g *godit) replay_macro() {
	for _, keyev := range g.keymacros {
		ev := keyev.to_termbox_event()
		g.handle_event(&ev)
	}
}

func (g *godit) view_context() view_context {
	return view_context{
		set_status: func(f string, args ...interface{}) {
			g.set_status(f, args...)
		},
		kill_buffer: &g.killbuffer,
		buffers:     &g.buffers,
	}
}

func (g *godit) has_unsaved_buffers() bool {
	for _, buf := range g.buffers {
		if !buf.synced_with_disk() {
			return true
		}
	}
	return false
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
	godit.main_loop()
}

package main

import (
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
	"strings"
	"unicode/utf8"
)

//----------------------------------------------------------------------------
// line edit mode
//----------------------------------------------------------------------------

type line_edit_mode struct {
	stub_overlay_mode
	line_edit_mode_params
	godit         *godit
	linebuf       *buffer
	lineview      *view
	prompt        []byte
	prompt_w      int
}

type line_edit_mode_params struct {
	on_apply        func(buffer *buffer)
	on_cancel       func()
	key_filter      func(ev *termbox.Event) bool
	post_key_hook   func(buffer *buffer)
	ac_decide       ac_decide_func
	prompt          string
	initial_content string
	init_autocompl  bool
}

func (l *line_edit_mode) exit() {
	if l.on_cancel != nil {
		l.on_cancel()
	}
}

func (l *line_edit_mode) on_key(ev *termbox.Event) {
	if l.key_filter != nil && l.key_filter(ev) {
		goto post_key_hook
	}

	switch ev.Key {
	case termbox.KeyEnter, termbox.KeyCtrlJ:
		if l.lineview.ac != nil {
			l.lineview.on_key(ev)
			if !l.init_autocompl {
				break
			}
		}

		if l.on_apply != nil {
			l.on_apply(l.linebuf)
		}
		l.godit.set_overlay_mode(nil)
		return // return early to avoid running post key hook
	case termbox.KeyTab:
		l.lineview.on_vcommand(vcommand_autocompl_init, 0)
	case termbox.KeyArrowUp:
		l.lineview.on_vcommand(vcommand_autocompl_move_cursor_up, 0)
	case termbox.KeyArrowDown:
		l.lineview.on_vcommand(vcommand_autocompl_move_cursor_down, 0)
	default:
		l.lineview.on_key(ev)
	}

post_key_hook:
	if l.post_key_hook != nil {
		l.post_key_hook(l.linebuf)
	}
}

func (l *line_edit_mode) resize(ev *termbox.Event) {
	w, h := ev.Width-l.prompt_w-1, 1
	if w < 1 || ev.Height < 1 {
		return
	}
	l.lineview.resize(w, h)
}

func (l *line_edit_mode) draw() {
	ui := l.godit.uibuf
	view := l.lineview

	// update label
	prompt_r := tulib.Rect{
		0, ui.Height - 1,
		l.prompt_w + 1, 1,
	}
	ui.Fill(prompt_r, termbox.Cell{
		Fg: termbox.ColorDefault,
		Bg: termbox.ColorDefault,
		Ch: ' ',
	})
	lp := tulib.DefaultLabelParams
	lp.Fg = termbox.ColorCyan
	ui.DrawLabel(prompt_r, &lp, l.prompt)

	// update line view
	view.draw()
	line_r := tulib.Rect{
		l.prompt_w + 1, ui.Height - 1,
		ui.Width - l.prompt_w - 1, 1,
	}
	ui.Blit(line_r, 0, 0, &view.uibuf)
	if view.ac == nil {
		return
	}

	// draw autocompletion
	proposals := view.ac.actual_proposals()
	if len(proposals) > 0 {
		cx, cy := view.cursor_position_for(view.ac.origin)
		view.ac.draw_onto(&ui, line_r.X+cx, line_r.Y+cy)
	}
}

func (l *line_edit_mode) needs_cursor() bool {
	return true
}

func (l *line_edit_mode) cursor_position() (int, int) {
	y := l.godit.uibuf.Height - 1
	x := l.prompt_w + 1
	lx, ly := l.lineview.cursor_position()
	return x + lx, y + ly
}

func init_line_edit_mode(godit *godit, p line_edit_mode_params) *line_edit_mode {
	l := new(line_edit_mode)
	l.godit = godit
	l.line_edit_mode_params = p
	l.linebuf, _ = new_buffer(strings.NewReader(p.initial_content))
	l.lineview = new_view(godit.view_context(), l.linebuf)
	l.lineview.oneline = true          // enable one line mode
	l.lineview.ac_decide = p.ac_decide // override ac_decide function
	l.prompt = []byte(p.prompt)
	l.prompt_w = utf8.RuneCount(l.prompt)
	l.lineview.resize(l.godit.uibuf.Width-l.prompt_w-1, 1)
	l.lineview.on_vcommand(vcommand_move_cursor_end_of_line, 0)
	if l.init_autocompl {
		l.lineview.on_vcommand(vcommand_autocompl_init, 0)
	}
	return l
}

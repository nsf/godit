package main

import (
	"github.com/nsf/termbox-go"
)

type key_press_mode struct {
	stub_overlay_mode
	godit   *godit
	actions map[rune]func()
	def     rune
	prompt  string
}

func init_key_press_mode(godit *godit, actions map[rune]func(), def rune, prompt string) *key_press_mode {
	k := new(key_press_mode)
	k.godit = godit
	k.actions = actions
	k.def = def
	k.prompt = prompt
	k.godit.set_status(prompt)
	return k
}

func (k *key_press_mode) on_key(ev *termbox.Event) {
	if ev.Mod != 0 {
		return
	}

	ch := ev.Ch
	if ev.Key == termbox.KeyEnter || ev.Key == termbox.KeyCtrlJ {
		ch = k.def
	}

	action, ok := k.actions[ch]
	if ok {
		action()
		k.godit.set_overlay_mode(nil)
	} else {
		k.godit.set_status(k.prompt)
	}
}

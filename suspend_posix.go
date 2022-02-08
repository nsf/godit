// +build linux darwin dragonfly solaris openbsd netbsd freebsd

package main

import (
	"github.com/nsf/termbox-go"
	"syscall"
)

func suspend(g *godit) {
	// finalize termbox
	termbox.Close()

	// suspend the process
	pid := syscall.Getpid()
	err := syscall.Kill(pid, syscall.SIGSTOP)
	if err != nil {
		panic(err)
	}

	// reset the state so we can get back to work again
	err = termbox.Init()
	if err != nil {
		panic(err)
	}
	termbox.SetInputMode(termbox.InputAlt)
	g.resize()
}

package main

import (
	"syscall"
	"github.com/nsf/termbox-go"
	"github.com/nsf/tulib"
)

func suspend(g *godit) {
	// finalize termbox
	termbox.Close()

	// suspend the process
	pid := syscall.Getpid()
	tid := syscall.Gettid()
	err := syscall.Tgkill(pid, tid, syscall.SIGSTOP)
	if err != nil {
		panic(err)
	}

	// reset the state so we can get back to work again
	err = termbox.Init()
	if err != nil {
		panic(err)
	}
	g.uibuf = tulib.TermboxBuffer()
	g.resize()
}

// +build android plan9 nacl windows

package main

// do nothing, it's a posix specific feature at the moment
func suspend(g *godit) {}

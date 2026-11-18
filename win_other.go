//go:build !windows

package main

import (
	"os"
	"syscall"
	"time"
)

func focusProcessWindow(int) bool { return false }

func waitProcessExit(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	for p.Signal(syscall.Signal(0)) == nil {
		time.Sleep(time.Second)
	}
}

func showProcessWindows(int, bool) {}

func idleSeconds() int { return 0 }

func systemProxy() (string, bool) { return "", false }

func installResizeBorders() bool { return false }

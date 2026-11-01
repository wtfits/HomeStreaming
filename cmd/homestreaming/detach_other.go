//go:build !windows

package main

import "syscall"

// detachedProcAttr starts a process in its own session.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

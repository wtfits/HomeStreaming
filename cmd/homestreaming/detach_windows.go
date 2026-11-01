//go:build windows

package main

import "syscall"

// detachedProcAttr starts a process that outlives the service stopping it.
func detachedProcAttr() *syscall.SysProcAttr {
	const detachedProcess = 0x00000008
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}

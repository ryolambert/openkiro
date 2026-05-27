//go:build windows

package main

import "syscall"

// daemonSysProcAttr returns SysProcAttr that creates a new process group on
// Windows so the child survives the parent exiting.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

//go:build !windows

package main

import "syscall"

// daemonSysProcAttr returns SysProcAttr that detaches the child process from
// the parent's process group so it survives the parent exiting.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

//go:build !windows

package service

import "errors"

// Name is the Windows Service name.
const Name = "openkiro"

var errNotWindows = errors.New("windows service not supported on this platform")

func RunService(_ string) error       { return errNotWindows }
func IsWindowsService() (bool, error) { return false, nil }
func Install(_, _ string) error       { return errNotWindows }
func Uninstall() error                { return errNotWindows }
func QueryStatus() (string, error)    { return "", errNotWindows }

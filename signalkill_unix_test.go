//go:build !windows

package aarv

import "syscall"

const canRaiseSelfSignal = true

func raiseSelfSignal(sig syscall.Signal) error {
	return syscall.Kill(syscall.Getpid(), sig)
}

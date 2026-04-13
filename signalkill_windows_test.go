//go:build windows

package aarv

import "syscall"

const canRaiseSelfSignal = false

func raiseSelfSignal(sig syscall.Signal) error {
	_ = sig
	return nil
}

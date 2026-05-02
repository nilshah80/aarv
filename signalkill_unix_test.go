//go:build !windows

package aarv

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

const canRaiseSelfSignal = true

var syntheticSignalTarget struct {
	sync.Mutex
	ch chan<- os.Signal
}

func init() {
	listenServerSignalNotify = func(c chan<- os.Signal, _ ...os.Signal) {
		syntheticSignalTarget.Lock()
		syntheticSignalTarget.ch = c
		syntheticSignalTarget.Unlock()
	}
	listenServerSignalStop = func(c chan<- os.Signal) {
		syntheticSignalTarget.Lock()
		if syntheticSignalTarget.ch == c {
			syntheticSignalTarget.ch = nil
		}
		syntheticSignalTarget.Unlock()
	}
}

func raiseSelfSignal(sig syscall.Signal) error {
	deadline := time.Now().Add(time.Second)
	for {
		syntheticSignalTarget.Lock()
		ch := syntheticSignalTarget.ch
		syntheticSignalTarget.Unlock()
		if ch != nil {
			ch <- sig
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no synthetic signal target registered for %s", sig)
		}
		time.Sleep(time.Millisecond)
	}
}

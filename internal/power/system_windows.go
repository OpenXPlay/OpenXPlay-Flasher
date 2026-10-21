// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package power

import (
	"fmt"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

type system struct{}

func NewSystem() Inhibitor { return system{} }

func (system) Acquire() (func(), error) {
	ready, stop, done := make(chan error, 1), make(chan struct{}), make(chan struct{})
	// Windows attaches this assertion to an OS thread; acquire and release on the same one.
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		const continuous, systemRequired = 0x80000000, 0x00000001
		result, _, err := setThreadExecutionState.Call(continuous | systemRequired)
		if result == 0 {
			ready <- fmt.Errorf("SetThreadExecutionState: %w", err)
			return
		}
		ready <- nil
		<-stop
		setThreadExecutionState.Call(continuous)
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(stop); <-done }) }, nil
}

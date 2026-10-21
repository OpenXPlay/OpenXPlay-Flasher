// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build darwin && cgo

package power

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/pwr_mgt/IOPMLib.h>
static IOReturn holdSystemAwake(IOPMAssertionID *id) {
	return IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleSystemSleep,
		kIOPMAssertionLevelOn, CFSTR("OpenXPlay Flasher"), id);
}
*/
import "C"

import (
	"fmt"
	"sync"
)

type system struct{}

func NewSystem() Inhibitor { return system{} }

func (system) Acquire() (func(), error) {
	var id C.IOPMAssertionID
	if status := C.holdSystemAwake(&id); status != 0 {
		return nil, fmt.Errorf("IOPMAssertionCreateWithName: %d", status)
	}
	var once sync.Once
	return func() { once.Do(func() { C.IOPMAssertionRelease(id) }) }, nil
}

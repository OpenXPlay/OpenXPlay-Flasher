// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package usb

import (
	"context"
	"slices"
	"sync"
)

// FakeBus is an in-memory Bus for tests and for running the GUI without hardware.
type FakeBus struct {
	mu      sync.Mutex
	devices []DeviceInfo
	err     error
}

// NewFakeBus returns a bus that lists the given devices.
func NewFakeBus(devices ...DeviceInfo) *FakeBus {
	return &FakeBus{devices: slices.Clone(devices)}
}

// Set replaces the attached devices, as if they had been plugged in or out.
func (b *FakeBus) Set(devices ...DeviceInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.devices = slices.Clone(devices)
}

// Fail makes List return err until it is called again with nil.
func (b *FakeBus) Fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (b *FakeBus) List(context.Context) ([]DeviceInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}
	return slices.Clone(b.devices), nil
}

func (b *FakeBus) Close() error { return nil }

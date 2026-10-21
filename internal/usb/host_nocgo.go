// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !cgo && !windows

package usb

import (
	"context"
	"errors"
)

// ErrNoUSB: macOS and Linux reach USB through libusb, which needs cgo; this build was made without.
var ErrNoUSB = errors.New("USB access is not available in this build (built without cgo)")

type noBus struct{}

// NewHostBus returns a bus that lists nothing and reports ErrNoUSB.
func NewHostBus() Bus { return noBus{} }

func (noBus) List(context.Context) ([]DeviceInfo, error) { return nil, ErrNoUSB }

func (noBus) Close() error { return nil }

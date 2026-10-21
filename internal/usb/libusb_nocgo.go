// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !cgo

package usb

import (
	"context"
	"errors"
)

// ErrNoUSB: this build was made without cgo, so it has no libusb. Windows builds are cgo-free for
// now; a pure-Go WinUSB backend will take this place there.
var ErrNoUSB = errors.New("USB access is not available in this build")

// LibusbBus is the stand-in for builds without cgo: it lists nothing and reports ErrNoUSB.
type LibusbBus struct{}

func NewLibusbBus() *LibusbBus { return &LibusbBus{} }

func (*LibusbBus) List(context.Context) ([]DeviceInfo, error) { return nil, ErrNoUSB }

func (*LibusbBus) Close() error { return nil }

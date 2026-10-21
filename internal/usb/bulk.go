// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package usb

import (
	"context"
	"errors"
)

// Bulk is one claimed interface with a bulk IN and a bulk OUT endpoint, as protocol clients (adb,
// fastboot) use it. OpenBulk opens one; each build provides its own OpenBulk.
type Bulk interface {
	// Read runs one IN transfer into p and returns what arrived: up to len(p) bytes, fewer on a
	// short packet, 0 on a zero-length packet. It returns when the transfer ends or ctx is done.
	Read(ctx context.Context, p []byte) (int, error)
	// Write runs one OUT transfer of p; an empty p sends a zero-length packet.
	Write(ctx context.Context, p []byte) (int, error)
	// MaxPacketSize is the OUT endpoint's packet size, which tells when a transfer needs a
	// zero-length packet to end it.
	MaxPacketSize() int
	// Close releases the interface and the device.
	Close() error
}

var (
	// ErrBusy: another program holds the interface (on macOS an adb server, on Linux a driver).
	ErrBusy = errors.New("usb: the interface is in use by another program")
	// ErrNotFound: the device is not attached, or it has no such interface.
	ErrNotFound = errors.New("usb: no such device or interface")
	// ErrTransfersUnsupported: this build lists devices but cannot open them.
	ErrTransfersUnsupported = errors.New("USB transfers are not supported in this build")
)

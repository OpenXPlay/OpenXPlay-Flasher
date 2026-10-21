// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build !cgo || windows

package usb

import "context"

// OpenBulk reports ErrTransfersUnsupported: builds without libusb (Windows, and macOS or Linux
// without cgo) have no transfer backend yet.
func OpenBulk(context.Context, DeviceInfo, Interface) (Bulk, error) {
	return nil, ErrTransfersUnsupported
}

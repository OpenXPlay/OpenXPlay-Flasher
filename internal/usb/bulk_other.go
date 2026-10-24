// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build !cgo && !windows

package usb

import "context"

// OpenBulk reports ErrTransfersUnsupported for macOS or Linux builds without libusb.
func OpenBulk(context.Context, DeviceInfo, Interface) (Bulk, error) {
	return nil, ErrTransfersUnsupported
}

// TransfersSupported reports the backend capability without opening any device.
func TransfersSupported() bool { return false }

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package device

import (
	"context"
	"time"
)

// Watch scans every interval and calls onChange with the first snapshot and with every snapshot
// that differs from the previous one. gousb does not expose libusb's hot-plug API, and libusb has
// none on Windows, so polling is the common path. Watch returns when ctx is done.
func Watch(ctx context.Context, d Detector, interval time.Duration, onChange func(Snapshot)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last Snapshot
	first := true
	for {
		snap := d.Scan(ctx)
		if ctx.Err() != nil {
			return
		}
		if first || !snap.Equal(last) {
			onChange(snap)
			last, first = snap, false
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

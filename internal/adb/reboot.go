// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"fmt"
)

// RebootBootloader uses the adb service when available (stock Android's shell reboot is often
// restricted). Fakes and older connectors can implement the equivalent shell command.
func RebootBootloader(ctx context.Context, d Device) error {
	if r, ok := d.(interface{ RebootBootloader(context.Context) error }); ok {
		return r.RebootBootloader(ctx)
	}
	result, err := d.Shell(ctx, "reboot bootloader")
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("adb reboot bootloader: exit %d", result.ExitCode)
	}
	return nil
}
func (d *device) RebootBootloader(ctx context.Context) error {
	s, err := d.open(ctx, "reboot:bootloader")
	if err != nil {
		return err
	}
	return s.Close()
}

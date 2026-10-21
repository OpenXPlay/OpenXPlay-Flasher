// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package usb

import (
	"context"
	"testing"
)

// TestSetupAPIListsThisMachine runs against the real SetupAPI: it must succeed on any Windows
// machine, including CI runners with no USB devices attached.
func TestSetupAPIListsThisMachine(t *testing.T) {
	devices, err := NewHostBus().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		t.Logf("%s at %s interfaces %v", d.ID(), d.Location(), d.Interfaces)
	}
}

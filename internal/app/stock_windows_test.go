// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func TestWindowsStockDesignationPreservesActualSerial(t *testing.T) {
	s, d, fb, _, _ := baseServiceFixture(t)
	unknown := testCandidate(profile.ModeADB, 2, false)
	unknown.USB.VendorID, unknown.USB.ProductID = 0x05c6, 0x90b4
	unknown.USB.Serial = "BOARD1" // Windows' case-folded instance ID
	d.det.set(unknown)
	state, err := s.DesignateStockSerial(t.Context(), "board1")
	if err != nil || state.Identity == nil || state.Identity.Serial != "board1" || state.Identity.Mode != profile.ModeStock {
		t.Fatalf("designation lost the original USB serial: identity=%+v err=%v", state.Identity, err)
	}
	state = s.Install(t.Context())
	if state.Identity == nil || state.Identity.Mode != profile.ModeStock || state.Suggested != "stock" {
		t.Fatalf("designation was lost after rescanning: %+v", state)
	}
	if _, err := s.InstallStart(t.Context(), stage.Request{Choice: "stock", YesErase: "BOARD1"}); err == nil {
		t.Fatal("case-folded Windows instance serial authorized an erase")
	}
	if len(fb.calls) != 0 {
		t.Fatal("an unconfirmed stock install touched fastboot")
	}
}

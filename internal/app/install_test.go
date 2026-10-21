// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type discardEvents struct{}

func (discardEvents) Emit(string, any) {}

func TestServiceCanStopImmediately(t *testing.T) {
	// Cancelling at the CLI prompt can stop the service before its watch goroutine starts.
	s, err := NewForBus(usb.NewFakeBus(), discardEvents{})
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		s.Start(context.Background())
		s.Stop()
	}
}

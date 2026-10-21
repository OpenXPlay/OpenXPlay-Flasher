// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestFlashGuardBeforeEveryPieceAndBeforeWriting(t *testing.T) {
	img := rawFixture(make([]byte, 8192))
	denied := errors.New("another device appeared")
	// A 4160-byte limit needs two pieces. Stop before the second download; no further USB
	// command may be sent, even though Flash holds the same protocol connection.
	first, err := img.pieces(t.Context(), 4160)
	if err != nil {
		t.Fatal(err)
	}
	steps := []bulkStep{out("getvar:max-download-size"), in("OKAY268435456"), out("getvar:partition-size:boot"), in("OKAY8192")}
	data, err := io.ReadAll(first[0].reader(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	steps = append(steps, flashSteps("boot", data)...)
	c, _ := script(t, steps...)
	calls := 0
	err = c.Flash(context.Background(), "boot", img, FlashOptions{MaxPiece: 4160, BeforeWrite: func(context.Context) error {
		calls++
		if calls == 3 {
			return denied
		}
		return nil
	}}, nil)
	if !errors.Is(err, denied) || calls != 3 {
		t.Fatalf("guard calls %d: %v", calls, err)
	}
}

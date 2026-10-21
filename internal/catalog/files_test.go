// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHashProgressAndCancellation(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "large fake file")
	if err := os.WriteFile(filename, bytes.Repeat([]byte{42}, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var done, total int64
	_, _, err := HashProgress(ctx, filename, func(p Progress) { done, total = p.Done, p.Total; cancel() })
	if !errors.Is(err, context.Canceled) || done <= 0 || done >= total || total != 8<<20 {
		t.Fatalf("cancellation/progress: %d/%d %v", done, total, err)
	}
	n, sum, err := HashProgress(t.Context(), filename, func(p Progress) { done, total = p.Done, p.Total })
	if err != nil || n != 8<<20 || len(sum) != 64 || done != total {
		t.Fatalf("completed hash: %d/%d %v", done, total, err)
	}
}

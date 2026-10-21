// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

// lk1st's oem dump returns a partition at the size the GPT in place gives it: 1.5 MiB under the
// stock table, 2 MiB under OpenStick's. Any allowed size is kept as it is; others are refused, and
// a larger dump that starts with the author's file is refused like an exact copy.
func TestDumpSizesFollowTheGPT(t *testing.T) {
	author := bytes.Repeat([]byte{22}, 64) // the fixture's author fsg.bin
	for _, tc := range []struct {
		name string
		fsg  []byte
		want string // problem code, "" for success
	}{
		{"stock GPT size", bytes.Repeat([]byte{7}, 64), ""},
		{"OpenStick GPT size", bytes.Repeat([]byte{8}, 128), ""},
		{"size not allowed", bytes.Repeat([]byte{9}, 96), "backup-size"},
		{"larger than allowed", bytes.Repeat([]byte{9}, 200), "backup-size"},
		{"starts with the author's file", append(bytes.Clone(author), bytes.Repeat([]byte{5}, 64)...), "backup-author"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := makeFixture(t)
			f.store.Profile.Base.Protected["fsg"] = []int64{64, 128}
			f.dumps["fsg"] = tc.fsg
			b, err := f.store.Capture(context.Background(), "board1", "oem-dump", func(_ context.Context, name string, w io.Writer) error {
				_, err := w.Write(f.dumps[name])
				return err
			})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if b.Partitions["fsg"].Size != int64(len(tc.fsg)) {
					t.Fatalf("recorded size %d, want %d", b.Partitions["fsg"].Size, len(tc.fsg))
				}
				if _, err := f.store.Validate(t.Context(), "board1", b.Set); err != nil {
					t.Fatal(err)
				}
				return
			}
			var p *stage.Problem
			if !errors.As(err, &p) || p.Code != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
		})
	}
}

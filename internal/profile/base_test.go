// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package profile

import (
	"slices"
	"testing"
)

func TestBasePinsAndValidation(t *testing.T) {
	profiles, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	b := profiles[0].Base
	if b.Release != "openstick-v1" || b.Files["rootfs"].Size != 919755256 || b.Files["boot"].Member != "" || !slices.Equal(b.Protected["fsg"], []int64{1572864, 2097152}) || !slices.Equal(b.Protected["fsc"], []int64{1024}) || b.Fastboot.MaxPiece != 209715200 {
		t.Fatalf("incomplete base: %+v", b)
	}
	for _, tc := range []struct {
		name   string
		change func(*Base)
	}{
		{"missing", func(b *Base) { delete(b.Files, "gpt") }},
		{"member hash", func(b *Base) { f := b.Files["rootfs"]; f.SHA256 = "wrong"; b.Files["rootfs"] = f }},
		{"traversal", func(b *Base) { f := b.Files["hyp"]; f.Member = "../hyp"; b.Files["hyp"] = f }},
		{"author writable", func(b *Base) { f := b.Files["hyp"]; f.Member = "base/fsg.bin"; b.Files["hyp"] = f }},
		{"generic boot", func(b *Base) {
			f := b.Files["boot"]
			f.Archive = "rootfs-zip"
			f.Member = "debian/boot.img"
			b.Files["boot"] = f
		}},
		{"piece size", func(b *Base) { b.Fastboot.MaxPiece = 201 << 20 }},
		{"protected missing", func(b *Base) { delete(b.Protected, "fsc") }},
		{"stock property", func(b *Base) { delete(b.Stock.Properties, "ro.product.model") }},
		{"Linux USB", func(b *Base) { b.Linux.VendorID = "invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps, err := Builtin()
			if err != nil {
				t.Fatal(err)
			}
			tc.change(ps[0].Base)
			if err := ps[0].Validate(); err == nil {
				t.Fatal("invalid base accepted")
			}
		})
	}
}

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package base

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testgpt"
)

func TestGPTTemplateAndFixedDisk(t *testing.T) {
	want := map[string]int64{"aboot": 1 << 20, "tz": 1 << 20, "校准😀": 512, "rootfs": 0}
	data := testgpt.Image(want)
	got, err := gptSizes(bytes.NewReader(data), int64(len(data)))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("sizes=%v err=%v", got, err)
	}
	delete(want, "rootfs")
	data = testgpt.Image(want)
	binary.LittleEndian.PutUint64(data[512+32:], 1000000)
	binary.LittleEndian.PutUint64(data[512+48:], 999966)
	testgpt.Checksum(data)
	got, err = gptSizes(bytes.NewReader(data), int64(len(data)))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed disk sizes=%v err=%v", got, err)
	}
}

func TestGPTRejectsMalformedInput(t *testing.T) {
	fixHeader := func(b []byte) {
		h := b[512:1024]
		binary.LittleEndian.PutUint32(h[16:], 0)
		binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
	}
	for _, tc := range []struct {
		name   string
		change func([]byte)
	}{
		{"MBR signature", func(b []byte) { b[510] = 0 }},
		{"protective MBR", func(b []byte) { b[450] = 0 }},
		{"header signature", func(b []byte) { b[512] = 0; fixHeader(b) }},
		{"revision", func(b []byte) { b[520] = 1; fixHeader(b) }},
		{"short header", func(b []byte) { binary.LittleEndian.PutUint32(b[524:], 91) }},
		{"oversized header", func(b []byte) { binary.LittleEndian.PutUint32(b[524:], 513) }},
		{"reserved", func(b []byte) { b[532] = 1; fixHeader(b) }},
		{"current LBA", func(b []byte) { b[536] = 2; fixHeader(b) }},
		{"header CRC", func(b []byte) { b[568] ^= 1 }},
		{"entry CRC", func(b []byte) { b[1024] ^= 1 }},
		{"entry offset", func(b []byte) { binary.LittleEndian.PutUint64(b[584:], math.MaxUint64); fixHeader(b) }},
		{"entry count", func(b []byte) { binary.LittleEndian.PutUint32(b[592:], math.MaxUint32); fixHeader(b) }},
		{"entry size", func(b []byte) { binary.LittleEndian.PutUint32(b[596:], 120); fixHeader(b) }},
		{"usable LBA", func(b []byte) { binary.LittleEndian.PutUint64(b[552:], 2); fixHeader(b) }},
		{"last usable LBA", func(b []byte) { binary.LittleEndian.PutUint64(b[560:], 100); fixHeader(b) }},
		{"empty name", func(b []byte) { clear(b[1080:1152]); testgpt.Checksum(b) }},
		{"bad UTF16", func(b []byte) { binary.LittleEndian.PutUint16(b[1080:], 0xd800); testgpt.Checksum(b) }},
		{"name after NUL", func(b []byte) { binary.LittleEndian.PutUint16(b[1082:], 0); b[1084] = 1; testgpt.Checksum(b) }},
		{"duplicate name", func(b []byte) { copy(b[1208:1280], b[1080:1152]); testgpt.Checksum(b) }},
		{"before usable range", func(b []byte) { binary.LittleEndian.PutUint64(b[1056:], 1); testgpt.Checksum(b) }},
		{"reversed partition", func(b []byte) { binary.LittleEndian.PutUint64(b[1064:], 100); testgpt.Checksum(b) }},
		{"size overflow", func(b []byte) { binary.LittleEndian.PutUint64(b[1064:], math.MaxUint64); testgpt.Checksum(b) }},
		{"overlap", func(b []byte) { copy(b[1184:1200], b[1056:1072]); testgpt.Checksum(b) }},
		{"empty table", func(b []byte) { clear(b[1024:17408]); testgpt.Checksum(b) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testgpt.Image(map[string]int64{"aboot": 1 << 20, "tz": 1 << 20})
			tc.change(b)
			if sizes, err := gptSizes(bytes.NewReader(b), int64(len(b))); err == nil {
				t.Fatalf("accepted invalid GPT: %v", sizes)
			}
		})
	}
	for _, n := range []int{0, 512, 1023, 1024, 17407} {
		b := testgpt.Base()[:n]
		if _, err := gptSizes(bytes.NewReader(b), int64(len(b))); err == nil {
			t.Fatalf("accepted truncation at %d", n)
		}
	}
}

func TestGPTPinnedArchiveOffline(t *testing.T) {
	profiles, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	pin := profiles[0].Base.Archives["base-generic"]
	path := os.Getenv("OPENXPLAY_BASE_ARCHIVE")
	if path == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Skip("no offline cache directory")
		}
		path = filepath.Join(cache, "OpenXPlay Flasher", "artifacts", pin.SHA256, pin.File)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Skip("pinned archive not cached offline; constructed GPT tests cover the format")
		}
	}
	if err := verify(t.Context(), path, pin.Size, pin.SHA256); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	gpt := profiles[0].Base.Files["gpt"]
	r, err := z.Open(gpt.Member)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, gpt.Size+1))
	if err != nil || int64(len(data)) != gpt.Size || hash(data) != gpt.SHA256 {
		t.Fatalf("invalid pinned member: %v", err)
	}
	sizes, err := gptSizes(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"tz": 1 << 20, "sbl1": 512 << 10, "aboot": 1 << 20, "modemst1": 2 << 20, "rootfs": 0} {
		if got, ok := sizes[name]; !ok || got != want {
			t.Fatalf("%s=%d want %d (present=%v)", name, got, want, ok)
		}
	}
}

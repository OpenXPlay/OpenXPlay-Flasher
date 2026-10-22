// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

// Package testgpt constructs offline partition-table fixtures for transport and app tests.
package testgpt

import (
	"encoding/binary"
	"hash/crc32"
	"maps"
	"slices"
	"unicode/utf16"
)

// Image follows OpenStick's compact 512-byte-sector template layout, including its zero last
// disk LBA and final rootfs entry with end=start-1. Positive sizes are rounded up to a sector.
func Image(sizes map[string]int64) []byte {
	data := make([]byte, 67*512)
	data[450] = 0xee
	binary.LittleEndian.PutUint32(data[454:], 1)
	binary.LittleEndian.PutUint32(data[458:], 0xffffffff)
	data[510], data[511] = 0x55, 0xaa
	h := data[512:1024]
	copy(h, "EFI PART")
	binary.LittleEndian.PutUint32(h[8:], 0x10000)
	binary.LittleEndian.PutUint32(h[12:], 92)
	binary.LittleEndian.PutUint64(h[24:], 1)
	binary.LittleEndian.PutUint64(h[40:], 34)
	h[56] = 1
	binary.LittleEndian.PutUint64(h[72:], 2)
	binary.LittleEndian.PutUint32(h[80:], 128)
	binary.LittleEndian.PutUint32(h[84:], 128)
	names := slices.Sorted(maps.Keys(sizes))
	if len(names) > 128 {
		panic("too many test partitions")
	}
	// A grow-to-end partition is physically last, regardless of its name.
	slices.SortStableFunc(names, func(a, b string) int {
		if sizes[a] == 0 && sizes[b] != 0 {
			return 1
		}
		if sizes[a] != 0 && sizes[b] == 0 {
			return -1
		}
		return 0
	})
	start := uint64(2048)
	for i, name := range names {
		e := data[1024+i*128 : 1024+(i+1)*128]
		e[0], e[16] = 1, byte(i+1)
		sectors := uint64((sizes[name] + 511) / 512)
		binary.LittleEndian.PutUint64(e[32:], start)
		binary.LittleEndian.PutUint64(e[40:], start+sectors-1)
		for n, u := range utf16.Encode([]rune(name)) {
			if n >= 36 {
				panic("test partition name too long")
			}
			binary.LittleEndian.PutUint16(e[56+n*2:], u)
		}
		start += sectors
	}
	Checksum(data)
	return data
}

// Checksum repairs the primary CRCs after a test deliberately changes a header or entry.
func Checksum(data []byte) {
	h := data[512:1024]
	offset := binary.LittleEndian.Uint64(h[72:]) * 512
	length := uint64(binary.LittleEndian.Uint32(h[80:])) * uint64(binary.LittleEndian.Uint32(h[84:]))
	binary.LittleEndian.PutUint32(h[88:], crc32.ChecksumIEEE(data[offset:offset+length]))
	binary.LittleEndian.PutUint32(h[16:], 0)
	binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(h[:binary.LittleEndian.Uint32(h[12:])]))
}

// Base is a small table covering every partition written by the base procedure.
func Base() []byte {
	return Image(map[string]int64{
		"hyp": 512 << 10, "rpm": 512 << 10, "sbl1": 512 << 10, "tz": 1 << 20,
		"fsc": 1024, "fsg": 2 << 20, "modemst1": 2 << 20, "modemst2": 2 << 20,
		"aboot": 1 << 20, "cdt": 2048, "boot": 64 << 20, "rootfs": 0,
	})
}

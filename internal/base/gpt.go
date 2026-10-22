// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package base

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"slices"
	"unicode/utf16"
)

const sectorSize = 512

// gptSizes reads the protective MBR, primary header and entry array. gpt_both0.bin is a
// compact flashing template, not a disk image: its backup header need not be at its disk LBA.
// OpenStick leaves the disk's last LBA and the final grow-to-end partition size at zero.
// That partition can be erased, but its capacity is unknown until the bootloader restarts.
func gptSizes(r io.ReaderAt, size int64) (map[string]int64, error) {
	bad := func(why string) (map[string]int64, error) { return nil, fmt.Errorf("GPT: %s", why) }
	if size < 2*sectorSize || size%sectorSize != 0 {
		return bad("truncated image")
	}
	var sectors [2 * sectorSize]byte
	if _, err := r.ReadAt(sectors[:], 0); err != nil {
		return nil, err
	}
	mbr, h := sectors[:sectorSize], sectors[sectorSize:]
	u32 := binary.LittleEndian.Uint32
	u64 := binary.LittleEndian.Uint64
	protective := false
	for off := 446; off < 510; off += 16 {
		if mbr[off+4] == 0xee && u32(mbr[off+8:]) == 1 && u32(mbr[off+12:]) != 0 {
			protective = true
		}
	}
	if mbr[510] != 0x55 || mbr[511] != 0xaa || !protective {
		return bad("missing protective MBR")
	}
	headerSize := u32(h[12:])
	if string(h[:8]) != "EFI PART" || u32(h[8:]) != 0x10000 || headerSize < 92 || headerSize > sectorSize || u32(h[20:]) != 0 || u64(h[24:]) != 1 {
		return bad("invalid primary header")
	}
	wantCRC := u32(h[16:])
	binary.LittleEndian.PutUint32(h[16:], 0)
	if crc32.ChecksumIEEE(h[:headerSize]) != wantCRC {
		return bad("header CRC mismatch")
	}
	backup, first, last := u64(h[32:]), u64(h[40:]), u64(h[48:])
	template := backup == 0 && last == 0
	if first < 3 || first > math.MaxInt64/sectorSize || !template && (last < first || last >= backup || backup > math.MaxInt64/sectorSize) {
		return bad("invalid usable LBAs")
	}
	entryLBA, count, entrySize := u64(h[72:]), uint64(u32(h[80:])), uint64(u32(h[84:]))
	if entryLBA < 2 || entryLBA >= first || entryLBA > uint64(size/sectorSize) || count == 0 || entrySize < 128 || entrySize%128 != 0 {
		return bad("invalid entry array")
	}
	offset, length := entryLBA*sectorSize, count*entrySize
	if length > uint64(size)-offset || length > (first-entryLBA)*sectorSize || length > 16<<20 {
		return bad("entry array out of bounds")
	}
	entries := make([]byte, int(length))
	if _, err := r.ReadAt(entries, int64(offset)); err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(entries) != u32(h[88:]) {
		return bad("entry array CRC mismatch")
	}
	type extent struct{ first, last uint64 }
	extents := []extent{}
	sizes := map[string]int64{}
	var growStart uint64
	var emptyGUID [16]byte
	for off := uint64(0); off < length; off += entrySize {
		e := entries[off : off+entrySize]
		if bytes.Equal(e[:16], emptyGUID[:]) {
			continue
		}
		units := []uint16{}
		ended := false
		for i := 56; i < 128; i += 2 {
			u := binary.LittleEndian.Uint16(e[i:])
			if u == 0 {
				ended = true
			} else if ended {
				return bad("invalid partition name")
			} else {
				units = append(units, u)
			}
		}
		name := string(utf16.Decode(units))
		if name == "" || !slices.Equal(utf16.Encode([]rune(name)), units) {
			return bad("invalid UTF-16 partition name")
		}
		if _, exists := sizes[name]; exists {
			return bad("duplicate partition name")
		}
		start, end := u64(e[32:]), u64(e[40:])
		if start < first || start > math.MaxInt64/sectorSize {
			return bad("partition start out of bounds")
		}
		if template && end == start-1 && growStart == 0 {
			growStart = start
			sizes[name] = 0
			continue
		}
		if end < start || end >= math.MaxInt64/sectorSize || !template && end > last {
			return bad("partition end out of bounds")
		}
		extents = append(extents, extent{start, end})
		sizes[name] = int64(end-start+1) * sectorSize
	}
	slices.SortFunc(extents, func(a, b extent) int {
		if a.first < b.first {
			return -1
		}
		if a.first > b.first {
			return 1
		}
		return 0
	})
	for i, e := range extents {
		if i > 0 && e.first <= extents[i-1].last || growStart != 0 && e.last >= growStart {
			return bad("overlapping partitions")
		}
	}
	if len(sizes) == 0 {
		return bad("empty partition table")
	}
	return sizes, nil
}

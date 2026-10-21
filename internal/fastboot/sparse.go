// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

const (
	sparseMagic      = 0xed26ff3a
	sparseHeaderSize = 28
	chunkHeaderSize  = 12
	chunkRaw         = 0xcac1
	chunkFill        = 0xcac2
	chunkDontCare    = 0xcac3
	chunkCRC32       = 0xcac4
	rawBlockSize     = 4096
)

type sparseImage struct {
	blockSize, blocks uint32
	// Only material chunks need indexing. Holes are represented by gaps between starts; CRC32
	// chunks have no blocks. Resparse drops checksums, as each piece writes a different subset.
	chunks []sparseChunk
}

type sparseChunk struct {
	kind          uint16
	start, blocks uint32
	offset, bytes int64 // RAW source extent; bytes can exclude a raw file's final block padding
	fill          [4]byte
}

func imageError(offset int64, format string, args ...any) error {
	return &ImageError{Offset: offset, Message: fmt.Sprintf(format, args...)}
}

// parseSparse validates declared header sizes, chunk payload sizes and the complete block/file
// accounting. Extra header fields are skipped using the declared lengths, never treated as data.
// CRC32 values are advisory (as in fastboot's normal import); this is structural validation, not
// a content checksum pass over potentially gigabytes of expanded holes.
func parseSparse(r io.ReaderAt, size int64) (*sparseImage, error) {
	var h [sparseHeaderSize]byte
	if size < sparseHeaderSize {
		return nil, imageError(0, "truncated sparse header")
	}
	if _, err := r.ReadAt(h[:], 0); err != nil {
		return nil, imageError(0, "read sparse header: %v", err)
	}
	le := binary.LittleEndian
	if le.Uint32(h[:]) != sparseMagic {
		return nil, imageError(0, "bad sparse magic")
	}
	if le.Uint16(h[4:]) != 1 || le.Uint16(h[6:]) != 0 {
		return nil, imageError(4, "unsupported sparse version %d.%d", le.Uint16(h[4:]), le.Uint16(h[6:]))
	}
	fileHeader, chunkHeader := int64(le.Uint16(h[8:])), int64(le.Uint16(h[10:]))
	if fileHeader < sparseHeaderSize || chunkHeader < chunkHeaderSize || fileHeader > size {
		return nil, imageError(8, "invalid file/chunk header sizes %d/%d", fileHeader, chunkHeader)
	}
	s := &sparseImage{blockSize: le.Uint32(h[12:]), blocks: le.Uint32(h[16:])}
	if s.blockSize == 0 || s.blockSize%4 != 0 {
		return nil, imageError(12, "block size %d must be a positive multiple of four", s.blockSize)
	}
	if uint64(s.blockSize)*uint64(s.blocks) > math.MaxInt64 {
		return nil, imageError(16, "expanded image exceeds int64 size")
	}
	nchunks := le.Uint32(h[20:])
	if int64(nchunks) > (size-fileHeader)/chunkHeader {
		return nil, imageError(20, "chunk count cannot fit in the file")
	}
	offset := fileHeader
	var blocks uint64
	var ch [chunkHeaderSize]byte
	for i := uint32(0); i < nchunks; i++ {
		if chunkHeader > size-offset {
			return nil, imageError(offset, "truncated chunk header")
		}
		if _, err := r.ReadAt(ch[:], offset); err != nil {
			return nil, imageError(offset, "read chunk header: %v", err)
		}
		kind, reserved := le.Uint16(ch[:]), le.Uint16(ch[2:])
		count, total := le.Uint32(ch[4:]), int64(le.Uint32(ch[8:]))
		if reserved != 0 {
			return nil, imageError(offset+2, "nonzero reserved chunk field")
		}
		var payload int64
		switch kind {
		case chunkRaw:
			// A valid RAW chunk's total size is uint32, so avoid an overflowing int64
			// multiplication on malformed chunks with huge block counts.
			if uint64(count)*uint64(s.blockSize) > math.MaxUint32-uint64(chunkHeader) {
				return nil, imageError(offset, "RAW chunk exceeds its uint32 total size")
			}
			payload = int64(count) * int64(s.blockSize)
		case chunkFill:
			payload = 4
		case chunkDontCare:
		case chunkCRC32:
			payload = 4
			if count != 0 {
				return nil, imageError(offset+4, "CRC32 chunk must have zero blocks")
			}
		default:
			return nil, imageError(offset, "unknown chunk type %#x", kind)
		}
		if total != chunkHeader+payload {
			return nil, imageError(offset+8, "chunk size %d, expected %d", total, chunkHeader+payload)
		}
		if total > size-offset {
			return nil, imageError(offset, "truncated chunk payload")
		}
		if blocks+uint64(count) > uint64(s.blocks) {
			return nil, imageError(offset+4, "chunk blocks exceed total blocks")
		}
		c := sparseChunk{kind: kind, start: uint32(blocks), blocks: count, offset: offset + chunkHeader, bytes: payload}
		if kind == chunkFill || kind == chunkCRC32 {
			if _, err := r.ReadAt(c.fill[:], c.offset); err != nil {
				return nil, imageError(c.offset, "read chunk value: %v", err)
			}
		}
		if count > 0 && (kind == chunkRaw || kind == chunkFill) {
			s.chunks = append(s.chunks, c)
		}
		blocks += uint64(count)
		offset += total
	}
	if blocks != uint64(s.blocks) {
		return nil, imageError(offset, "chunks cover %d blocks, header declares %d", blocks, s.blocks)
	}
	if offset != size {
		return nil, imageError(offset, "%d trailing bytes after sparse chunks", size-offset)
	}
	return s, nil
}

func sparseHeader(blockSize, blocks, chunks uint32) []byte {
	h := make([]byte, sparseHeaderSize)
	le := binary.LittleEndian
	le.PutUint32(h, sparseMagic)
	le.PutUint16(h[4:], 1)
	le.PutUint16(h[8:], sparseHeaderSize)
	le.PutUint16(h[10:], chunkHeaderSize)
	le.PutUint32(h[12:], blockSize)
	le.PutUint32(h[16:], blocks)
	le.PutUint32(h[20:], chunks)
	// Minor version and image checksum stay zero. Original checksums don't describe a piece.
	return h
}

func chunkHeader(kind uint16, blocks uint32, payload int64) []byte {
	h := make([]byte, chunkHeaderSize)
	le := binary.LittleEndian
	le.PutUint16(h, kind)
	le.PutUint32(h[4:], blocks)
	le.PutUint32(h[8:], uint32(chunkHeaderSize+payload))
	return h
}

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"bytes"
	"context"
	"io"
	"math"
)

// sparsePiece holds only chunk descriptors. Even a 200 MiB piece has no payload buffer: reader
// joins small generated headers, source-file sections, and at most one block of virtual padding.
type sparsePiece struct {
	source            io.ReaderAt
	blockSize, blocks uint32
	chunks            []sparseChunk
	size              int64
}

// pieces follows resparse's block map semantics: every output has the original total block count,
// with DONT_CARE gaps wherever this piece has no writes. CRC chunks and image checksums are dropped.
// Splitting RAW chunks at block boundaries makes progress even when one input chunk exceeds limit.
func (img *Image) pieces(ctx context.Context, limit int64) ([]sparsePiece, error) {
	if err := ctx.Err(); err != nil {
		return nil, &OpError{Phase: "splitting image", Err: err}
	}
	s := img.sparse
	if s == nil {
		blocks := img.stored / rawBlockSize
		if img.stored%rawBlockSize != 0 {
			blocks++
		}
		if blocks > math.MaxUint32 {
			return nil, imageError(0, "raw image needs more than uint32 blocks")
		}
		s = &sparseImage{blockSize: rawBlockSize, blocks: uint32(blocks)}
		if blocks > 0 {
			s.chunks = []sparseChunk{{kind: chunkRaw, blocks: uint32(blocks), bytes: img.stored}}
		}
	}
	limit = min(limit, maxDownload)
	minimum := int64(sparseHeaderSize)
	if s.blocks > 0 {
		minimum += chunkHeaderSize
	}
	if limit < minimum {
		return nil, &PieceLimitError{Limit: limit, Minimum: minimum}
	}
	newPiece := func() sparsePiece {
		return sparsePiece{source: img.source, blockSize: s.blockSize, blocks: s.blocks, size: sparseHeaderSize}
	}
	piece := newPiece()
	var result []sparsePiece
	var end uint32
	finish := func() {
		if end < s.blocks {
			piece.size += chunkHeaderSize
		}
		result = append(result, piece)
		piece, end = newPiece(), 0
	}
	for _, original := range s.chunks {
		c := original
		for c.blocks > 0 {
			if err := ctx.Err(); err != nil {
				return nil, &OpError{Phase: "splitting image", Err: err}
			}
			gap := int64(0)
			if c.start > end {
				gap = chunkHeaderSize
			}
			trailing := int64(0)
			if c.start+c.blocks < s.blocks {
				trailing = chunkHeaderSize
			}
			payload := int64(4)
			if c.kind == chunkRaw {
				payload = int64(c.blocks) * int64(s.blockSize)
			}
			take := c.blocks
			if piece.size+gap+chunkHeaderSize+payload+trailing > limit {
				take = 0
				if c.kind == chunkRaw {
					// A split leaves blocks after it, so reserve the trailing hole too.
					room := limit - piece.size - gap - 2*chunkHeaderSize
					if room > 0 {
						take = uint32(min(room/int64(s.blockSize), int64(c.blocks)))
					}
				}
			}
			if take == 0 {
				if len(piece.chunks) > 0 {
					finish()
					continue
				}
				payload = 4
				trailing = 0
				if c.kind == chunkRaw {
					payload = int64(s.blockSize)
					if c.start+1 < s.blocks {
						trailing = chunkHeaderSize
					}
				} else if c.start+c.blocks < s.blocks {
					trailing = chunkHeaderSize
				}
				return nil, &PieceLimitError{Limit: limit, Minimum: piece.size + gap + chunkHeaderSize + payload + trailing}
			}
			part := c
			part.blocks = take
			if c.kind == chunkRaw {
				payload = int64(take) * int64(s.blockSize)
				part.bytes = min(c.bytes, payload)
				c.offset += part.bytes
				c.bytes -= part.bytes
			} else {
				payload = 4
			}
			piece.size += gap + chunkHeaderSize + payload
			piece.chunks = append(piece.chunks, part)
			end = c.start + take
			c.start, c.blocks = end, c.blocks-take
			if c.blocks > 0 {
				finish()
			}
		}
	}
	// An entirely empty/holes-only image still has a valid sparse representation.
	if len(piece.chunks) > 0 || len(result) == 0 {
		finish()
	}
	return result, nil
}

func (p sparsePiece) reader(ctx context.Context) io.Reader {
	nchunks := uint32(len(p.chunks))
	var end uint32
	for _, c := range p.chunks {
		if c.start > end {
			nchunks++
		}
		end = c.start + c.blocks
	}
	if end < p.blocks {
		nchunks++
	}
	readers := []io.Reader{bytes.NewReader(sparseHeader(p.blockSize, p.blocks, nchunks))}
	end = 0
	for _, c := range p.chunks {
		if c.start > end {
			readers = append(readers, bytes.NewReader(chunkHeader(chunkDontCare, c.start-end, 0)))
		}
		if c.kind == chunkRaw {
			payload := int64(c.blocks) * int64(p.blockSize)
			readers = append(readers, bytes.NewReader(chunkHeader(chunkRaw, c.blocks, payload)),
				io.NewSectionReader(p.source, c.offset, c.bytes))
			if c.bytes < payload {
				readers = append(readers, io.LimitReader(zeroReader{}, payload-c.bytes))
			}
		} else {
			readers = append(readers, bytes.NewReader(chunkHeader(chunkFill, c.blocks, 4)), bytes.NewReader(c.fill[:]))
		}
		end = c.start + c.blocks
	}
	if end < p.blocks {
		readers = append(readers, bytes.NewReader(chunkHeader(chunkDontCare, p.blocks-end, 0)))
	}
	return contextReader{ctx: ctx, r: io.MultiReader(readers...)}
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// zeroReader supplies raw file padding without allocating a block or changing the source file.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

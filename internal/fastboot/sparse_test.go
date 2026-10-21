// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

type testChunk struct {
	kind   uint16
	blocks uint32
	data   []byte
}

// fixtureSparse intentionally encodes headers independently of the production writer.
func fixtureSparse(blockSize uint32, chunks []testChunk, extensions bool) []byte {
	fh, ch := 28, 12
	if extensions {
		fh, ch = 36, 20
	}
	h := make([]byte, fh)
	le := binary.LittleEndian
	le.PutUint32(h, 0xed26ff3a)
	le.PutUint16(h[4:], 1)
	le.PutUint16(h[8:], uint16(fh))
	le.PutUint16(h[10:], uint16(ch))
	le.PutUint32(h[12:], blockSize)
	var blocks uint32
	for _, c := range chunks {
		blocks += c.blocks
	}
	le.PutUint32(h[16:], blocks)
	le.PutUint32(h[20:], uint32(len(chunks)))
	for _, c := range chunks {
		header := make([]byte, ch)
		le.PutUint16(header, c.kind)
		le.PutUint32(header[4:], c.blocks)
		le.PutUint32(header[8:], uint32(ch+len(c.data)))
		h = append(h, header...)
		h = append(h, c.data...)
	}
	return h
}

func sparseFixture(t *testing.T, data []byte) *Image {
	t.Helper()
	s, err := parseSparse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return &Image{source: bytes.NewReader(data), stored: int64(len(data)), sparse: s}
}

func rawFixture(data []byte) *Image {
	return &Image{source: bytes.NewReader(data), stored: int64(len(data))}
}

func openFixture(t *testing.T, data []byte) *Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.img")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	img, err := OpenImage(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := img.Close(); err != nil {
			t.Error(err)
		}
	})
	return img
}

type sparseStats struct {
	blockSize, blocks, checksum uint32
	crcChunks                   int
	written                     uint64
	bytes                       int64
}

type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}

// walkSparse is an independent, streaming block-device interpreter. It checks emitted headers
// and visits only material blocks, leaving DONT_CARE blocks unchanged in its caller's device.
func walkSparse(t *testing.T, r io.Reader, visit func(uint32, []byte)) sparseStats {
	t.Helper()
	count := &countingReader{r: r}
	read := func(p []byte) {
		t.Helper()
		if _, err := io.ReadFull(count, p); err != nil {
			t.Fatal(err)
		}
	}
	var h [28]byte
	read(h[:])
	le := binary.LittleEndian
	if le.Uint32(h[:]) != 0xed26ff3a || le.Uint16(h[4:]) != 1 || le.Uint16(h[6:]) != 0 {
		t.Fatal("bad emitted sparse header")
	}
	fh, ch := int(le.Uint16(h[8:])), int(le.Uint16(h[10:]))
	if fh < 28 || ch < 12 {
		t.Fatalf("bad header sizes %d/%d", fh, ch)
	}
	read(make([]byte, fh-28))
	stats := sparseStats{blockSize: le.Uint32(h[12:]), blocks: le.Uint32(h[16:]), checksum: le.Uint32(h[24:])}
	if stats.blockSize == 0 || stats.blockSize%4 != 0 {
		t.Fatal("invalid block size")
	}
	buf := make([]byte, stats.blockSize)
	var block uint64
	for i, chunks := uint32(0), le.Uint32(h[20:]); i < chunks; i++ {
		header := make([]byte, ch)
		read(header)
		kind, blocks, size := le.Uint16(header), le.Uint32(header[4:]), le.Uint32(header[8:])
		if le.Uint16(header[2:]) != 0 {
			t.Fatal("emitted nonzero reserved field")
		}
		payload := int64(0)
		switch kind {
		case 0xcac1:
			payload = int64(blocks) * int64(stats.blockSize)
			for j := uint32(0); j < blocks; j++ {
				read(buf)
				visit(uint32(block)+j, buf)
				stats.written++
			}
		case 0xcac2:
			payload = 4
			var fill [4]byte
			read(fill[:])
			for j := range buf {
				buf[j] = fill[j%4]
			}
			for j := uint32(0); j < blocks; j++ {
				visit(uint32(block)+j, buf)
				stats.written++
			}
		case 0xcac3:
		case 0xcac4:
			payload = 4
			stats.crcChunks++
			if blocks != 0 {
				t.Fatal("CRC32 emitted blocks")
			}
			read(make([]byte, 4))
		default:
			t.Fatalf("emitted unknown chunk %#x", kind)
		}
		if int64(size) != int64(ch)+payload {
			t.Fatalf("chunk size %d != %d + %d", size, ch, payload)
		}
		block += uint64(blocks)
		if block > uint64(stats.blocks) {
			t.Fatal("emitted too many blocks")
		}
	}
	if block != uint64(stats.blocks) {
		t.Fatalf("emitted %d blocks, want %d", block, stats.blocks)
	}
	var extra [1]byte
	if n, err := count.Read(extra[:]); n != 0 || err != io.EOF {
		t.Fatalf("trailing data: %d, %v", n, err)
	}
	stats.bytes = count.n
	return stats
}

func TestOpenImage(t *testing.T) {
	for _, data := range [][]byte{nil, {1}, {1, 2, 3}, []byte("ordinary raw image")} {
		img := openFixture(t, data)
		if img.Sparse() || img.Size() != int64(len(data)) {
			t.Fatalf("raw: sparse=%v size=%d", img.Sparse(), img.Size())
		}
	}
	data := fixtureSparse(4096, []testChunk{
		{chunkDontCare, 3, nil}, {chunkFill, 2, []byte{1, 2, 3, 4}},
		{chunkRaw, 1, bytes.Repeat([]byte{0xa5}, 4096)}, {chunkCRC32, 0, []byte{5, 6, 7, 8}},
	}, true)
	img := openFixture(t, data)
	if !img.Sparse() || img.Size() != 6*4096 || len(img.sparse.chunks) != 2 {
		t.Fatalf("sparse image: %+v", img)
	}
	if _, err := OpenImage(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing image: %v", err)
	}
	if _, err := OpenImage(t.TempDir()); err == nil {
		t.Fatal("opened a directory")
	}
	bad := filepath.Join(t.TempDir(), "truncated.img")
	if err := os.WriteFile(bad, data[:10], 0o600); err != nil {
		t.Fatal(err)
	}
	var malformed *ImageError
	if _, err := OpenImage(bad); !errors.As(err, &malformed) {
		t.Fatalf("truncated image: %v", err)
	}
}

func TestSparseValidation(t *testing.T) {
	good := fixtureSparse(4, []testChunk{
		{chunkRaw, 2, []byte("abcdefgh")}, {chunkFill, 3, []byte{1, 2, 3, 4}},
		{chunkDontCare, 4, nil}, {chunkCRC32, 0, []byte{5, 6, 7, 8}},
	}, false)
	mutate16 := func(offset int, n uint16) []byte {
		p := bytes.Clone(good)
		binary.LittleEndian.PutUint16(p[offset:], n)
		return p
	}
	mutate32 := func(offset int, n uint32) []byte {
		p := bytes.Clone(good)
		binary.LittleEndian.PutUint32(p[offset:], n)
		return p
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"magic", mutate32(0, 0)}, {"major", mutate16(4, 2)}, {"minor", mutate16(6, 1)},
		{"short file header", mutate16(8, 27)}, {"oversized file header", mutate16(8, 1000)},
		{"short chunk header", mutate16(10, 11)}, {"oversized chunk header", mutate16(10, 1000)},
		{"zero block size", mutate32(12, 0)}, {"unaligned block size", mutate32(12, 3)},
		{"blocks too few", mutate32(16, 8)}, {"blocks too many", mutate32(16, 10)},
		{"chunks too few", mutate32(20, 3)}, {"chunks too many", mutate32(20, 5)},
		{"huge chunk count", mutate32(20, math.MaxUint32)},
		{"unknown chunk", mutate16(28, 0xcaff)}, {"reserved", mutate16(30, 1)},
		{"raw block overflow", mutate32(32, math.MaxUint32)}, {"raw total too small", mutate32(36, 19)}, {"raw total too big", mutate32(36, 21)},
		{"fill total", mutate32(56, 15)}, {"hole total", mutate32(72, 13)}, {"crc blocks", mutate32(80, 1)}, {"crc total", mutate32(84, 15)},
		{"trailing byte", append(bytes.Clone(good), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var malformed *ImageError
			if _, err := parseSparse(bytes.NewReader(tc.data), int64(len(tc.data))); !errors.As(err, &malformed) {
				t.Fatalf("parse: %v", err)
			}
		})
	}
	for n := range len(good) {
		var malformed *ImageError
		if _, err := parseSparse(bytes.NewReader(good[:n]), int64(n)); !errors.As(err, &malformed) {
			t.Fatalf("truncated at %d: %v", n, err)
		}
	}
	huge := fixtureSparse(math.MaxUint32-3, []testChunk{{chunkDontCare, math.MaxUint32, nil}}, false)
	var malformed *ImageError
	if _, err := parseSparse(bytes.NewReader(huge), int64(len(huge))); !errors.As(err, &malformed) {
		t.Fatalf("expanded overflow: %v", err)
	}
	// Size metadata alone must not hide missing headers/values from ReaderAt.
	if _, err := parseSparse(bytes.NewReader(good[:30]), int64(len(good))); !errors.As(err, &malformed) {
		t.Fatalf("truncated reader: %v", err)
	}
	for _, chunks := range [][]testChunk{nil, {{chunkDontCare, 10, nil}}, {{chunkRaw, 0, nil}, {chunkFill, 0, []byte{1, 2, 3, 4}}, {chunkCRC32, 0, make([]byte, 4)}}} {
		data := fixtureSparse(4, chunks, false)
		if _, err := parseSparse(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("empty chunks: %v", err)
		}
	}
}

func checkPieces(t *testing.T, img *Image, original []byte, limit int64) {
	t.Helper()
	pieces, err := img.pieces(t.Context(), limit)
	if err != nil {
		var small *PieceLimitError
		if !errors.As(err, &small) || small.Minimum <= limit || len(pieces) != 0 {
			t.Fatalf("split limit %d: %v", limit, err)
		}
		return
	}
	if len(pieces) == 0 {
		t.Fatal("no pieces")
	}
	blocks, blockSize := uint32(0), uint32(rawBlockSize)
	if img.Sparse() {
		blocks, blockSize = img.sparse.blocks, img.sparse.blockSize
	} else {
		blocks = uint32((len(original) + rawBlockSize - 1) / rawBlockSize)
	}
	expected := bytes.Repeat([]byte{0xa5}, int(blocks)*int(blockSize))
	wantWrites := make([]bool, blocks)
	if img.Sparse() {
		walkSparse(t, bytes.NewReader(original), func(b uint32, p []byte) { copy(expected[int(b)*int(blockSize):], p); wantWrites[b] = true })
	} else {
		clear(expected)
		copy(expected, original)
		for b := range wantWrites {
			wantWrites[b] = true
		}
	}
	actual := bytes.Repeat([]byte{0xa5}, len(expected))
	seen := make([]bool, blocks)
	for i, piece := range pieces {
		if piece.size > limit {
			t.Fatalf("piece %d is %d > %d", i, piece.size, limit)
		}
		stats := walkSparse(t, piece.reader(t.Context()), func(b uint32, p []byte) {
			if b >= blocks || seen[b] {
				t.Fatalf("piece %d writes block %d twice or outside image", i, b)
			}
			seen[b] = true
			copy(actual[int(b)*int(blockSize):], p)
		})
		if stats.bytes != piece.size || stats.blockSize != blockSize || stats.blocks != blocks || stats.crcChunks != 0 || stats.checksum != 0 {
			t.Fatalf("piece %d header/size: %+v, planned %d", i, stats, piece.size)
		}
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("limit %d: reconstructed block device differs", limit)
	}
	for b := range seen {
		if seen[b] != wantWrites[b] {
			t.Fatalf("limit %d: block %d write coverage differs", limit, b)
		}
	}
}

func TestResparseRandomBlockMaps(t *testing.T) {
	rng := rand.New(rand.NewSource(20261021))
	for trial := range 40 {
		blockSize := []uint32{4, 12, 512, 4096}[trial%4]
		var chunks []testChunk
		for j := range 6 + rng.Intn(12) {
			c := testChunk{kind: []uint16{chunkRaw, chunkFill, chunkDontCare, chunkCRC32}[j%4], blocks: uint32(rng.Intn(9))}
			switch c.kind {
			case chunkRaw:
				c.data = make([]byte, int(c.blocks)*int(blockSize))
			case chunkFill:
				c.data = make([]byte, 4)
			case chunkCRC32:
				c.blocks = 0
				c.data = make([]byte, 4)
			}
			rng.Read(c.data)
			chunks = append(chunks, c)
		}
		data := fixtureSparse(blockSize, chunks, trial%2 == 0)
		img := sparseFixture(t, data)
		limits := []int64{28, 39, 40, 52, 55, 56, 64, int64(blockSize) + 40, int64(blockSize) + 52, int64(blockSize) + 64, int64(len(data)), int64(len(data)) + 1}
		for range 12 {
			limits = append(limits, int64(40+rng.Intn(10*int(blockSize)+100)))
		}
		for _, limit := range limits {
			t.Run(fmt.Sprintf("%d/%d", trial, limit), func(t *testing.T) { checkPieces(t, img, data, limit) })
		}
	}
}

func TestRawToSparse(t *testing.T) {
	for _, size := range []int{0, 1, 3, 4095, 4096, 4097, 8192, 3*4096 + 17} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i*37 + 11)
		}
		original := bytes.Clone(data)
		img := openFixture(t, data)
		for _, limit := range []int64{28, 40, 4096 + 40, 4096 + 52, 4096 + 64, 8192 + 64, 200 << 20} {
			t.Run(fmt.Sprintf("%d/%d", size, limit), func(t *testing.T) { checkPieces(t, img, data, limit) })
		}
		if img.Size() != int64(size) || img.Sparse() {
			t.Fatalf("conversion changed source metadata")
		}
		after := make([]byte, size)
		if size > 0 {
			if _, err := img.source.ReadAt(after, 0); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(after, original) {
			t.Fatal("conversion changed source bytes")
		}
	}
}

func TestSparseOnlyHoles(t *testing.T) {
	for _, chunks := range [][]testChunk{nil, {{chunkDontCare, math.MaxUint32, nil}}} {
		data := fixtureSparse(4, chunks, false)
		img := sparseFixture(t, data)
		pieces, err := img.pieces(t.Context(), 40)
		if err != nil || len(pieces) != 1 {
			t.Fatalf("holes-only split: %v", err)
		}
		stats := walkSparse(t, pieces[0].reader(t.Context()), func(uint32, []byte) { t.Fatal("holes-only piece writes data") })
		if stats.blocks != img.sparse.blocks || stats.bytes > 40 || stats.written != 0 {
			t.Fatalf("holes-only: %+v", stats)
		}
	}
}

type generatedSource struct {
	size           int64
	calls, largest int
	read           int64
}

func (s *generatedSource) ReadAt(p []byte, offset int64) (int, error) {
	s.calls++
	s.largest = max(s.largest, len(p))
	if offset < 0 || offset+int64(len(p)) > s.size {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = byte((offset + int64(i)) * 31)
	}
	s.read += int64(len(p))
	return len(p), nil
}

func TestPieceReadersAreLazyAndBounded(t *testing.T) {
	source := &generatedSource{size: 800<<20 + 13}
	img := &Image{source: source, stored: source.size}
	pieces, err := img.pieces(t.Context(), 200<<20)
	if err != nil || len(pieces) != 5 {
		t.Fatalf("plan large raw image: %d pieces, %v", len(pieces), err)
	}
	buf := make([]byte, 64<<10)
	for _, piece := range pieces {
		before := source.calls
		r := piece.reader(t.Context())
		if source.calls != before {
			t.Fatal("constructing reader fetched payload")
		}
		n, err := io.CopyBuffer(io.Discard, r, buf)
		if err != nil || n != piece.size {
			t.Fatalf("streamed %d, planned %d: %v", n, piece.size, err)
		}
	}
	if source.read != source.size || source.largest > len(buf) {
		t.Fatalf("source read %d of %d, largest allocation-facing read %d", source.read, source.size, source.largest)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := img.pieces(ctx, 200<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel plan: %v", err)
	}
	if _, err := pieces[0].reader(ctx).Read(buf); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel stream: %v", err)
	}
}

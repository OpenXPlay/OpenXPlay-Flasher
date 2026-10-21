// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"os"
	"testing"
)

// TestOpenStickRootfs is opt-in: it reads the supplied file and hashes the material block map
// without creating expanded images or writing anything beside the source (or anywhere else).
func TestOpenStickRootfs(t *testing.T) {
	path := os.Getenv("OXP_ROOTFS_IMG")
	if path == "" {
		t.Skip("set OXP_ROOTFS_IMG to verify the real OpenStick rootfs")
	}
	img, err := OpenImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	if !img.Sparse() {
		t.Fatal("OpenStick rootfs is expected to be sparse")
	}
	pieces, err := img.pieces(t.Context(), 209715200)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != 5 {
		t.Fatalf("rootfs split into %d pieces, want 5", len(pieces))
	}
	blockMap := func(h hash.Hash) func(uint32, []byte) {
		return func(block uint32, data []byte) {
			var position [4]byte
			binary.LittleEndian.PutUint32(position[:], block)
			h.Write(position[:])
			h.Write(data)
		}
	}
	original, union := sha256.New(), sha256.New()
	want := walkSparse(t, io.NewSectionReader(img.source, 0, img.stored), blockMap(original))
	aospKiB := []int64{203958, 197981, 204797, 204552, 86909}
	var written uint64
	for i, piece := range pieces {
		if piece.size > 209715200 {
			t.Fatalf("piece %d exceeds 200 MiB: %d", i+1, piece.size)
		}
		got := walkSparse(t, piece.reader(t.Context()), blockMap(union))
		if got.blocks != want.blocks || got.blockSize != want.blockSize || got.bytes != piece.size || got.crcChunks != 0 || got.checksum != 0 {
			t.Fatalf("piece %d: %+v", i+1, got)
		}
		written += got.written
		t.Logf("piece %d: %d bytes (%.3f KiB); AOSP -S 200M: %d KiB", i+1, piece.size, float64(piece.size)/1024, aospKiB[i])
	}
	if written != want.written || !bytes.Equal(original.Sum(nil), union.Sum(nil)) {
		t.Fatal("piece union differs from the original material block map")
	}
	t.Logf("verified %d material blocks, expanded size %d bytes, block-map SHA-256 %x", written, img.Size(), original.Sum(nil))
}

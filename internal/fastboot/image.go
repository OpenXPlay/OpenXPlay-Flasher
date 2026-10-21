// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Image holds one open, seek-independent source and its sparse chunk index. Opening validates
// sparse structure without reading RAW payloads into memory. Close it after the last Flash.
type Image struct {
	source io.ReaderAt
	file   *os.File
	stored int64
	sparse *sparseImage
}

// OpenImage opens path once, recognises Android's sparse magic, and validates any sparse headers
// and payload extents. A non-sparse file is kept raw until a flash requires smaller pieces.
func OpenImage(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("fastboot: open image: %w", err)
	}
	img := &Image{source: f, file: f}
	stat, err := f.Stat()
	if err == nil && !stat.Mode().IsRegular() {
		err = &ImageError{Message: "source must be a regular file"}
	}
	if err == nil {
		img.stored = stat.Size()
		var magic [4]byte
		if img.stored >= 4 {
			_, err = f.ReadAt(magic[:], 0)
			if err == nil && binary.LittleEndian.Uint32(magic[:]) == sparseMagic {
				img.sparse, err = parseSparse(f, img.stored)
			}
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("fastboot: image %s: %w", path, err)
	}
	return img, nil
}

// Size returns expanded bytes for a sparse image, or the original file length for a raw image.
// Padding a raw image's last block during sparse conversion does not change this size.
func (img *Image) Size() int64 {
	if img.sparse != nil {
		return int64(img.sparse.blocks) * int64(img.sparse.blockSize)
	}
	return img.stored
}

// Sparse reports whether the source file was already an Android sparse image.
func (img *Image) Sparse() bool { return img.sparse != nil }

// Close releases the source file. It must not run concurrently with Flash.
func (img *Image) Close() error { return img.file.Close() }

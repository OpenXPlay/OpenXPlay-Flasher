// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

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

// ReadFile bounds reads of indexes, signatures and state before allocating their contents.
func ReadFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(contextReader{ctx, f}, limit)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(b)) > limit {
		return nil, ErrTooLarge
	}
	return b, err
}

// WriteFile replaces a file by rename, without exposing partially written contents.
func WriteFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".catalog-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Hash reads a regular local file with a size cap. It is also used by the maintainer's builder.
func Hash(ctx context.Context, path string) (int64, string, error) {
	return HashProgress(ctx, path, nil)
}

// HashProgress reports bytes hashed and honors cancellation between bounded reads.
func HashProgress(ctx context.Context, path string, progress ProgressFunc) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	if !st.Mode().IsRegular() {
		return 0, "", &InvalidError{"file", "not a regular file"}
	}
	if st.Size() > MaxArtifactSize {
		return 0, "", ErrTooLarge
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, &hashReader{r: f, total: st.Size(), progress: progress}}, MaxArtifactSize+1))
	if n > MaxArtifactSize {
		return 0, "", ErrTooLarge
	}
	return n, hex.EncodeToString(h.Sum(nil)), err
}

// Verify checks a file outside the cache. VerifyContext also permits cancellation during hashing.
func Verify(path string, a Artifact) error { return VerifyContext(context.Background(), path, a) }

// VerifyContext hashes the bytes again; file names and modification times are not trusted.
func VerifyContext(ctx context.Context, path string, a Artifact) error {
	if err := a.Validate(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != a.Size {
		return &IntegrityError{a.Name, "wrong size or not a regular file"}
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, a.Size+1))
	if err != nil {
		return err
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return &IntegrityError{a.Name, "wrong size or SHA-256"}
	}
	return nil
}

type hashReader struct {
	r           io.Reader
	done, total int64
	progress    ProgressFunc
}

func (r *hashReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.done += int64(n)
	if r.progress != nil {
		r.progress(Progress{Done: r.done, Total: r.total})
	}
	return n, err
}

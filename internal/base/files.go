// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package base prepares pinned OpenStick files, protects board-specific backups and executes
// the stock, system and repair procedures. Discovery and transports are injected boundaries.
package base

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/fastboot"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

// ArchiveProvider resolves user-selected archives. Files checks its answer against the profile
// pins as well; a provider cannot substitute another base release.
type ArchiveProvider interface {
	Resolve(context.Context, string, profile.Archive, catalog.ProgressFunc) (string, string, error)
}

type Files struct {
	Dir      string
	Profile  profile.Profile
	Provider ArchiveProvider
	Progress func(string, string, catalog.Progress)
	paths    map[string]string
	author   map[string]AuthorFile
}

// AuthorFile is one of the OpenStick author's own modem files inside base-generic.zip.
type AuthorFile struct {
	Size   int64
	SHA256 string
}

func (f *Files) AuthorHashes() map[string]AuthorFile { return maps.Clone(f.author) }

func verify(ctx context.Context, path string, size int64, sum string) error {
	n, got, err := catalog.Hash(ctx, path)
	if err != nil {
		return err
	}
	if n != size || got != sum {
		return &catalog.IntegrityError{Name: filepath.Base(path), Reason: "wrong size or SHA-256"}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

// copyPinned streams through a bounded hash and commits only complete, synced bytes.
func copyPinned(ctx context.Context, dest string, r io.Reader, size int64, sum string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dest), ".base-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(contextReader{ctx, r}, size+1))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sum {
		return &catalog.IntegrityError{Name: filepath.Base(dest), Reason: "wrong size or SHA-256"}
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(out.Name(), dest)
}

// Prepare hashes every archive and every reused member in this session. Author modem bytes
// are hashed directly from zip streams; they are never extracted to a writable image path.
func (f *Files) Prepare(ctx context.Context) error { return f.prepare(ctx, false) }

// PrepareAuthor needs only base-generic, for offline backup imports and verification.
func (f *Files) PrepareAuthor(ctx context.Context) error { return f.prepare(ctx, true) }

func (f *Files) prepare(ctx context.Context, authorOnly bool) error {
	f.paths, f.author = map[string]string{}, map[string]AuthorFile{}
	if f.Profile.Base == nil || f.Provider == nil {
		return &stage.Problem{Code: "base-files"}
	}
	b := f.Profile.Base
	if err := b.Validate(); err != nil {
		return err
	}
	root := filepath.Join(f.Dir, "base", b.Release)
	for _, role := range slices.Sorted(maps.Keys(b.Archives)) {
		if authorOnly && role != "base-generic" {
			continue
		}
		pin := b.Archives[role]
		path, source, err := f.Provider.Resolve(ctx, role, pin, func(p catalog.Progress) {
			if f.Progress != nil {
				f.Progress(role, "catalog", p)
			}
		})
		if err != nil {
			return err
		}
		if err = verify(ctx, path, pin.Size, pin.SHA256); err != nil {
			return err
		}
		if f.Progress != nil {
			f.Progress(role, source, catalog.Progress{Done: pin.Size, Total: pin.Size})
		}
		var z *zip.ReadCloser
		if role != "boot" {
			z, err = zip.OpenReader(path)
			if err != nil {
				return err
			}
		}
		err = func() error {
			if z != nil {
				defer z.Close()
			}
			members := map[string]*zip.File{}
			if z != nil {
				for _, entry := range z.File {
					if members[entry.Name] != nil {
						return fmt.Errorf("repeated zip member %q", entry.Name)
					}
					members[entry.Name] = entry
				}
			}
			open := func(name string) (io.ReadCloser, error) {
				if name == "" {
					return os.Open(path)
				}
				e := members[name]
				if e == nil || !e.Mode().IsRegular() {
					return nil, fmt.Errorf("missing regular member %s", name)
				}
				return e.Open()
			}
			for _, id := range slices.Sorted(maps.Keys(b.Files)) {
				pin := b.Files[id]
				if authorOnly || pin.Archive != role {
					continue
				}

				if pin.Member == "" {
					f.paths[id] = path
					continue
				}
				dest := filepath.Join(root, id+".img")
				if err := verify(ctx, dest, pin.Size, pin.SHA256); err != nil {
					r, err := open(pin.Member)
					if err != nil {
						return err
					}
					err = copyPinned(ctx, dest, r, pin.Size, pin.SHA256)
					r.Close()
					if err != nil {
						return err
					}
				}
				f.paths[id] = dest
			}
			for name, m := range b.NeverWrite {
				if m.Archive != role {
					continue
				}
				r, err := open(m.Member)
				if err != nil {
					return err
				}
				h := sha256.New()
				n, err := io.Copy(h, io.LimitReader(contextReader{ctx, r}, b.MaxProtected(name)+1))
				r.Close()
				if err != nil {
					return err
				}
				if !slices.Contains(b.Protected[name], n) {
					return fmt.Errorf("author member %s has unexpected size", m.Member)
				}
				f.author[name] = AuthorFile{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
			}
			return nil
		}()
		if err != nil {
			f.paths = nil
			return err
		}
	}
	return nil
}

// Open permits only pinned IDs prepared in this session, with a fresh hash on the same image
// handle Flash will use. Protected/neverWrite members have no ID in this namespace.
func (f *Files) Open(ctx context.Context, id string) (*fastboot.Image, error) {
	if f.Profile.Base == nil || f.paths[id] == "" {
		return nil, &stage.Problem{Code: "unpinned-file", Detail: id}
	}
	pin, ok := f.Profile.Base.Files[id]
	if !ok || protected(id) {
		return nil, &stage.Problem{Code: "unpinned-file", Detail: id}
	}
	img, err := fastboot.OpenImage(f.paths[id])
	if err != nil {
		return nil, err
	}
	if err = img.Verify(ctx, pin.Size, pin.SHA256); err != nil {
		img.Close()
		return nil, err
	}
	return img, nil
}

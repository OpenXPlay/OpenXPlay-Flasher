// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/fastboot"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

const BackupSchema = "org.openxplay.flasher-backup/1"

var ErrBackupConflict = errors.New("base: existing backup differs; the existing backup is preserved and the owner must resolve the difference")

var serialToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var partitionNames = []string{"fsc", "fsg", "modemst1", "modemst2"}

func protected(name string) bool { return slices.Contains(partitionNames, name) }
func ValidSerial(serial string) bool {
	return serialToken.MatchString(serial) && !strings.Contains(serial, "..") && strings.TrimRight(serial, ". ") == serial
}

type Partition struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Source string `json:"source"`
}
type Backup struct {
	Schema         string               `json:"schema"`
	Profile        string               `json:"profile"`
	Serial         string               `json:"serial"`
	Created        time.Time            `json:"created"`
	FlasherVersion string               `json:"flasherVersion"`
	Partitions     map[string]Partition `json:"partitions"`
}

// Store never exposes partition contents as diagnostics. All public descriptions are metadata.
type Store struct {
	Dir     string
	Profile profile.Profile
	Version string
	Author  map[string]string
	Now     func() time.Time
	mu      sync.Mutex
}

func DefaultBackupDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Documents", "OpenXPlay Backups"), nil
}

func (s *Store) path(serial string) (string, error) {
	if !ValidSerial(serial) || !serialToken.MatchString(s.Profile.ID) {
		return "", &stage.Problem{Code: "bad-serial"}
	}
	return filepath.Join(s.Dir, s.Profile.ID+"-"+serial), nil
}

func regular(path string) (*os.File, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filepath.Base(path))
	}
	return os.Open(path)
}

func (s *Store) Show(ctx context.Context, serial string) (Backup, error) {
	dir, err := s.path(serial)
	if err != nil {
		return Backup{}, err
	}
	f, err := regular(filepath.Join(dir, "backup.json"))
	if err != nil {
		return Backup{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, 64<<10))
	if err != nil {
		return Backup{}, err
	}
	var b Backup
	if err = catalog.DecodeStrict(data, &b); err != nil {
		return b, err
	}
	if b.Schema != BackupSchema || b.Profile != s.Profile.ID || b.Serial != serial || b.Created.IsZero() || b.FlasherVersion == "" || len(b.Partitions) != 4 {
		return b, &stage.Problem{Code: "backup-invalid", Detail: serial}
	}
	return b, nil
}

func (s *Store) List(ctx context.Context) ([]Backup, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Backup{}
	for _, e := range entries {
		serial, ok := strings.CutPrefix(e.Name(), s.Profile.ID+"-")
		if !ok || !e.IsDir() {
			continue
		}
		b, err := s.Show(ctx, serial)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *Store) Validate(ctx context.Context, serial string) (Backup, error) {
	b, err := s.Show(ctx, serial)
	if err != nil {
		return b, err
	}
	dir, _ := s.path(serial)
	if err = s.validateFiles(ctx, dir, b); err != nil {
		return b, err
	}
	return b, nil
}

func (s *Store) validateFiles(ctx context.Context, dir string, b Backup) error {
	if s.Profile.Base == nil {
		return &stage.Problem{Code: "backup-invalid"}
	}
	for _, name := range partitionNames {
		pin, ok := b.Partitions[name]
		if !ok || pin.Size != s.Profile.Base.Protected[name] || pin.Source != "oem-dump" && pin.Source != "imported" {
			return &stage.Problem{Code: "backup-invalid", Detail: name}
		}
		got, zero, ff, err := partitionHash(ctx, filepath.Join(dir, name+".bin"), pin.Size)
		if err != nil {
			return err
		}
		if got != pin.SHA256 {
			return &stage.Problem{Code: "backup-checksum", Detail: name}
		}
		if name != "fsc" && (zero || ff) {
			return &stage.Problem{Code: "backup-empty", Detail: name}
		}
		author := s.Author[name]
		if !(name == "fsc" && zero) && (author == "" || got == author) {
			return &stage.Problem{Code: "backup-author", Detail: name}
		}
	}
	return nil
}

func partitionHash(ctx context.Context, path string, size int64) (sum string, zero, ff bool, err error) {
	f, err := regular(path)
	if err != nil {
		return "", false, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false, false, err
	}
	if st.Size() != size {
		return "", false, false, &stage.Problem{Code: "backup-size", Detail: filepath.Base(path)}
	}
	h := sha256.New()
	zero, ff = true, true
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return "", false, false, err
		}
		n, e := f.Read(buf)
		total += int64(n)
		if total > size {
			return "", false, false, &stage.Problem{Code: "backup-size", Detail: filepath.Base(path)}
		}
		for _, v := range buf[:n] {
			zero = zero && v == 0
			ff = ff && v == 0xff
		}
		h.Write(buf[:n])
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", false, false, e
		}
	}
	if total != size {
		return "", false, false, &stage.Problem{Code: "backup-size", Detail: filepath.Base(path)}
	}
	return hex.EncodeToString(h.Sum(nil)), zero, ff, nil
}

type Dump func(context.Context, string, io.Writer) error

// Capture writes each file once to a private staging directory, syncs it, then reads it back.
// Even when a backup exists we capture and compare; a different board dump never replaces it.
func (s *Store) Capture(ctx context.Context, serial, source string, dump Dump) (Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.path(serial)
	if err != nil {
		return Backup{}, err
	}
	if source != "imported" && source != "oem-dump" || s.Profile.Base == nil {
		return Backup{}, &stage.Problem{Code: "backup-invalid"}
	}
	if err = os.MkdirAll(s.Dir, 0o700); err != nil {
		return Backup{}, err
	}
	tmp, err := os.MkdirTemp(s.Dir, ".backup-*")
	if err != nil {
		return Backup{}, err
	}
	defer os.RemoveAll(tmp)
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	b := Backup{Schema: BackupSchema, Profile: s.Profile.ID, Serial: serial, Created: now().UTC(), FlasherVersion: s.Version, Partitions: map[string]Partition{}}
	if b.FlasherVersion == "" {
		b.FlasherVersion = "dev"
	}
	for _, name := range partitionNames {
		path := filepath.Join(tmp, name+".bin")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return b, err
		}
		// Limit an unexpected OEM upload without ever retaining it in memory or in a log.
		w := &boundedWriter{w: f, left: s.Profile.Base.Protected[name]}
		err = dump(ctx, name, w)
		if err == nil && w.left != 0 {
			err = &stage.Problem{Code: "backup-size", Detail: name}
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return b, err
		}
		sum, _, _, err := partitionHash(ctx, path, s.Profile.Base.Protected[name])
		if err != nil {
			return b, err
		}
		b.Partitions[name] = Partition{Size: s.Profile.Base.Protected[name], SHA256: sum, Source: source}
	}
	if err = s.validateFiles(ctx, tmp, b); err != nil {
		return b, err
	}
	if _, err = os.Lstat(dir); err == nil {
		old, err := s.Validate(ctx, serial)
		if err != nil {
			return b, err
		}
		for _, name := range partitionNames {
			if old.Partitions[name].SHA256 != b.Partitions[name].SHA256 {
				return b, ErrBackupConflict
			}
		}
		return old, nil
	} else if !os.IsNotExist(err) {
		return b, err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return b, err
	}
	if err = catalog.WriteFile(ctx, filepath.Join(tmp, "backup.json"), append(data, '\n'), 0o600); err != nil {
		return b, err
	}
	if err = syncDir(tmp); err != nil {
		return b, err
	}
	if err = os.Rename(tmp, dir); err != nil {
		return b, err
	}
	return b, syncDir(s.Dir)
}

type boundedWriter struct {
	w    io.Writer
	left int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		return 0, &stage.Problem{Code: "backup-size"}
	}
	n, err := w.w.Write(p)
	w.left -= int64(n)
	return n, err
}

func (s *Store) Import(ctx context.Context, dir, serial string) (Backup, error) {
	return s.Capture(ctx, serial, "imported", func(ctx context.Context, name string, w io.Writer) error {
		f, err := regular(filepath.Join(dir, name+".bin"))
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, contextReader{ctx, f})
		return err
	})
}

// OpenRestore always validates all four files for the requested serial before opening one.
func (s *Store) OpenRestore(ctx context.Context, serial, name string) (*fastboot.Image, error) {
	if !protected(name) {
		return nil, &stage.Problem{Code: "protected-write", Detail: name}
	}
	b, err := s.Validate(ctx, serial)
	if err != nil {
		return nil, err
	}
	dir, _ := s.path(serial)
	img, err := fastboot.OpenImage(filepath.Join(dir, name+".bin"))
	if err != nil {
		return nil, err
	}
	pin := b.Partitions[name]
	if img.Sparse() {
		img.Close()
		return nil, &stage.Problem{Code: "backup-invalid", Detail: name}
	}
	if err = img.Verify(ctx, pin.Size, pin.SHA256); err != nil {
		img.Close()
		return nil, err
	}
	return img, nil
}

func syncDir(path string) error {
	// Windows does not support fsync on directory handles. Every file has already been synced.
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

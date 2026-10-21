// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

var serialToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var setToken = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z(-[1-9][0-9]*)?$`)
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
	Set            string               `json:"set"`
	Profile        string               `json:"profile"`
	Serial         string               `json:"serial"`
	Created        time.Time            `json:"created"`
	FlasherVersion string               `json:"flasherVersion"`
	Partitions     map[string]Partition `json:"partitions"`
}

type CaptureResult struct {
	Backup
	PreviousSet     string
	MatchedPrevious bool
	Differing       []string
	ComparisonError bool
}

// Store never exposes partition contents as diagnostics. All public descriptions are metadata.
type Store struct {
	Dir     string
	Profile profile.Profile
	Version string
	Author  map[string]AuthorFile
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
	if !ValidSerial(serial) || !ValidSerial(s.Profile.ID) {
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

func (s *Store) setPath(serial, set string) (string, error) {
	dir, err := s.path(serial)
	if err != nil {
		return "", err
	}
	if set == "legacy" {
		return dir, nil
	}
	if !setToken.MatchString(set) {
		return "", &stage.Problem{Code: "backup-invalid", Detail: set}
	}
	return filepath.Join(dir, set), nil
}

// Show reads metadata for exactly one set. Restore policy belongs to the executor.
func (s *Store) Show(ctx context.Context, serial, set string) (Backup, error) {
	dir, err := s.setPath(serial, set)
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
	if set == "legacy" && b.Set == "" {
		b.Set = "legacy"
	}
	if b.Schema != BackupSchema || b.Set != set || b.Profile != s.Profile.ID || b.Serial != serial || b.Created.IsZero() || b.FlasherVersion == "" || len(b.Partitions) != 4 {
		return b, &stage.Problem{Code: "backup-invalid", Detail: set}
	}
	return b, nil
}

func newestFirst(a, b Backup) int {
	if cmp := b.Created.Compare(a.Created); cmp != 0 {
		return cmp
	}
	// A counter resolves captures within the same second, including a fixed test clock.
	if len(a.Set) != len(b.Set) {
		return len(b.Set) - len(a.Set)
	}
	return strings.Compare(b.Set, a.Set)
}

// Sets includes a flat pre-versioning backup as legacy without moving any user files.
func (s *Store) Sets(ctx context.Context, serial string) ([]Backup, error) {
	dir, err := s.path(serial)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Backup{}
	for _, entry := range entries {
		set := entry.Name()
		if set == "backup.json" {
			set = "legacy"
		} else if !entry.IsDir() || !setToken.MatchString(set) {
			continue
		}
		b, err := s.Show(ctx, serial, set)
		if os.IsNotExist(err) && set != "legacy" {
			// An interrupted publication may leave an empty reserved set directory.
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	slices.SortFunc(out, newestFirst)
	return out, nil
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
		sets, err := s.Sets(ctx, serial)
		if err != nil {
			return nil, err
		}
		out = append(out, sets...)
	}
	return out, nil
}

func (s *Store) Validate(ctx context.Context, serial, set string) (Backup, error) {
	b, err := s.Show(ctx, serial, set)
	if err != nil {
		return b, err
	}
	dir, _ := s.setPath(serial, set)
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
		if !ok || !slices.Contains(s.Profile.Base.Protected[name], pin.Size) || pin.Source != "oem-dump" && pin.Source != "imported" {
			return &stage.Problem{Code: "backup-invalid", Detail: name}
		}
		author := s.Author[name]
		// A dump larger than the author's file (OpenStick GPT) still must not start with it.
		prefix := int64(0)
		if author.Size > 0 && author.Size < pin.Size {
			prefix = author.Size
		}
		got, head, zero, ff, err := partitionHash(ctx, filepath.Join(dir, name+".bin"), pin.Size, prefix)
		if err != nil {
			return err
		}
		if got != pin.SHA256 {
			return &stage.Problem{Code: "backup-checksum", Detail: name}
		}
		if name != "fsc" && (zero || ff) {
			return &stage.Problem{Code: "backup-empty", Detail: name}
		}
		if !(name == "fsc" && zero) && (author.SHA256 == "" || got == author.SHA256 || prefix > 0 && head == author.SHA256) {
			return &stage.Problem{Code: "backup-author", Detail: name}
		}
	}
	return nil
}

// partitionHash hashes a whole dump and, when prefix > 0, its first prefix bytes as well.
func partitionHash(ctx context.Context, path string, size, prefix int64) (sum, head string, zero, ff bool, err error) {
	f, err := regular(path)
	if err != nil {
		return "", "", false, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", "", false, false, err
	}
	if st.Size() != size || prefix < 0 || prefix > size {
		return "", "", false, false, &stage.Problem{Code: "backup-size", Detail: filepath.Base(path)}
	}
	h, ph := sha256.New(), sha256.New()
	zero, ff = true, true
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return "", "", false, false, err
		}
		n, e := f.Read(buf)
		if total+int64(n) > size {
			return "", "", false, false, &stage.Problem{Code: "backup-size", Detail: filepath.Base(path)}
		}
		for _, v := range buf[:n] {
			zero = zero && v == 0
			ff = ff && v == 0xff
		}
		h.Write(buf[:n])
		if total < prefix {
			ph.Write(buf[:min(int64(n), prefix-total)])
		}
		total += int64(n)
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", "", false, false, e
		}
	}
	if total != size {
		return "", "", false, false, &stage.Problem{Code: "backup-size", Detail: filepath.Base(path)}
	}
	if prefix > 0 {
		head = hex.EncodeToString(ph.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), head, zero, ff, nil
}

type Dump func(context.Context, string, io.Writer) error

// Capture writes each file once to a private staging directory, syncs it, then reads it back.
// A changed board dump becomes a new immutable set; an import always gets its own set.
func (s *Store) Capture(ctx context.Context, serial, source string, dump Dump) (result CaptureResult, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.path(serial)
	if err != nil {
		return result, err
	}
	if source != "imported" && source != "oem-dump" || s.Profile.Base == nil {
		return result, &stage.Problem{Code: "backup-invalid"}
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return result, err
	}
	tmp, err := os.MkdirTemp(dir, ".backup-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmp)
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	b := &result.Backup
	*b = Backup{Schema: BackupSchema, Profile: s.Profile.ID, Serial: serial, Created: now().UTC(), FlasherVersion: s.Version, Partitions: map[string]Partition{}}
	if b.FlasherVersion == "" {
		b.FlasherVersion = "dev"
	}
	for _, name := range partitionNames {
		path := filepath.Join(tmp, name+".bin")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return result, err
		}
		// Limit an unexpected OEM upload without ever retaining it in memory or in a log.
		max := s.Profile.Base.MaxProtected(name)
		w := &boundedWriter{w: f, left: max}
		err = dump(ctx, name, w)
		size := max - w.left
		if err == nil && !slices.Contains(s.Profile.Base.Protected[name], size) {
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
			return result, err
		}
		sum, _, _, _, err := partitionHash(ctx, path, size, 0)
		if err != nil {
			return result, err
		}
		b.Partitions[name] = Partition{Size: size, SHA256: sum, Source: source}
	}
	if err = s.validateFiles(ctx, tmp, *b); err != nil {
		return result, err
	}
	sets, listErr := s.Sets(ctx, serial)
	// Damaged older metadata cannot make a valid new safety copy unusable.
	result.ComparisonError = listErr != nil
	if listErr == nil && len(sets) > 0 {
		old := sets[0]
		result.PreviousSet = old.Set
		for _, name := range partitionNames {
			if old.Partitions[name].SHA256 != b.Partitions[name].SHA256 {
				result.Differing = append(result.Differing, name)
			}
		}
		result.MatchedPrevious = len(result.Differing) == 0
		if result.MatchedPrevious && source != "imported" {
			if _, err := s.Validate(ctx, serial, old.Set); err == nil {
				result.Backup = old
				return result, nil
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	// A separate reservation keeps publication atomic on Windows as well as Unix. No existing
	// set directory is ever replaced, including an empty directory left by another application.
	for counter := 0; ; counter++ {
		b.Set = b.Created.Format("20060102T150405Z")
		if counter > 0 {
			b.Set += fmt.Sprintf("-%d", counter)
		}
		if _, err = os.Lstat(filepath.Join(dir, b.Set)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return result, err
		}
		reservation := filepath.Join(dir, ".reserve-"+b.Set)
		err = os.Mkdir(reservation, 0o700)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return result, err
		}
		defer os.Remove(reservation)
		break
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return result, err
	}
	if err = catalog.WriteFile(ctx, filepath.Join(tmp, "backup.json"), append(data, '\n'), 0o600); err != nil {
		return result, err
	}
	if err = syncDir(tmp); err != nil {
		return result, err
	}
	if err = os.Rename(tmp, filepath.Join(dir, b.Set)); err != nil {
		return result, err
	}
	if err = syncDir(dir); err != nil {
		return result, err
	}
	return result, syncDir(s.Dir)
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
	result, err := s.Capture(ctx, serial, "imported", func(ctx context.Context, name string, w io.Writer) error {
		f, err := regular(filepath.Join(dir, name+".bin"))
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, contextReader{ctx, f})
		return err
	})
	return result.Backup, err
}

// OpenRestore always validates all four files for the requested serial before opening one.
func (s *Store) OpenRestore(ctx context.Context, serial, set, name string) (*fastboot.Image, error) {
	if !protected(name) {
		return nil, &stage.Problem{Code: "protected-write", Detail: name}
	}
	b, err := s.Validate(ctx, serial, set)
	if err != nil {
		return nil, err
	}
	dir, _ := s.setPath(serial, set)
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

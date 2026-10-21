// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package stage validates layer packages, plans installations and runs them over adb. A package
// is independent of its source: user-selected files use the same checked payload.
package stage

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
)

const Schema = "org.openxplay.flash-stage/1"

// Limits bound memory and decompression work even for a malformed local file.
const (
	MaxPackage  = 128 << 20
	maxUnpacked = 512 << 20
	maxMetadata = 1 << 20
)

var ErrPackage = errors.New("invalid layer package")

type Requirements struct {
	Stages           []string `json:"stages,omitempty"`
	KernelRelease    string   `json:"kernelRelease,omitempty"`
	DeviceTreeModel  string   `json:"deviceTreeModel,omitempty"`
	DebianVersion    string   `json:"debianVersion"`
	DPKGArchitecture string   `json:"dpkgArchitecture"`
	FreeKiB          int64    `json:"freeKiB,omitempty"`
	Packages         []string `json:"packages,omitempty"`
}

type Deb struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
}

type Success struct {
	Preflight  string `json:"preflight,omitempty"`
	Install    string `json:"install"`
	Verify     string `json:"verify,omitempty"`
	RolledBack string `json:"rolledBack,omitempty"`
}

type Manifest struct {
	Schema         string       `json:"schema"`
	Stage          string       `json:"stage"`
	Device         string       `json:"device,omitempty"`
	Name           string       `json:"name"`
	Version        string       `json:"version"`
	ReleaseVersion string       `json:"releaseVersion,omitempty"`
	Writes         string       `json:"writes"`
	Transport      string       `json:"transport"`
	Requires       Requirements `json:"requires"`
	DeviceDir      string       `json:"deviceDir"`
	File           string       `json:"file,omitempty"`
	SHA256         string       `json:"sha256,omitempty"`
	Preflight      string       `json:"preflight,omitempty"`
	FirstInstall   string       `json:"firstInstall,omitempty"`
	Install        string       `json:"install"`
	Verify         string       `json:"verify"`
	Reboot         string       `json:"reboot"`
	Rollback       string       `json:"rollback"`
	Success        Success      `json:"success"`
	Packages       []Deb        `json:"packages,omitempty"`
}

// Package keeps one copy of the validated push payload. Its metadata is returned by value.
type Package struct {
	manifest Manifest
	filename string
	digest   string
	data     []byte
	sources  []LocalFile
}

type PackageInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Layer   string `json:"layer"`
	Device  string `json:"device,omitempty"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Checked bool   `json:"checked"`
}

func (p *Package) Info() PackageInfo {
	m := p.manifest
	v := m.ReleaseVersion
	if v == "" {
		v = m.Version
	}
	return PackageInfo{p.digest, m.Name, m.Stage, m.Device, v, int64(len(p.data)), p.digest, true}
}

func (p *Package) Manifest() Manifest {
	m := p.manifest
	m.Packages = append([]Deb(nil), m.Packages...)
	m.Requires.Stages = append([]string(nil), m.Requires.Stages...)
	m.Requires.Packages = append([]string(nil), m.Requires.Packages...)
	return m
}

// Open recognises a device bundle or either member of a relay deb/manifest pair.
func Open(filename string) (*Package, error) {
	var p *Package
	var err error
	switch {
	case strings.HasSuffix(filename, ".tar.gz"):
		var data []byte
		data, err = readPackage(filename, MaxPackage)
		if err == nil {
			p, err = DevicePackage(data)
		}
	case strings.HasSuffix(filename, ".deb"), strings.HasSuffix(filename, ".manifest.json"):
		p, err = openRelay(filename)

	default:
		err = errors.New("choose a .tar.gz, .deb or .manifest.json file")
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPackage, err)
	}
	if len(p.sources) == 0 {
		p.sources = []LocalFile{{Path: filename, Size: int64(len(p.data)), SHA256: p.digest}}
	}
	return p, nil
}

// openRelay binds sidecar and Debian payload by digest. GitHub release filenames use a hyphen
// where Debian uses a tilde; neither a picked payload nor its sidecar must keep a particular name.
func openRelay(filename string) (*Package, error) {
	dir := filepath.Dir(filename)
	manifests := []string{filename}
	var picked []byte
	if strings.HasSuffix(filename, ".deb") {
		var err error
		picked, err = readPackage(filename, MaxPackage)
		if err != nil {
			return nil, err
		}
		manifests, _ = filepath.Glob(filepath.Join(dir, "*.manifest.json"))
	}
	for _, manifestPath := range manifests {
		metadata, err := readPackage(manifestPath, maxMetadata)
		if err != nil {
			continue
		}
		m, err := parseManifest(metadata)
		if err != nil || m.Stage != "relay" {
			continue
		}
		if picked != nil {
			if p, err := RelayPackage(metadata, picked); err == nil {
				p.sources = []LocalFile{{Path: manifestPath, Size: int64(len(metadata)), SHA256: digest(metadata)}, {Path: filename, Size: int64(len(picked)), SHA256: digest(picked)}}
				return p, nil
			}
			continue
		}
		candidates := []string{filepath.Join(dir, m.File), filepath.Join(dir, strings.ReplaceAll(m.File, "~", "-"))}
		others, _ := filepath.Glob(filepath.Join(dir, "*.deb"))
		candidates = append(candidates, others...)
		for _, candidate := range candidates {
			data, err := readPackage(candidate, MaxPackage)
			if err != nil {
				continue
			}
			if p, err := RelayPackage(metadata, data); err == nil {
				p.sources = []LocalFile{{Path: manifestPath, Size: int64(len(metadata)), SHA256: digest(metadata)}, {Path: candidate, Size: int64(len(data)), SHA256: digest(data)}}
				return p, nil
			}
		}
	}
	return nil, errors.New("no relay payload and manifest with matching SHA-256")
}

func readPackage(filename string, limit int64) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("package must be a regular file within the size limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("package exceeds the size limit")
	}
	return b, err
}

var token = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~:-]*$`)
var debName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)

func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size
}

func safeRelative(s string) bool {
	if s == "" || strings.ContainsAny(s, "\\\x00\r\n") || path.IsAbs(s) || path.Clean(s) != s {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." || identity(part) {
			return false
		}
	}
	return true
}

func identity(s string) bool {
	s = strings.ToLower(s)
	return s == "identity.pk8" || s == "certificate.p7b" || strings.HasSuffix(s, ".p12") || strings.HasSuffix(s, ".pfx") || strings.HasPrefix(s, "offline-mfi")
}

func parseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if len(data) > maxMetadata {
		return m, errors.New("manifest too large")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Schema != Schema || (m.Stage != "device" && m.Stage != "relay") {
		return m, errors.New("unsupported schema or stage")
	}
	// Early relay sidecars omit the transport and Debian version. The relay format has only
	// an adb file-install path; an omitted OS version adds no restriction of its own.
	if m.Stage == "relay" && m.Transport == "" {
		m.Transport = "adb"
	}
	if !token.MatchString(m.Name) || !validVersion(m.Version) || m.Writes != "files" || (m.Transport != "adb" && m.Transport != "ssh") || m.Install == "" || m.Verify == "" || m.Rollback == "" {
		return m, errors.New("missing or invalid manifest fields")
	}
	if !strings.HasPrefix(m.DeviceDir, "/var/cache/openxplay/") || !safeRelative(strings.TrimPrefix(m.DeviceDir, "/")) {
		return m, errors.New("deviceDir must be a clean path under /var/cache/openxplay")
	}
	r := m.Requires
	if (m.Stage == "device" && r.DebianVersion == "") || r.DPKGArchitecture == "" || r.FreeKiB < 0 {
		return m, errors.New("invalid requirements")
	}
	for _, name := range r.Packages {
		if !debName.MatchString(name) {
			return m, errors.New("invalid required package name")
		}
	}
	for _, id := range r.Stages {
		if !token.MatchString(id) {
			return m, errors.New("invalid required stage")
		}
	}
	if m.Success.Install != "OXP-STAGE "+m.Stage+" ok" {
		return m, errors.New("invalid install success marker")
	}
	if m.Stage == "relay" {
		if m.Name != "openxplay-relay" || !safeRelative(m.File) || path.Base(m.File) != m.File || !strings.HasSuffix(m.File, ".deb") || !validDigest(m.SHA256) || m.FirstInstall == "" || !token.MatchString(m.ReleaseVersion) || m.Reboot != "none" || m.Success.RolledBack != "OXP-STAGE relay rolled-back" {
			return m, errors.New("missing or invalid relay fields")
		}
	} else {
		if !token.MatchString(m.Device) || m.Preflight == "" || r.DeviceTreeModel == "" || m.Reboot != "required" || len(m.Packages) == 0 || m.Success.Preflight != "OXP-STAGE device preflight-ok" || m.Success.Verify != "OXP-STAGE device verified" {
			return m, errors.New("missing or invalid device fields")
		}
		seen := map[string]bool{}
		marker := false
		for _, d := range m.Packages {
			if !debName.MatchString(d.Name) || !validVersion(d.Version) || !safeRelative(d.File) || !strings.HasPrefix(d.File, "debs/") || !strings.HasSuffix(d.File, ".deb") || !validDigest(d.SHA256) || seen[d.File] || seen[d.Name] {
				return m, errors.New("invalid Debian package entry")
			}
			seen[d.File], seen[d.Name] = true, true
			marker = marker || (d.Name == "openxplay-"+m.Device && d.Version == m.Version)
		}
		if !marker {
			return m, errors.New("device package does not contain its installed-layer marker")
		}
	}
	return m, nil
}

// RelayPackage checks the payload against its sidecar. Callers transfer ownership of data.
func RelayPackage(metadata, data []byte) (*Package, error) {
	m, err := parseManifest(metadata)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPackage, err)
	}
	if m.Stage != "relay" || len(data) == 0 || len(data) > MaxPackage || !strings.EqualFold(digest(data), m.SHA256) {
		return nil, fmt.Errorf("%w: relay checksum mismatch", ErrPackage)
	}
	return &Package{manifest: m, filename: m.File, digest: digest(data), data: data}, nil
}

// DevicePackage hashes regular entries without keeping their unpacked bytes. Nothing is extracted
// on the computer, and the original checked archive is the only payload kept for upload.
func DevicePackage(data []byte) (*Package, error) {
	p, err := devicePackage(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPackage, err)
	}
	return p, nil
}

func devicePackage(data []byte) (*Package, error) {
	if len(data) > MaxPackage {
		return nil, errors.New("package too large")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: maxUnpacked + 1}
	tr := tar.NewReader(limited)
	files, entries := map[string]string{}, map[string]bool{}
	var metadata, sums []byte
	top := ""
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if count > 10000 || !safeRelative(name) || entries[name] {
			return nil, fmt.Errorf("unsafe or repeated tar entry %q", h.Name)
		}
		entries[name] = true
		parts := strings.SplitN(name, "/", 2)
		if top == "" {
			top = parts[0]
		}
		if parts[0] != top || (len(parts) == 1 && h.Typeflag != tar.TypeDir) {
			return nil, errors.New("tar entries must share one top directory")
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("tar entry %q is not a regular file or directory", name)
		}
		for key := range h.PAXRecords {
			if strings.Contains(strings.ToLower(key), "sparse") {
				return nil, errors.New("sparse entries are not allowed")
			}
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Size < 0 || h.Size > maxUnpacked {
			return nil, errors.New("tar entry too large")
		}
		rel := parts[1]
		if rel == "manifest.json" || rel == "SHA256SUMS" {
			if h.Size > maxMetadata {
				return nil, errors.New("bundle metadata too large")
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			files[rel] = digest(b)
			if rel == "manifest.json" {
				metadata = b
			} else {
				sums = b
			}
		} else {
			hash := sha256.New()
			if _, err := io.Copy(hash, tr); err != nil {
				return nil, err
			}
			files[rel] = hex.EncodeToString(hash.Sum(nil))
		}
	}
	// Drain gzip to check its checksum and reject a second archive hidden after tar's end.
	tail, err := io.ReadAll(io.LimitReader(limited, 1<<20))
	if err != nil {
		return nil, err
	}
	if limited.N <= 0 || len(tail) == 1<<20 || len(bytes.Trim(tail, "\x00")) != 0 {
		return nil, errors.New("excess or trailing archive data")
	}
	m, err := parseManifest(metadata)
	if err != nil {
		return nil, err
	}
	if m.Stage != "device" || m.Name != top || files["install-device.sh"] == "" || files["SOURCES.txt"] == "" {
		return nil, errors.New("incomplete device bundle or directory name mismatch")
	}
	checked := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 67 || !validDigest(line[:64]) || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
			return nil, errors.New("invalid SHA256SUMS entry")
		}
		name := line[66:]
		if !safeRelative(name) || checked[name] || !strings.EqualFold(files[name], line[:64]) {
			return nil, fmt.Errorf("SHA256SUMS mismatch: %s", name)
		}
		checked[name] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for name := range files {
		if name != "SHA256SUMS" && !checked[name] {
			return nil, fmt.Errorf("file missing from SHA256SUMS: %s", name)
		}
	}
	for _, d := range m.Packages {
		if !strings.EqualFold(files[d.File], d.SHA256) {
			return nil, fmt.Errorf("package checksum mismatch: %s", d.File)
		}
	}
	return &Package{manifest: m, filename: m.Name + ".tar.gz", digest: digest(data), data: data}, nil
}

// LocalFile binds a validated package to the files chosen by the user, without copying them.
type LocalFile struct {
	Path   string
	Size   int64
	SHA256 string
}

func (p *Package) SetLocalFiles(files []LocalFile) { p.sources = append([]LocalFile(nil), files...) }
func (p *Package) LocalFiles() []LocalFile         { return append([]LocalFile(nil), p.sources...) }

// Recheck detects changes to a payload or its manifest immediately before use.
func (p *Package) Recheck(ctx context.Context) error {
	for _, f := range p.sources {
		n, sum, err := catalog.Hash(ctx, f.Path)
		if err != nil || n != f.Size || sum != f.SHA256 {
			return &Problem{Code: "file-changed", Detail: filepath.Base(f.Path)}
		}
	}
	return nil
}

// RelayManifest validates the existing manifest format without requiring its payload yet.
func RelayManifest(data []byte) (Manifest, error) {
	m, err := parseManifest(data)
	if err == nil && m.Stage != "relay" {
		err = ErrPackage
	}
	return m, err
}

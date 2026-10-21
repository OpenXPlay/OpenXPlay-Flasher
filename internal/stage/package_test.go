// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

func deviceManifest() Manifest {
	m := Manifest{Schema: Schema, Stage: "device", Device: "ufi003", Name: "openxplay-ufi003-device-0.1.0", Version: "0.1.0",
		Writes: "files", Transport: "adb", Requires: Requirements{Stages: []string{"base"}, KernelRelease: "5.15.0-handsomekernel+",
			DeviceTreeModel: "Handsome Openstick", DebianVersion: "11", DPKGArchitecture: "arm64", FreeKiB: 65536},
		DeviceDir: "/var/cache/openxplay/stages/device", Preflight: "sh install-device.sh --dry-run .", Install: "sh install-device.sh .",
		Verify: "sh install-device.sh --verify .", Reboot: "required", Rollback: "install the previous bundle",
		Success: Success{Preflight: "OXP-STAGE device preflight-ok", Install: "OXP-STAGE device ok", Verify: "OXP-STAGE device verified"}}
	for _, name := range []string{"bluez", "iw", "openxplay-ufi003"} {
		m.Packages = append(m.Packages, Deb{Name: name, Version: "0.1.0", File: "debs/" + name + ".deb", SHA256: digest([]byte(name))})
	}
	return m
}

func relayManifest() Manifest {
	return Manifest{Schema: Schema, Stage: "relay", Name: "openxplay-relay", Version: "0.1.5~beta.1", ReleaseVersion: "0.1.5-beta.1", Writes: "files", Transport: "adb",
		Requires: Requirements{DebianVersion: "11", DPKGArchitecture: "arm64", Packages: []string{"bluez", "network-manager", "iw"}},
		File:     "openxplay-relay_0.1.5~beta.1_arm64.deb", SHA256: digest([]byte("relay deb")), DeviceDir: "/var/cache/openxplay/relay",
		FirstInstall: "dpkg -i /var/cache/openxplay/relay/openxplay-relay_0.1.5~beta.1_arm64.deb", Install: "openxplay-relay-update --sha256 " + digest([]byte("relay deb")) + " /var/cache/openxplay/relay/openxplay-relay_0.1.5~beta.1_arm64.deb",
		Verify: "systemctl is-active openxplay-relay.service", Rollback: "openxplay-relay-update --rollback", Reboot: "none",
		Success: Success{Install: "OXP-STAGE relay ok", RolledBack: "OXP-STAGE relay rolled-back"}}
}

func jsonBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bundleBytes(t *testing.T, m Manifest, change func(map[string][]byte), extra ...*tar.Header) []byte {
	t.Helper()
	files := map[string][]byte{"manifest.json": jsonBytes(t, m), "install-device.sh": []byte("#!/bin/sh\nexit 0\n"), "SOURCES.txt": []byte("test fixture\n")}
	for _, p := range m.Packages {
		files[p.File] = []byte(p.Name)
	}
	if change != nil {
		change(files)
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var sums strings.Builder
	for _, name := range names {
		fmt.Fprintf(&sums, "%s  %s\n", digest(files[name]), name)
	}
	if files["SHA256SUMS"] == nil {
		files["SHA256SUMS"] = []byte(sums.String())
		names = append(names, "SHA256SUMS")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: m.Name + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		b := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: m.Name + "/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(b))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range extra {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkedPackages(t *testing.T) (*Package, *Package) {
	t.Helper()
	d, err := DevicePackage(bundleBytes(t, deviceManifest(), nil))
	if err != nil {
		t.Fatal(err)
	}
	r, err := RelayPackage(jsonBytes(t, relayManifest()), []byte("relay deb"))
	if err != nil {
		t.Fatal(err)
	}
	return d, r
}

func builtinProfile(t *testing.T) profile.Profile {
	t.Helper()
	ps, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	return ps[0]
}

func TestDeviceBundleValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest func(*Manifest)
		files    func(map[string][]byte)
		header   *tar.Header
		good     bool
	}{
		{name: "good", good: true},
		{name: "bad schema", manifest: func(m *Manifest) { m.Schema = "v2" }},
		{name: "bad digest", manifest: func(m *Manifest) { m.Packages[0].SHA256 = "xyz" }},
		{name: "mismatched deb digest", manifest: func(m *Manifest) { m.Packages[0].SHA256 = strings.Repeat("a", 64) }},
		{name: "manifest path escape", manifest: func(m *Manifest) { m.Packages[0].File = "../outside.deb" }},
		{name: "missing command", manifest: func(m *Manifest) { m.Preflight = "" }},
		{name: "wrong marker", manifest: func(m *Manifest) { m.Success.Install = "OXP-STAGE relay ok" }},
		{name: "unsafe cache", manifest: func(m *Manifest) { m.DeviceDir = "/tmp/stage" }},
		{name: "identity", files: func(f map[string][]byte) { f["identity.pk8"] = []byte("fixture") }},
		{name: "identity nested", files: func(f map[string][]byte) { f["nested/CERTIFICATE.P7B"] = []byte("fixture") }},
		{name: "p12", files: func(f map[string][]byte) { f["nested/test.p12"] = []byte("fixture") }},
		{name: "pfx", files: func(f map[string][]byte) { f["test.pfx"] = []byte("fixture") }},
		{name: "offline identity", files: func(f map[string][]byte) { f["offline-mfi-test"] = []byte("fixture") }},
		{name: "tar path escape", header: &tar.Header{Name: "openxplay-ufi003-device-0.1.0/../escape", Typeflag: tar.TypeReg}},
		{name: "absolute path", header: &tar.Header{Name: "/absolute", Typeflag: tar.TypeReg}},
		{name: "second top", header: &tar.Header{Name: "other/file", Typeflag: tar.TypeReg}},
		{name: "symlink", header: &tar.Header{Name: "openxplay-ufi003-device-0.1.0/link", Typeflag: tar.TypeSymlink, Linkname: "install-device.sh"}},
		{name: "hard link", header: &tar.Header{Name: "openxplay-ufi003-device-0.1.0/link", Typeflag: tar.TypeLink, Linkname: "install-device.sh"}},
		{name: "device file", header: &tar.Header{Name: "openxplay-ufi003-device-0.1.0/dev", Typeflag: tar.TypeChar}},
		{name: "bad sums", files: func(f map[string][]byte) { f["SHA256SUMS"] = []byte("bad\n") }},
		{name: "sums escape", files: func(f map[string][]byte) { f["SHA256SUMS"] = []byte(strings.Repeat("a", 64) + "  ../escape\n") }},
		{name: "incomplete sums", files: func(f map[string][]byte) { f["SHA256SUMS"] = []byte("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := deviceManifest()
			if tc.manifest != nil {
				tc.manifest(&m)
			}
			var extra []*tar.Header
			if tc.header != nil {
				extra = append(extra, tc.header)
			}
			p, err := DevicePackage(bundleBytes(t, m, tc.files, extra...))
			if tc.good {
				if err != nil || !p.Info().Checked || p.Info().Size == 0 {
					t.Fatalf("package=%v, error=%v", p, err)
				}
			} else if !errors.Is(err, ErrPackage) {
				t.Fatalf("accepted bad bundle: %v", err)
			}
		})
	}
}

func TestRelayPackageAndLocalPairs(t *testing.T) {
	m := relayManifest()
	dir := t.TempDir()
	deb := filepath.Join(dir, m.File)
	manifest := strings.TrimSuffix(deb, ".deb") + ".manifest.json"
	if err := os.WriteFile(deb, []byte("relay deb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, jsonBytes(t, m), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{deb, manifest} {
		p, err := Open(filename)
		if err != nil || p.Info().Version != m.ReleaseVersion || p.Manifest().Version != m.Version {
			t.Fatalf("Open(%s)=%v %v", filename, p, err)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*Manifest)
	}{
		{"schema", func(m *Manifest) { m.Schema = "bad" }},
		{"digest", func(m *Manifest) { m.SHA256 = "not hex" }},
		{"checksum", func(m *Manifest) { m.SHA256 = strings.Repeat("f", 64) }},
		{"file escape", func(m *Manifest) { m.File = "../relay.deb" }},
		{"first install", func(m *Manifest) { m.FirstInstall = "" }},
		{"release version", func(m *Manifest) { m.ReleaseVersion = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := m
			tc.change(&bad)
			if _, err := RelayPackage(jsonBytes(t, bad), []byte("relay deb")); !errors.Is(err, ErrPackage) {
				t.Fatalf("bad package: %v", err)
			}
		})
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(deb); !errors.Is(err, ErrPackage) {
		t.Fatalf("missing sidecar: %v", err)
	}
	if _, err := Open(filepath.Join(dir, "foo.txt")); !errors.Is(err, ErrPackage) {
		t.Fatalf("unknown format: %v", err)
	}
}

func TestManifestMetadataIsCopied(t *testing.T) {
	d, _ := checkedPackages(t)
	m := d.Manifest()
	m.Packages[0].File = "changed"
	m.Requires.Stages[0] = "changed"
	if d.Manifest().Packages[0].File == "changed" || d.Manifest().Requires.Stages[0] == "changed" {
		t.Fatal("manifest exposes mutable internal slices")
	}
}

func TestEarlyRelaySidecar(t *testing.T) {
	m := relayManifest()
	m.Transport, m.Requires.DebianVersion = "", ""
	p, err := RelayPackage(jsonBytes(t, m), []byte("relay deb"))
	if err != nil || p.Manifest().Transport != "adb" || p.Manifest().Requires.DebianVersion != "" {
		t.Fatalf("early relay sidecar: %v %v", p, err)
	}
	m.Transport = "fastboot"
	if _, err := RelayPackage(jsonBytes(t, m), []byte("relay deb")); err == nil {
		t.Fatal("relay sidecar accepted another transport")
	}
}

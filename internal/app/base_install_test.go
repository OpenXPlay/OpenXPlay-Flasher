// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/fastboot"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func testHash(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func testJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func testPut(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

type catalogStub struct {
	index   *catalog.Index
	offline bool
	err     error
	calls   int
}

func (s *catalogStub) Fetch(context.Context, string) (*catalog.Index, error) {
	s.calls++
	return s.index, s.err
}
func (s *catalogStub) LoadCached(context.Context, string) (*catalog.Index, error) {
	return s.index, s.err
}
func (s *catalogStub) Offline() bool { return s.offline }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type baseTestDetector struct {
	mu         sync.Mutex
	candidates []device.Candidate
}

func (d *baseTestDetector) Scan(context.Context) device.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return device.Snapshot{Candidates: slices.Clone(d.candidates)}
}
func (d *baseTestDetector) set(c ...device.Candidate) { d.mu.Lock(); d.candidates = c; d.mu.Unlock() }
func testCandidate(mode profile.Mode, address int, supported bool) device.Candidate {
	pid := uint16(0xd00d)
	if mode == profile.ModeADB {
		pid = 0xd001
	}
	profiles := []string{}
	if supported {
		profiles = append(profiles, "ufi003")
	}
	return device.Candidate{Mode: mode, Profiles: profiles, USB: usb.DeviceInfo{VendorID: 0x18d1, ProductID: pid, Bus: 1, Address: address, Path: []int{address}, Serial: "board1"}}
}

type baseTestDevice struct {
	serial                      string
	onClock                     func()
	pushes                      int
	det                         *baseTestDetector
	stock                       bool
	wrongModel                  bool
	bootID                      int
	deviceVersion, relayVersion string
	push                        []byte
	opened                      []string
}

func (d *baseTestDevice) Serial() string { return "board1" }
func (d *baseTestDevice) Shell(_ context.Context, cmd string) (adb.Result, error) {
	out := ""
	switch {
	case cmd == "cat /proc/cmdline":
		serial := d.serial
		if serial == "" {
			serial = "board1"
		}
		out = "androidboot.serialno=" + serial
	case strings.HasPrefix(cmd, "getprop "):
		key := strings.TrimPrefix(cmd, "getprop ")
		out = "msm8916_32_512"
		if key == "ro.build.version.release" {
			out = "4.4.4"
		}
		if key == "ro.serialno" {
			out = "board1"
		}
		if d.wrongModel {
			out = "phone"
		}
	case cmd == "reboot bootloader":
		d.det.set(testCandidate(profile.ModeFastboot, 1, true))
	case strings.Contains(cmd, "uname -r"):
		out = "root\t0\nkernel\t5.15.0-handsomekernel+\nmodel\tHandsome Openstick\ndebian\t11\narch\tarm64\n"
	case strings.HasPrefix(cmd, "dpkg-query"):
		version := ""
		if strings.Contains(cmd, "'openxplay-ufi003'") {
			version = d.deviceVersion
		}
		if strings.Contains(cmd, "'openxplay-relay'") {
			version = d.relayVersion
		}
		if version == "" {
			return adb.Result{ExitCode: 1}, nil
		}
		out = "installed\t" + version
	case strings.Contains(cmd, "df -Pk"):
		out = "root 4000000 100000 3900000 1% /"
	case cmd == "df -h /":
		out = "root 4G 1G 3G /"
	case cmd == "systemctl --failed --no-pager":
		out = "0 loaded units listed."
	case cmd == "cat /proc/sys/kernel/random/boot_id":
		out = fmt.Sprint(d.bootID)
	case cmd == "systemctl --no-block reboot":
		d.bootID++
	case strings.HasPrefix(cmd, "sha256sum "):
		out = testHash(d.push) + "  payload\n"
	case strings.Contains(cmd, "device-preflight"):
		out = "OXP-STAGE device preflight-ok"
	case strings.Contains(cmd, "device-verify"):
		out = "OXP-STAGE device verified"
	case strings.Contains(cmd, "device-install"):
		d.deviceVersion = "1.0"
		out = "OXP-STAGE device ok"
	case strings.Contains(cmd, "relay-first"), strings.Contains(cmd, "relay-install"):
		d.relayVersion = "1.0~beta.1"
		out = "OXP-STAGE relay ok"
	case strings.Contains(cmd, "relay-verify"):
		out = "active"
	case strings.HasPrefix(cmd, "date -u -s"):
		if d.onClock != nil {
			d.onClock()
		}
	case strings.HasPrefix(cmd, "umask 077; mkdir"), strings.Contains(cmd, "tar -xzmf"):
	default:
		return adb.Result{}, fmt.Errorf("unexpected test shell %s", cmd)
	}
	return adb.Result{Stdout: []byte(out)}, nil
}
func (d *baseTestDevice) Push(_ context.Context, _ string, b []byte, _ fs.FileMode) error {
	d.pushes++
	d.push = b
	return nil
}
func (d *baseTestDevice) Pull(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("unexpected pull")
}
func (d *baseTestDevice) Close() error { return nil }

type baseTestConnector struct{ d *baseTestDevice }

func (c baseTestConnector) Connect(_ context.Context, dev usb.DeviceInfo) (adb.Device, error) {
	c.d.opened = append(c.d.opened, dev.Location())
	return c.d, nil
}

type baseTestFB struct {
	boot                  bool
	det                   *baseTestDetector
	product, serial, dump string
	calls                 []string
}

func (f *baseTestFB) GetVar(_ context.Context, name string) (string, error) {
	if name == "product" {
		return f.product, nil
	}
	if name == "serialno" {
		return f.serial, nil
	}
	return "", nil
}
func (f *baseTestFB) Flash(ctx context.Context, name string, _ *fastboot.Image, opts fastboot.FlashOptions, _ fastboot.Progress) error {
	if opts.BeforeWrite != nil {
		if err := opts.BeforeWrite(ctx); err != nil {
			return err
		}
	}
	if name == "boot" {
		f.boot = true
	}
	f.calls = append(f.calls, "flash:"+name)
	return nil
}
func (f *baseTestFB) Erase(_ context.Context, name string) error {
	if name == "boot" {
		f.boot = false
	}
	f.calls = append(f.calls, "erase:"+name)
	return nil
}
func (f *baseTestFB) OEM(_ context.Context, args string) (fastboot.Response, error) {
	f.dump = strings.TrimPrefix(args, "dump ")
	f.calls = append(f.calls, "oem "+args)
	return fastboot.Response{}, nil
}
func (f *baseTestFB) Upload(_ context.Context, w io.Writer, _ fastboot.Progress) (int64, error) {
	v := byte(99)
	if f.dump == "fsc" {
		v = 0
	}
	n, err := w.Write(bytes.Repeat([]byte{v}, 64))
	return int64(n), err
}
func (f *baseTestFB) Reboot(context.Context) error {
	f.calls = append(f.calls, "reboot")
	if f.boot {
		f.det.set(testCandidate(profile.ModeADB, 1, true))
	} else {
		f.product = "LK1ST_MSM8916"
		c := testCandidate(profile.ModeFastboot, 1, true)
		old := f.det.Scan(context.Background())
		if len(old.Candidates) > 0 {
			c.USB.Address = old.Candidates[0].USB.Address + 1
		}
		f.det.set(c)
	}
	return nil
}
func (f *baseTestFB) RebootBootloader(context.Context) error {
	f.calls = append(f.calls, "reboot-bootloader")
	snap := f.det.Scan(context.Background())
	c := snap.Candidates[0]
	c.USB.Address++
	f.det.set(c)
	return nil
}
func (f *baseTestFB) Close() error { return nil }

type testPower struct{ acquired, released int }

func (p *testPower) Acquire() (func(), error) { p.acquired++; return func() { p.released++ }, nil }

func baseServiceFixture(t *testing.T) (*Service, *baseTestDevice, *baseTestFB, *catalogStub, map[string][]byte) {
	t.Helper()
	ps, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	p := ps[0]
	b := p.Base
	bodies := map[string]map[string][]byte{"base-generic": {}, "rootfs-zip": {}}
	payloads := map[string][]byte{}
	for id, pin := range b.Files {
		data := []byte("small-image-" + id)
		pin.Size = int64(len(data))
		pin.SHA256 = testHash(data)
		b.Files[id] = pin
		if pin.Member != "" {
			bodies[pin.Archive][pin.Member] = data
		} else {
			a := b.Archives[pin.Archive]
			a.Size = pin.Size
			a.SHA256 = pin.SHA256
			b.Archives[pin.Archive] = a
			payloads[a.File] = data
		}
	}
	for _, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
		b.Protected[name] = []int64{64}
		bodies["base-generic"][b.NeverWrite[name].Member] = bytes.Repeat([]byte{44}, 64)
	}
	for role, entries := range bodies {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for name, data := range entries {
			w, e := z.Create(name)
			if e != nil {
				t.Fatal(e)
			}
			w.Write(data)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		a := b.Archives[role]
		a.Size = int64(buf.Len())
		a.SHA256 = testHash(buf.Bytes())
		b.Archives[role] = a
		payloads[a.File] = buf.Bytes()
	}
	dm := stage.Manifest{Schema: stage.Schema, Stage: "device", Device: p.ID, Name: "openxplay-ufi003-device-1.0", Version: "1.0", Writes: "files", Transport: "adb", Requires: stage.Requirements{Stages: []string{"base"}, KernelRelease: b.Linux.KernelRelease, DeviceTreeModel: b.Linux.DeviceTreeModel, DebianVersion: "11", DPKGArchitecture: "arm64"}, DeviceDir: "/var/cache/openxplay/device", Preflight: "device-preflight", Install: "device-install", Verify: "device-verify", Reboot: "required", Rollback: "previous bundle", Success: stage.Success{Preflight: "OXP-STAGE device preflight-ok", Install: "OXP-STAGE device ok", Verify: "OXP-STAGE device verified"}, Packages: []stage.Deb{{Name: "openxplay-ufi003", Version: "1.0", File: "debs/marker.deb", SHA256: testHash([]byte("device"))}}}
	files := map[string][]byte{"manifest.json": testJSON(t, dm), "install-device.sh": []byte("fixture"), "SOURCES.txt": []byte("fixture"), "debs/marker.deb": []byte("device")}
	var sums strings.Builder
	for _, name := range sortedKeys(files) {
		fmt.Fprintf(&sums, "%s  %s\n", testHash(files[name]), name)
	}
	files["SHA256SUMS"] = []byte(sums.String())
	var bundle bytes.Buffer
	gz := gzip.NewWriter(&bundle)
	tarw := tar.NewWriter(gz)
	for _, name := range sortedKeys(files) {
		data := files[name]
		if err = tarw.WriteHeader(&tar.Header{Name: dm.Name + "/" + name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		tarw.Write(data)
	}
	tarw.Close()
	gz.Close()
	payloads[dm.Name+".tar.gz"] = bundle.Bytes()
	relay := []byte("relay")
	rm := stage.Manifest{Schema: stage.Schema, Stage: "relay", Name: "openxplay-relay", Version: "1.0~beta.1", ReleaseVersion: "1.0-beta.1", Writes: "files", Transport: "adb", Requires: stage.Requirements{Stages: []string{"base", "device"}, DebianVersion: "11", DPKGArchitecture: "arm64"}, File: "relay.deb", SHA256: testHash(relay), DeviceDir: "/var/cache/openxplay/relay", FirstInstall: "relay-first", Install: "relay-install", Verify: "relay-verify", Reboot: "none", Rollback: "rollback", Success: stage.Success{Install: "OXP-STAGE relay ok", RolledBack: "OXP-STAGE relay rolled-back"}}
	payloads["relay.deb"] = relay
	payloads["relay.manifest.json"] = testJSON(t, rm)
	art := func(role, name string) catalog.Artifact {
		return catalog.Artifact{Role: role, Name: name, Size: int64(len(payloads[name])), SHA256: testHash(payloads[name]), URLs: []string{"https://fixture.invalid/" + name}}
	}
	layers := []catalog.Layer{{Layer: "base", Version: b.Release, Source: b.Source}, {Layer: "device", Version: "1.0", Source: "https://fixture.invalid/device", Artifacts: []catalog.Artifact{art("bundle", dm.Name+".tar.gz")}}, {Layer: "relay", Version: "1.0~beta.1", Source: "https://fixture.invalid/relay", Artifacts: []catalog.Artifact{art("deb", "relay.deb"), art("manifest", "relay.manifest.json")}}}
	for role, pin := range b.Archives {
		layers[0].Artifacts = append(layers[0].Artifacts, art(role, pin.File))
	}
	index := &catalog.Index{Schema: catalog.Schema, Channel: "beta", Sequence: 1, Generated: "2026-10-21T00:00:00Z", Devices: map[string]catalog.Device{p.ID: {Recommended: map[string]string{"base": b.Release, "device": "1.0", "relay": "1.0~beta.1"}, Layers: layers}}}
	if err = index.Validate(); err != nil {
		t.Fatal(err)
	}
	source := &catalogStub{index: index}
	det := &baseTestDetector{}
	det.set(testCandidate(profile.ModeFastboot, 1, true))
	d := &baseTestDevice{det: det}
	fb := &baseTestFB{det: det, serial: "board1", product: b.Fastboot.Product}
	s := New([]profile.Profile{p}, det, discardEvents{})
	s.SetADB(baseTestConnector{d: d})
	s.SetFastboot(func(context.Context, usb.DeviceInfo) (base.Fastboot, error) { return fb, nil }, true)
	dir := t.TempDir()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, ok := payloads[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			return nil, errors.New("unexpected fixture URL")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
	s.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source, catalog.NewCache(filepath.Join(dir, "cache"), nil, client))
	t.Cleanup(s.Stop)
	return s, d, fb, source, payloads
}

func TestCatalogEveryLayerAndFullBaseJob(t *testing.T) {
	s, _, fb, source, _ := baseServiceFixture(t)
	ctx := t.Context()
	if state, err := s.CatalogFetch(ctx, "beta"); err != nil || state.Status != "fresh" {
		t.Fatal(state, err)
	}
	if got := s.Install(ctx); got.Suggested != "repair" || len(got.Choices) != 7 {
		t.Fatal("missing fastboot choices")
	}
	if _, err := s.InstallPrepare(ctx, "full-reinstall"); err != nil {
		t.Fatal(err)
	}
	state := s.InstallSnapshot()
	var selected stage.Choice
	for _, c := range state.Choices {
		if c.ID == "full-reinstall" {
			selected = c
		}
	}
	if !selected.Ready || len(selected.Needs) != 3 {
		t.Fatalf("unprepared full choice %+v", selected)
	}
	for _, serial := range []string{"", "other-board"} {
		if _, err := s.InstallStart(ctx, stage.Request{Choice: "full-reinstall", YesErase: serial}); err == nil {
			t.Fatal("missing serial confirmation accepted")
		}
	}
	if len(fb.calls) != 0 {
		t.Fatal("unconfirmed request mutated device")
	}
	power := &testPower{}
	s.SetSleepInhibitor(power)
	if _, err := s.InstallStart(ctx, stage.Request{Choice: "full-reinstall", YesErase: "board1"}); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := s.InstallWait(wait)
	if err != nil || state.Job.Status != "succeeded" || state.Job.Installed.Layers["device"] != "1.0" || state.Job.Installed.Layers["relay"] != "1.0~beta.1" {
		t.Fatalf("job=%+v err=%v", state.Job, err)
	}
	if power.acquired != 1 || power.released != 1 || s.InstallRunning() {
		t.Fatal("full job lost inhibitor")
	}
	if slices.Contains(fb.calls, "flash:partition") {
		t.Fatal("system plan changed bootloader chain")
	}
	journal, err := (base.Journals{Dir: s.dataDir}).Load(ctx, "ufi003", "board1")
	if err != nil || !journal.Finished || !slices.Contains(journal.Completed, "relay/install") || len(journal.CompletedAt) != len(journal.Completed) {
		t.Fatalf("journal=%+v %v", journal, err)
	}
	source.offline = true
	if state, err := s.CatalogFetch(ctx, "beta"); err != nil || state.Status != "cached/offline" {
		t.Fatal(state, err)
	}
	source.err = errors.New("invalid signature")
	if state, err := s.CatalogFetch(ctx, "beta"); err == nil || state.Status != "failed" || state.Reason == "" {
		t.Fatal(state, err)
	}
}

func TestUnknownADBRequiresDesignationAndMismatchIsForgotten(t *testing.T) {
	s, d, fb, _, _ := baseServiceFixture(t)
	unknown := testCandidate(profile.ModeADB, 2, false)
	unknown.USB.VendorID = 0x1111
	unknown.USB.ProductID = 0x2222
	d.det.set(unknown)
	state := s.Install(t.Context())
	if state.Target != nil || len(d.opened) != 0 {
		t.Fatal("automatically opened unknown adb")
	}
	if _, err := s.DesignateStock(t.Context(), unknown.USB.Location()); err != nil {
		t.Fatal(err)
	}
	state = s.InstallSnapshot()
	if state.Identity == nil || state.Identity.Mode != profile.ModeStock || state.Suggested != "stock" {
		t.Fatalf("designation=%+v", state)
	}
	if _, err := s.InstallStart(t.Context(), stage.Request{Choice: "stock"}); err == nil {
		t.Fatal("stock erasure allowed without serial")
	}
	if len(fb.calls) != 0 {
		t.Fatal("unconfirmed stock install wrote")
	}
	d.wrongModel = true
	if _, err := s.DesignateStock(t.Context(), unknown.USB.Location()); err == nil {
		t.Fatal("phone accepted as stock")
	}
	opened := len(d.opened)
	s.Install(t.Context())
	if len(d.opened) != opened {
		t.Fatal("failed designation was retained")
	}
}

func TestUnsupportedFastbootChoicesHaveBilingualReason(t *testing.T) {
	s, d, _, _, _ := baseServiceFixture(t)
	d.det.set(testCandidate(profile.ModeADB, 1, true))
	s.SetFastboot(nil, false)
	state := s.Install(t.Context())
	for _, c := range state.Choices {
		if !c.Destructive {
			continue
		}
		if c.Ready || c.Problem.Code != "fastboot-unsupported" || c.Reason["en"] == "" || c.Reason["zh-CN"] == "" {
			t.Fatalf("unsupported choice %+v", c)
		}
	}
}

func TestRepairPlusLayersIsOneExplicitJob(t *testing.T) {
	s, _, fb, _, _ := baseServiceFixture(t)
	ctx := t.Context()
	if _, err := s.CatalogFetch(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	s.Install(ctx)
	if _, err := s.InstallPrepare(ctx, "full-reinstall"); err != nil {
		t.Fatal(err)
	}
	req := stage.Request{Choice: "full-reinstall", BasePlan: "repair", YesErase: "board1"}
	preview, err := s.InstallPlan(req)
	if err != nil || preview.BasePlan != "repair" || !slices.ContainsFunc(preview.Steps, func(s stage.Step) bool { return s.ID == "chain-partition" }) {
		t.Fatal("repair scope was not previewed", err)
	}
	if _, err = s.InstallStart(ctx, req); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := s.InstallWait(wait)
	if err != nil || state.Job.Status != "succeeded" || !slices.Contains(fb.calls, "flash:partition") || state.Job.Installed.Layers["relay"] != "1.0~beta.1" {
		t.Fatalf("repair plus layers: %+v %v", state.Job, err)
	}
	j, err := (base.Journals{Dir: s.dataDir}).Load(ctx, "ufi003", "board1")
	if err != nil || j.Plan != "repair" || !j.Finished {
		t.Fatal(j, err)
	}
}

func TestOfflineImportsAndLocalArtifactProvenance(t *testing.T) {
	s, d, _, source, payloads := baseServiceFixture(t)
	source.index = nil
	source.err = errors.New("offline")
	if _, err := s.CatalogFetch(t.Context(), "beta"); err == nil {
		t.Fatal("offline catalog succeeded")
	}
	dir := t.TempDir()
	for _, pin := range s.profiles[0].Base.Archives {
		path := filepath.Join(dir, "renamed-"+pin.File)
		testPut(t, path, payloads[pin.File])
		if _, err := s.ImportFile(t.Context(), path); err != nil {
			t.Fatal(err)
		}
	}
	var bundle string
	for name, data := range payloads {
		if strings.HasSuffix(name, ".tar.gz") {
			bundle = filepath.Join(dir, name)
			testPut(t, bundle, data)
		}
	}
	if _, err := s.InstallAdd(bundle); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "picked.manifest.json")
	testPut(t, manifest, payloads["relay.manifest.json"])
	testPut(t, filepath.Join(dir, "release-name.deb"), payloads["relay.deb"])
	if _, err := s.InstallAdd(manifest); err != nil {
		t.Fatal(err)
	}
	d.det.set(testCandidate(profile.ModeADB, 1, true))
	s.Install(t.Context())
	if _, err := s.InstallPrepare(t.Context(), "full-reinstall"); err != nil {
		t.Fatal(err)
	}
	choice, err := s.InstallPlan(stage.Request{Choice: "full-reinstall"})
	if err != nil || !choice.Ready {
		t.Fatalf("offline preparation %+v %v", choice, err)
	}
	for _, need := range choice.Needs {
		if len(need.Artifacts) == 0 {
			t.Fatal("missing provenance", need.Layer)
		}
		for _, a := range need.Artifacts {
			want := "local"
			if need.Layer == "base" {
				want = "imported"
			}
			if !a.Verified || a.Source != want {
				t.Fatalf("artifact provenance %+v", a)
			}
		}
	}
	if _, err := s.DownloadLayer(t.Context(), "ufi003", "base", "openstick-v2"); err == nil {
		t.Fatal("download silently substituted another base release")
	}
}

func TestLayerHandoffRetainsOriginalBoardIdentity(t *testing.T) {
	s, d, _, _, _ := baseServiceFixture(t)
	ctx := t.Context()
	if _, err := s.CatalogFetch(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	s.Install(ctx)
	if _, err := s.InstallPrepare(ctx, "full-reinstall"); err != nil {
		t.Fatal(err)
	}
	d.onClock = func() { d.serial = "another-board" }
	if _, err := s.InstallStart(ctx, stage.Request{Choice: "full-reinstall", YesErase: "board1"}); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := s.InstallWait(wait)
	if err != nil || state.Job.Status != "failed" || state.Job.Problem.Code != "serial-mismatch" || d.pushes != 0 || state.Job.Observed {
		t.Fatalf("handoff lost serial: %+v %v", state.Job, err)
	}
}

func TestReadOnlyFastbootFactsAndResumeOffer(t *testing.T) {
	s, d, fb, _, _ := baseServiceFixture(t)
	phone := testCandidate(profile.ModeADB, 2, false)
	phone.USB.VendorID = 0x1111
	phone.USB.ProductID = 0x2222
	d.det.set(testCandidate(profile.ModeFastboot, 1, true), phone)
	snap := s.Rescan(t.Context())
	if snap.Candidates[0].Product != "LK1ST_MSM8916" || snap.Candidates[0].Serialno != "board1" || len(d.opened) != 0 || len(fb.calls) != 0 {
		t.Fatalf("read-only discovery %+v", snap)
	}
	if value, err := s.FastbootGetVar(t.Context(), "serialno"); err != nil || value != "board1" {
		t.Fatal(value, err)
	}
	d.det.set(testCandidate(profile.ModeADB, 1, true))
	journal := base.Journal{Schema: base.JournalSchema, Profile: "ufi003", Serial: "board1", Plan: "stock", Choice: "stock", Started: time.Now(), Updated: time.Now(), Completed: []string{"bootstrap-aboot"}}
	if err := (base.Journals{Dir: s.dataDir}).Save(t.Context(), journal); err != nil {
		t.Fatal(err)
	}
	state := s.Install(t.Context())
	if state.Suggested != "repair" || !slices.ContainsFunc(state.Choices, func(c stage.Choice) bool { return c.ID == "repair" }) {
		t.Fatal("unfinished journal did not offer repair")
	}
	journal.Finished = true
	if err := (base.Journals{Dir: s.dataDir}).Save(t.Context(), journal); err != nil {
		t.Fatal(err)
	}
	state = s.Install(t.Context())
	if slices.ContainsFunc(state.Choices, func(c stage.Choice) bool { return c.ID == "repair" }) {
		t.Fatal("completed Linux job offered resume")
	}
}

func TestBackupOperationImportsAndListsSets(t *testing.T) {
	s, d, fb, source, _ := baseServiceFixture(t)
	if _, err := s.CatalogFetch(t.Context(), "beta"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for i, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
		testPut(t, filepath.Join(dir, name+".bin"), bytes.Repeat([]byte{byte(70 + i)}, 64))
	}
	var imported []base.Backup
	for range 2 {
		result, err := s.BackupOperation(t.Context(), "import", "board1", dir)
		if err != nil {
			t.Fatal(err)
		}
		b := result.(base.Backup)
		if b.Set == "" || b.Partitions["modemst1"].Source != "imported" {
			t.Fatal("import lost set or provenance", b)
		}
		imported = append(imported, b)
	}
	if imported[0].Set == imported[1].Set {
		t.Fatal("identical imports must add separate sets")
	}
	j := base.Journal{Schema: base.JournalSchema, Profile: "ufi003", Serial: "board1", Plan: "stock", Started: time.Now().Add(-time.Hour), Updated: time.Now(), CapturedSet: imported[0].Set, InFlight: "chain-rpm"}
	if err := (base.Journals{Dir: s.dataDir}).Save(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	// A newer pre-chain attempt cannot change the pending repair's source.
	j.Started = j.Started.Add(time.Minute)
	j.InFlight, j.CapturedSet = "backup", imported[1].Set
	if err := (base.Journals{Dir: s.dataDir}).Save(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	source.err = errors.New("offline")
	for _, operation := range []string{"list", "show", "verify"} {
		result, err := s.BackupOperation(t.Context(), operation, "board1", "")
		if err != nil {
			t.Fatal(operation, err)
		}
		var listing BackupListing
		if operation == "list" {
			all := result.([]BackupListing)
			if len(all) != 1 {
				t.Fatal("expected one serial", all)
			}
			listing = all[0]
		} else {
			listing = result.(BackupListing)
		}
		if listing.RepairSet != imported[0].Set || listing.RepairSource != "previous-job" || len(listing.Sets) != 2 || listing.Sets[0].Set != imported[1].Set {
			t.Fatal("wrong listing or repair source", listing)
		}
		for _, set := range listing.Sets {
			for _, part := range set.Partitions {
				if part.Size != 64 || len(part.ShortSHA256) != 12 || part.ShortSHA256 != part.SHA256[:12] || part.Source != "imported" {
					t.Fatal("incomplete partition summary", part)
				}
			}
		}
	}
	if len(fb.calls) != 0 || len(d.opened) != 0 {
		t.Fatal("backup metadata and imports accessed a device")
	}
}

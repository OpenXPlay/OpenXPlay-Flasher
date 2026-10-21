// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type installDevice struct {
	mu               sync.Mutex
	version          string
	data             []byte
	pushes           int
	started          chan struct{}
	release          chan struct{}
	onClock          func()
	deviceMissing    bool
	noBase           bool
	failInstall      bool
	failInspect      bool
	failPackageFacts bool
	readbackStarted  chan struct{}
	readbackRelease  chan struct{}
}

func (d *installDevice) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	switch {
	case cmd == "cat /proc/cmdline":
		return adb.Result{Stdout: []byte("androidboot.serialno=fixture-board")}, nil
	case strings.Contains(cmd, "uname -r"):
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.failInspect {
			if d.readbackStarted != nil {
				close(d.readbackStarted)
				d.readbackStarted = nil
				select {
				case <-d.readbackRelease:
				case <-ctx.Done():
				}
			}
			return adb.Result{}, errors.New("fake device unavailable")
		}
		out := "root\t0\nkernel\t5.15.0-handsomekernel+\nmodel\tHandsome Openstick\ndebian\t11\narch\tarm64\n"
		if d.noBase {
			out = strings.ReplaceAll(out, "5.15.0-handsomekernel+", "stock-kernel")
		}
		return adb.Result{Stdout: []byte(out)}, nil
	case strings.HasPrefix(cmd, "dpkg-query "):
		version := "1.0"
		if strings.HasSuffix(cmd, "'openxplay-ufi003'") && d.deviceMissing {
			version = ""
		}
		if strings.HasSuffix(cmd, "'openxplay-relay'") {
			d.mu.Lock()
			version = d.version
			d.mu.Unlock()
		}
		if version == "" {
			return adb.Result{ExitCode: 1}, nil
		}
		return adb.Result{Stdout: []byte("installed\t" + version)}, nil
	case strings.Contains(cmd, "df -Pk"):
		if d.failPackageFacts && strings.Contains(cmd, "/var/cache/openxplay/relay") {
			return adb.Result{}, errors.New("fake package cache unavailable")
		}
		return adb.Result{Stdout: []byte("Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/root 1000000 200000 800000 20% /\n")}, nil
	case strings.HasPrefix(cmd, "date -u -s"):
		if d.onClock != nil {
			d.onClock()
		}
	case strings.HasPrefix(cmd, "sha256sum "):
		d.mu.Lock()
		sum := sha256.Sum256(d.data)
		d.mu.Unlock()
		return adb.Result{Stdout: []byte(hex.EncodeToString(sum[:]) + "  relay.deb\n")}, nil
	case strings.Contains(cmd, "openxplay-relay-update --sha256"), strings.Contains(cmd, "dpkg -i "):
		if d.started != nil {
			close(d.started)
			select {
			case <-d.release:
			case <-ctx.Done():
				return adb.Result{}, ctx.Err()
			}
		}
		d.mu.Lock()
		if d.failInstall {
			d.failInspect = true
			d.mu.Unlock()
			return adb.Result{}, errors.New("fake interrupted install")
		}
		d.version = "0.1.5~beta.1"
		d.mu.Unlock()
		return adb.Result{Stdout: []byte("OXP-STAGE relay ok version=0.1.5~beta.1\n")}, nil
	case strings.Contains(cmd, "systemctl is-active"):
		return adb.Result{Stdout: []byte("active\n")}, nil
	case strings.HasPrefix(cmd, "umask 077; mkdir -p"):
	default:
		return adb.Result{}, errors.New("unexpected fake command: " + cmd)
	}
	return adb.Result{}, nil
}

func (d *installDevice) Push(_ context.Context, _ string, data []byte, mode fs.FileMode) error {
	if mode != 0o644 {
		return errors.New("wrong push mode")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.data = data
	d.pushes++
	return nil
}
func (d *installDevice) Pull(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("unexpected pull")
}
func (d *installDevice) Close() error { return nil }

type packageNative struct {
	*fakeNative
	paths []string
	saved []byte
	layer string
}

func (n *packageNative) OpenPackages(layer string) ([]string, error) {
	n.layer = layer
	return n.paths, nil
}
func (n *packageNative) SaveFile(_ string, b []byte) (string, error) {
	n.saved = b
	return "saved.log", nil
}

func relayFiles(t *testing.T) (deb, manifest string) {
	t.Helper()
	dir := t.TempDir()
	data := []byte("small relay fixture")
	sum := sha256.Sum256(data)
	m := stage.Manifest{Schema: stage.Schema, Stage: "relay", Name: "openxplay-relay", Version: "0.1.5~beta.1", ReleaseVersion: "0.1.5-beta.1", Writes: "files", Transport: "adb",
		File: "openxplay-relay_0.1.5~beta.1_arm64.deb", SHA256: hex.EncodeToString(sum[:]), DeviceDir: "/var/cache/openxplay/relay",
		Requires:     stage.Requirements{DebianVersion: "11", DPKGArchitecture: "arm64", Packages: []string{"bluez", "network-manager", "iw"}},
		FirstInstall: "dpkg -i /var/cache/openxplay/relay/openxplay-relay_0.1.5~beta.1_arm64.deb",
		Install:      "openxplay-relay-update --sha256 " + hex.EncodeToString(sum[:]) + " /var/cache/openxplay/relay/openxplay-relay_0.1.5~beta.1_arm64.deb",
		Verify:       "systemctl is-active openxplay-relay.service", Reboot: "none", Rollback: "openxplay-relay-update --rollback", Success: stage.Success{Install: "OXP-STAGE relay ok", RolledBack: "OXP-STAGE relay rolled-back"}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	deb = filepath.Join(dir, m.File)
	manifest = strings.TrimSuffix(deb, ".deb") + ".manifest.json"
	if err := os.WriteFile(deb, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return
}

func installFixture(t *testing.T, withADB bool) (fixture, *app.Service, *usb.FakeBus, *installDevice, *packageNative) {
	t.Helper()
	bus := usb.NewFakeBus(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 1, Address: 1, Path: []int{1}})
	hub := NewHub()
	svc, err := app.NewForBus(bus, hub)
	if err != nil {
		t.Fatal(err)
	}
	dev := &installDevice{version: "0.1.3"}
	if withADB {
		svc.SetADB(oneDevice{dev})
	}
	native := &packageNative{fakeNative: &fakeNative{}}
	settings := NewSettingsStore(t.TempDir())
	h := Handler(Deps{Service: svc, Hub: hub, Settings: settings, Native: native})
	t.Cleanup(svc.Stop)
	return fixture{handler: h, hub: hub, settings: settings, native: native.fakeNative}, svc, bus, dev, native
}

func bodyPath(path string) string {
	b, _ := json.Marshal(map[string]string{"path": path})
	return string(b)
}

func installState(t *testing.T, f fixture) app.InstallState {
	t.Helper()
	rec := f.do("GET", "/api/install", "")
	var state app.InstallState
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &state) != nil {
		t.Fatalf("install state=%d %s", rec.Code, rec.Body)
	}
	return state
}

func TestInstallPackageAndJobRoutes(t *testing.T) {
	f, svc, _, _, native := installFixture(t, true)
	deb, manifest := relayFiles(t)
	for _, path := range []string{deb, manifest} {
		if rec := f.do("POST", "/api/install/packages", bodyPath(path)); rec.Code != http.StatusOK {
			t.Fatalf("load=%d %s", rec.Code, rec.Body)
		}
	}
	state := installState(t, f)
	if state.Problem != nil || state.Target.Profile != "ufi003" || state.Installed.Layers["base"] == "" || len(state.Packages) != 1 || state.Packages[0].Version != "0.1.5-beta.1" {
		t.Fatalf("state=%+v", state)
	}
	if state.Choices[0].Problem != nil || state.Choices[0].Ready || state.Choices[1].Problem != nil || state.Choices[1].Needs[0].Status != "missing" || !state.Choices[2].Ready {
		t.Fatalf("choices=%v", state.Choices)
	}
	rec := f.do("POST", "/api/install/start", `{"choice":"relay","downgrade":false}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start=%d %s", rec.Code, rec.Body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	state, err := svc.InstallWait(ctx)
	if err != nil || state.Job.Status != "succeeded" || state.Job.Installed.Layers["relay"] != "0.1.5~beta.1" {
		t.Fatalf("job=%+v err=%v", state.Job, err)
	}
	if pushed := f.hub.State().Install; pushed == nil || pushed.Job.Status != "succeeded" {
		t.Fatal("job did not reach the hub")
	}
	rec = f.do("GET", "/api/install/log", "")
	if rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(rec.Body.String(), "OXP-STAGE relay ok") {
		t.Fatalf("log=%s", rec.Body)
	}
	if rec := f.do("POST", "/api/install/log/save", `{}`); rec.Code != http.StatusOK || !strings.Contains(string(native.saved), "OXP-STAGE relay ok") {
		t.Fatalf("save=%d %s", rec.Code, rec.Body)
	}
	id := state.Packages[0].ID
	if rec := f.do("DELETE", "/api/install/packages/"+id, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("remove=%d %s", rec.Code, rec.Body)
	}
	if len(installState(t, f).Packages) != 0 {
		t.Fatal("package not removed")
	}
	native.paths = []string{manifest, deb}
	if rec := f.do("POST", "/api/install/pick", `{"layer":"relay"}`); rec.Code != http.StatusOK {
		t.Fatalf("picker=%d %s", rec.Code, rec.Body)
	}
	if len(installState(t, f).Packages) != 1 {
		t.Fatal("picker did not deduplicate the pair")
	}
}

func TestInstallRefusalsAndDowngrade(t *testing.T) {
	f, _, bus, dev, _ := installFixture(t, true)
	deb, _ := relayFiles(t)
	if rec := f.do("POST", "/api/install/packages", bodyPath(deb+".missing")); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "file-read" {
		t.Fatalf("bad package=%d %s", rec.Code, rec.Body)
	}
	f.do("POST", "/api/install/packages", bodyPath(deb))
	dev.version = "0.2.0"
	state := installState(t, f)
	if !state.Choices[2].Downgrade {
		t.Fatal("downgrade warning missing")
	}
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "downgrade" {
		t.Fatalf("downgrade=%d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/install/start", `{"choice":"base"}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "typed-serial" {
		t.Fatalf("base=%d %s", rec.Code, rec.Body)
	}
	bus.Set(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Address: 1}, usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Address: 2})
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay","downgrade":true}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "several-devices" {
		t.Fatalf("several=%d %s", rec.Code, rec.Body)
	}
	fNoADB, _, _, _, _ := installFixture(t, false)
	if state := installState(t, fNoADB); state.Problem == nil || state.Problem.Code != "no-adb" {
		t.Fatalf("no adb=%+v", state)
	}
}

func TestInstallSharesConfigurationLock(t *testing.T) {
	f, svc, _, dev, _ := installFixture(t, true)
	deb, _ := relayFiles(t)
	dev.started, dev.release = make(chan struct{}), make(chan struct{})
	defer close(dev.release)
	f.do("POST", "/api/install/packages", bodyPath(deb))
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("start=%d %s", rec.Code, rec.Body)
	}
	select {
	case <-dev.started:
	case <-time.After(2 * time.Second):
		t.Fatal("install did not start")
	}
	for _, route := range []struct{ method, path, body string }{
		{"POST", "/api/install/start", `{"choice":"relay"}`},
		{"POST", "/api/install/packages", bodyPath(deb)},
		{"DELETE", "/api/install/packages/anything", `{}`},
		{"POST", "/api/config/read", `{"path":"/etc/hostname"}`},
		{"POST", "/api/config/write", `{"path":"/etc/hostname","content":"test"}`},
	} {
		rec := f.do(route.method, route.path, route.body)
		if rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "busy" {
			t.Fatalf("%s=%d %s", route.path, rec.Code, rec.Body)
		}
	}
	if state := installState(t, f); state.Job.Status != "running" {
		t.Fatal("running state was lost")
	}
	_ = svc
}

func TestInstallRescansBeforeEachWrite(t *testing.T) {
	f, svc, bus, dev, _ := installFixture(t, true)
	deb, _ := relayFiles(t)
	dev.onClock = func() { bus.Set() }
	f.do("POST", "/api/install/packages", bodyPath(deb))
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("start=%d %s", rec.Code, rec.Body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	state, err := svc.InstallWait(ctx)
	if err != nil || state.Job.Status != "failed" || state.Job.Problem.Code != "no-device" || state.Job.Steps[state.Job.Failed].Action != "prepare" || dev.pushes != 0 {
		t.Fatalf("job=%+v err=%v", state.Job, err)
	}
}

func TestInstallMutationsNeedJSON(t *testing.T) {
	f, _, _, _, _ := installFixture(t, true)
	for _, route := range []string{"/api/install/packages", "/api/install/start", "/api/install/pick", "/api/install/log/save"} {
		if rec := f.do("POST", route, ""); rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s without JSON=%d", route, rec.Code)
		}
	}
}

func TestLayerChoiceAPIWithoutPackages(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		deviceMissing, relayMissing, baseMissing bool
		suggested, relayProblem                  string
	}{
		{"base only", true, true, false, "full", "below-missing"},
		{"device only", false, true, false, "full", ""},
		{"all installed", false, false, false, "relay", ""},
		{"relay without device", true, false, false, "full", "below-missing"},
		{"base missing", false, false, true, "", "base-missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _, _, dev, _ := installFixture(t, true)
			dev.deviceMissing, dev.noBase = tc.deviceMissing, tc.baseMissing
			if tc.relayMissing {
				dev.version = ""
			}
			s := installState(t, f)
			if len(s.Layers) != 3 || len(s.Choices) != 5 || s.Suggested != tc.suggested || len(s.Packages) != 0 {
				t.Fatalf("state=%+v", s)
			}
			if s.Layers[0].Action != "install" || s.Layers[0].Choice != "base" || s.Layers[0].Name["zh-CN"] == "" {
				t.Fatalf("base=%+v", s.Layers[0])
			}
			for _, c := range s.Choices[:3] {
				if c.Ready || len(c.Needs) == 0 || len(c.Steps) == 0 {
					t.Fatalf("choice=%+v", c)
				}
				if tc.baseMissing && c.Problem.Code != "base-missing" {
					t.Fatalf("choice=%+v", c)
				}
				if !tc.baseMissing && c.ID != "relay" && c.Problem != nil {
					t.Fatalf("package gated choice=%+v", c)
				}
			}
			row := s.Layers[2]
			if tc.relayProblem != "" && (row.Enabled || row.Reason.Code != tc.relayProblem) {
				t.Fatalf("relay=%+v", row)
			}
			if rec := f.do("POST", "/api/install/start", `{"choice":"full"}`); rec.Code != http.StatusConflict {
				t.Fatalf("unready start=%d %s", rec.Code, rec.Body)
			}
			if dev.pushes != 0 {
				t.Fatal("unready choice wrote a payload")
			}
		})
	}
}

func TestFullInstallRequiresEveryDeclaredNeed(t *testing.T) {
	f, _, _, _, native := installFixture(t, true)
	deb, _ := relayFiles(t)
	native.paths = []string{deb}
	if rec := f.do("POST", "/api/install/pick", `{"layer":"relay"}`); rec.Code != http.StatusOK || native.layer != "relay" {
		t.Fatalf("picker=%d %s layer=%s", rec.Code, rec.Body, native.layer)
	}
	s := installState(t, f)
	if len(s.Choices[0].Needs) != 2 || s.Choices[0].Ready || !s.Choices[2].Ready || s.Choices[0].Problem != nil {
		t.Fatalf("choices=%+v", s.Choices)
	}
	if rec := f.do("POST", "/api/install/start", `{"choice":"full"}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "no-package" {
		t.Fatalf("partial full=%d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/install/pick", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing layer=%d", rec.Code)
	}
	native.layer = ""
	if rec := f.do("POST", "/api/install/pick", `{"layer":"device"}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "wrong-layer" || native.layer != "device" {
		t.Fatalf("wrong layer=%d %s", rec.Code, rec.Body)
	}
	if len(s.Choices[1].Needs[0].FileTypes) != 1 || s.Choices[1].Needs[0].FileTypes[0] != ".tar.gz" || len(s.Choices[2].Needs[0].FileTypes) != 2 {
		t.Fatal("picker file types do not match needs")
	}
}

type fakeInhibitor struct {
	acquired, released atomic.Int32
	err                error
}

func (p *fakeInhibitor) Acquire() (func(), error) {
	p.acquired.Add(1)
	if p.err != nil {
		return nil, p.err
	}
	return func() { p.released.Add(1) }, nil
}

func TestJobKeepsAwakeUntilSuccessFailureOrCancellation(t *testing.T) {
	for _, end := range []string{"success", "failure", "cancel"} {
		t.Run(end, func(t *testing.T) {
			f, svc, _, dev, _ := installFixture(t, true)
			deb, _ := relayFiles(t)
			dev.started, dev.release = make(chan struct{}), make(chan struct{})
			dev.failInstall = end == "failure"
			p := &fakeInhibitor{}
			svc.SetSleepInhibitor(p)
			f.do("POST", "/api/install/packages", bodyPath(deb))
			if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != http.StatusAccepted {
				t.Fatalf("start=%d %s", rec.Code, rec.Body)
			}
			select {
			case <-dev.started:
			case <-time.After(2 * time.Second):
				t.Fatal("job did not start")
			}
			if !svc.InstallRunning() || p.acquired.Load() != 1 || p.released.Load() != 0 || !svc.InstallSnapshot().InstalledStale {
				t.Fatal("running job not protected")
			}
			if end == "cancel" {
				svc.Stop()
			} else {
				close(dev.release)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s, err := svc.InstallWait(ctx)
			if err != nil || svc.InstallRunning() || p.released.Load() != 1 {
				t.Fatalf("release=%d running=%v err=%v", p.released.Load(), svc.InstallRunning(), err)
			}
			if end == "success" && (s.Job.Status != "succeeded" || s.InstalledStale) {
				t.Fatalf("success=%+v", s)
			}
			if end == "failure" {
				if s.Job.Status != "failed" || s.Job.Left != "partial" || s.Job.Observed || !s.InstalledStale {
					t.Fatalf("failure=%+v", s)
				}
				// A failed read retains the same explicitly stale upper summary.
				if again := installState(t, f); !again.InstalledStale || again.Installed == nil || again.Problem == nil {
					t.Fatalf("stale read=%+v", again)
				}
				dev.mu.Lock()
				dev.failInspect = false
				dev.version = "0.1.5~beta.1"
				dev.mu.Unlock()
				if again := installState(t, f); again.InstalledStale || again.Installed.Layers["relay"] != "0.1.5~beta.1" {
					t.Fatalf("fresh read=%+v", again)
				}
			}
		})
	}
}

func TestKeepAwakeFailureRefusesStart(t *testing.T) {
	f, svc, _, dev, _ := installFixture(t, true)
	deb, _ := relayFiles(t)
	p := &fakeInhibitor{err: errors.New("fake assertion failure")}
	svc.SetSleepInhibitor(p)
	f.do("POST", "/api/install/packages", bodyPath(deb))
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "keep-awake" {
		t.Fatalf("start=%d %s", rec.Code, rec.Body)
	}
	if svc.InstallRunning() || dev.pushes != 0 || p.acquired.Load() != 1 || p.released.Load() != 0 {
		t.Fatal("assertion failure started a job")
	}
}

func TestPackageRequirementReadFailureDoesNotGateChoose(t *testing.T) {
	f, _, _, dev, _ := installFixture(t, true)
	deb, _ := relayFiles(t)
	dev.failPackageFacts = true
	f.do("POST", "/api/install/packages", bodyPath(deb))
	s := installState(t, f)
	if s.Problem != nil || s.Installed == nil || s.InstalledStale {
		t.Fatalf("package affected discovery=%+v", s)
	}
	for _, c := range s.Choices[:3] {
		if c.Problem != nil {
			t.Fatalf("package gated choose=%+v", c)
		}
	}
	if !s.Layers[2].Enabled || s.Choices[2].Ready || s.Choices[2].Needs[0].Problem == nil {
		t.Fatalf("relay=%+v", s.Choices[2])
	}
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != http.StatusConflict {
		t.Fatalf("unusable start=%d %s", rec.Code, rec.Body)
	}
}

func TestCloseGuardStaysArmedThroughFailureReadback(t *testing.T) {
	f, svc, _, dev, _ := installFixture(t, true)
	deb, _ := relayFiles(t)
	dev.failInstall = true
	readback := make(chan struct{})
	dev.readbackStarted, dev.readbackRelease = readback, make(chan struct{})
	defer close(dev.readbackRelease)
	p := &fakeInhibitor{}
	svc.SetSleepInhibitor(p)
	f.do("POST", "/api/install/packages", bodyPath(deb))
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("start=%d %s", rec.Code, rec.Body)
	}
	select {
	case <-readback:
	case <-time.After(2 * time.Second):
		t.Fatal("failure readback not reached")
	}
	if !svc.InstallRunning() || svc.InstallSnapshot().Job.Status != "running" || p.released.Load() != 0 {
		t.Fatal("warning and guard ended before readback")
	}
}

func TestDestructiveAPIDemandsTypedDeviceSerial(t *testing.T) {
	f, _, _, dev, _ := installFixture(t, true)
	for _, choice := range []string{"base", "full-reinstall"} {
		for _, confirmation := range []string{"", "another-board"} {
			body, _ := json.Marshal(map[string]string{"choice": choice, "yesErase": confirmation})
			rec := f.do("POST", "/api/install/start", string(body))
			if rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "typed-serial" {
				t.Fatalf("%s: %d %s", choice, rec.Code, rec.Body)
			}
		}
	}
	if dev.pushes != 0 {
		t.Fatal("destructive refusal wrote a package")
	}
}

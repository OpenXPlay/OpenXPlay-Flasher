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
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type installDevice struct {
	mu      sync.Mutex
	version string
	data    []byte
	pushes  int
	started chan struct{}
	release chan struct{}
	onClock func()
}

func (d *installDevice) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	switch {
	case strings.Contains(cmd, "uname -r"):
		return adb.Result{Stdout: []byte("root\t0\nkernel\t5.15.0-handsomekernel+\nmodel\tHandsome Openstick\ndebian\t11\narch\tarm64\n")}, nil
	case strings.HasPrefix(cmd, "dpkg-query "):
		version := "1.0"
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
}

func (n *packageNative) OpenPackages() ([]string, error) { return n.paths, nil }
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
	if state.Choices[0].Problem != nil || state.Choices[1].Problem.Code != "no-package" {
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
	if rec := f.do("POST", "/api/install/pick", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("picker=%d %s", rec.Code, rec.Body)
	}
	if len(installState(t, f).Packages) != 1 {
		t.Fatal("picker did not deduplicate the pair")
	}
}

func TestInstallRefusalsAndDowngrade(t *testing.T) {
	f, _, bus, dev, _ := installFixture(t, true)
	deb, _ := relayFiles(t)
	if rec := f.do("POST", "/api/install/packages", bodyPath(deb+".missing")); rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec.Body.Bytes()) != "package" {
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
	if rec := f.do("POST", "/api/install/start", `{"choice":"base"}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "bad-choice" {
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

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type agentCatalog struct {
	t        *testing.T
	index    *catalog.Index
	payloads map[string][]byte
	err      error
	verified bool
}

func (s *agentCatalog) Fetch(context.Context, string, catalog.Mode) (*catalog.Result, error) {
	s.t.Fatal("read-only agent command fetched the package list")
	return nil, errors.New("unexpected fetch")
}
func (s *agentCatalog) LoadCached(_ context.Context, _ string, mode catalog.Mode) (*catalog.Result, error) {
	if mode != catalog.Verified {
		s.t.Fatal("agent command did not enforce the signed catalog")
	}
	return &catalog.Result{Index: s.index, Verified: s.verified}, s.err
}
func (*agentCatalog) Offline() bool { return true }

func agentCatalogFixture(t *testing.T) *agentCatalog {
	t.Helper()
	ps, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	source := &agentCatalog{t: t, verified: true, payloads: map[string][]byte{}, index: &catalog.Index{
		Schema: catalog.Schema, Channel: "beta", Sequence: 1, Generated: "2026-10-22T00:00:00Z", Devices: map[string]catalog.Device{}}}
	for _, p := range ps {
		dev := catalog.Device{Recommended: map[string]string{"device": "2.0", "relay": "3.0"}}
		if p.Base != nil {
			layer := catalog.Layer{Layer: "base", Version: p.Base.Release, Source: "https://fixture.invalid/base"}
			for _, role := range []string{"base-generic", "rootfs-zip", "boot"} {
				pin := p.Base.Archives[role]
				layer.Artifacts = append(layer.Artifacts, catalog.Artifact{Role: role, Name: pin.File, Size: pin.Size, SHA256: pin.SHA256, URLs: []string{"https://fixture.invalid/" + pin.File}})
			}
			dev.Recommended["base"] = p.Base.Release
			dev.Layers = append(dev.Layers, layer)
		}
		for _, layer := range []string{"device", "relay"} {
			entry := catalog.Layer{Layer: layer, Version: dev.Recommended[layer], Source: "https://fixture.invalid/" + layer}
			roles := []string{"bundle"}
			if layer == "relay" {
				roles = []string{"deb", "manifest"}
			}
			for _, role := range roles {
				name := p.ID + "-" + layer + "-" + role + ".bin"
				data := []byte("signed package fixture " + name)
				sum := sha256.Sum256(data)
				source.payloads[name] = data
				entry.Artifacts = append(entry.Artifacts, catalog.Artifact{Role: role, Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), URLs: []string{"https://fixture.invalid/" + name, "https://mirror.invalid/" + name}})
			}
			dev.Layers = append(dev.Layers, entry)
		}
		source.index.Devices[p.ID] = dev
	}
	if err := source.index.Validate(); err != nil {
		t.Fatal(err)
	}
	return source
}

type agentConnector struct {
	t        *testing.T
	allowed  map[string]adb.Device
	metadata map[string]adb.Metadata
	opens    int
}

func (c *agentConnector) Connect(_ context.Context, info usb.DeviceInfo) (adb.Device, error) {
	c.opens++
	d := c.allowed[info.Location()]
	if d == nil {
		c.t.Fatal("opened an ignored or ambiguous adb device", info.Location())
	}
	return d, nil
}
func (c *agentConnector) Describe(context.Context, []usb.DeviceInfo) map[string]adb.Metadata {
	return c.metadata
}

type agentStockDevice struct {
	adb.Device
	t     *testing.T
	wrong bool
}

func (d agentStockDevice) Shell(_ context.Context, cmd string) (adb.Result, error) {
	value := "msm8916_32_512"
	switch cmd {
	case "getprop ro.product.model", "getprop ro.product.name", "getprop ro.product.device":
		if d.wrong {
			value = "phone"
		}
	case "getprop ro.build.version.release":
		value = "4.4.4"
	case "getprop ro.serialno":
		value = "cli-board"
	default:
		d.t.Fatal("stock status performed an unexpected operation", cmd)
	}
	return adb.Result{Stdout: []byte(value)}, nil
}
func (agentStockDevice) Close() error { return nil }

type agentFastboot struct {
	base.Fastboot
	t       *testing.T
	product string
}

func (f agentFastboot) GetVar(_ context.Context, name string) (string, error) {
	switch name {
	case "product":
		return f.product, nil
	case "serialno":
		return "cli-board", nil
	}
	f.t.Fatal("unexpected fastboot variable", name)
	return "", nil
}
func (agentFastboot) Close() error { return nil }

func agentFixture(t *testing.T, mode string) (*app.Service, *nextDeviceFake, *agentCatalog, *agentConnector, string) {
	t.Helper()
	svc, d, bus := nextFixture(t)
	source := agentCatalogFixture(t)
	dir := t.TempDir()
	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source)
	linux := usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 1, Address: 1, Path: []int{1}}
	stock := usb.DeviceInfo{VendorID: 0x05c6, ProductID: 0x90b4, Bus: 1, Address: 2, Path: []int{2}, Serial: "cli-board", Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 0x01}}}
	phone := stock
	phone.VendorID, phone.Address, phone.Serial = 0x2717, 3, "ignored-phone"
	phone.Path = []int{3}
	c := &agentConnector{t: t, allowed: map[string]adb.Device{}, metadata: map[string]adb.Metadata{
		stock.Location(): {Serial: "cli-board", Model: "msm8916_32_512"}, phone.Location(): {Serial: "ignored-phone", Model: "Phone"}}}
	svc.SetADB(c)
	fb := agentFastboot{t: t, product: "LK1ST_MSM8916"}
	svc.SetFastboot(func(context.Context, usb.DeviceInfo) (base.Fastboot, error) { return fb, nil }, true)
	switch mode {
	case "Linux":
		bus.Set(linux, phone)
		c.allowed[linux.Location()] = d
	case "stock", "wrong stock":
		bus.Set(stock, phone)
		c.allowed[stock.Location()] = agentStockDevice{t: t, wrong: mode == "wrong stock"}
	case "fastboot":
		linux.ProductID = 0xd00d
		bus.Set(linux, phone)
	case "two":
		second := linux
		second.Address = 2
		second.Path = []int{2}
		bus.Set(linux, second, phone)
	case "none", "SSH":
		bus.Set()
	}
	return svc, d, source, c, dir
}

func TestCLIStatusModesAndJSON(t *testing.T) {
	for _, tc := range []struct {
		mode, suggested, wantMode string
		designate                 bool
		count, exit               int
	}{
		{"Linux", "relay", "adb", false, 2, 0},
		{"stock", "", "", false, 2, 0},
		{"stock", "stock", "stock", true, 2, 0},
		{"fastboot", "repair", "fastboot", false, 2, 0},
		{"none", "", "", false, 0, 0},
		{"two", "", "", false, 3, 1},
	} {
		t.Run(fmt.Sprintf("%s/%t", tc.mode, tc.designate), func(t *testing.T) {
			svc, d, _, connector, _ := agentFixture(t, tc.mode)
			args := []string{"status", "-json"}
			if tc.designate {
				args = append(args, "--stock-adb", "cli-board")
			}
			var out, errout bytes.Buffer
			code := Main(args, &out, &errout)
			var result app.DeviceStatus
			if code != tc.exit || json.Unmarshal(out.Bytes(), &result) != nil || len(result.Candidates) != tc.count || result.Suggested != tc.suggested {
				t.Fatalf("exit=%d out=%s err=%s", code, &out, &errout)
			}
			for _, field := range []string{"candidates", "installed", "recommended", "catalog", "suggested", "choices"} {
				var shape map[string]json.RawMessage
				_ = json.Unmarshal(out.Bytes(), &shape)
				if len(shape[field]) == 0 || string(shape[field]) == "null" {
					t.Fatal("missing JSON field", field)
				}
			}
			if tc.wantMode != "" {
				if result.Target == nil || string(result.Target.Device.Mode) != tc.wantMode || result.Target.Profile != "ufi003" || result.Serial != "cli-board" || result.Recommended["relay"] != "3.0" || len(result.Choices) < 3 {
					t.Fatalf("incomplete target status: %+v", result)
				}
				for _, c := range result.Choices {
					if c.Destructive != (c.ID == "stock" || c.ID == "base" || c.ID == "full-reinstall" || c.ID == "repair") {
						t.Fatalf("wrong erasure flag: %+v", c)
					}
				}
				if tc.mode == "Linux" && result.Installed["relay"] != "1.0" {
					t.Fatal("installed versions missing")
				}
			} else if result.Target != nil {
				t.Fatal("selected an absent, ignored or ambiguous target")
			}
			if tc.mode == "stock" && !tc.designate && connector.opens != 0 || tc.mode == "two" && connector.opens != 0 {
				t.Fatal("opened an undesignated or ambiguous device")
			}
			if tc.mode == "two" && (result.Problem == nil || result.Problem.Code != "several-devices") {
				t.Fatal("ambiguity not reported")
			}
			if tc.mode == "stock" && !tc.designate && (result.Candidates[0].ADBName != "msm8916_32_512" || result.Candidates[0].ADBSerial != "cli-board" || result.Candidates[0].StockDesignated) {
				t.Fatal("missing ignored stock metadata")
			}
			if tc.designate && !result.Candidates[0].StockDesignated {
				t.Fatal("designation not reported")
			}
			out.Reset()
			errout.Reset()
			if code := runEngineCLI("status", args[2:], strings.NewReader(""), &out, &errout); code != tc.exit {
				t.Fatalf("text exit=%d error=%s", code, &errout)
			}
			if tc.mode == "stock" && !tc.designate && !strings.Contains(out.String(), "msm8916_32_512") {
				t.Fatal("text lacks ignored model")
			}
			for _, cmd := range d.commands {
				if strings.Contains(cmd, "/mfi/") && (!strings.Contains(cmd, "stat -c '%s'") || strings.Contains(cmd, "cat ") || strings.Contains(cmd, "sha256sum")) {
					t.Fatal("status touched identity contents")
				}
			}
			if svc.InstallSnapshot().Job != nil || d.installs != 0 {
				t.Fatal("status started a job")
			}
		})
	}
}

func TestCLIFilesModesAndJSON(t *testing.T) {
	for _, tc := range []struct {
		mode, choice string
		designate    bool
		count, exit  int
	}{
		{"Linux", "relay", false, 2, 3}, {"Linux", "full", false, 3, 3},
		{"stock", "stock", true, 6, 3}, {"stock", "stock", false, 0, 1},
		{"fastboot", "repair", false, 3, 3}, {"none", "relay", false, 0, 1}, {"two", "relay", false, 0, 1},
	} {
		t.Run(tc.mode+"/"+tc.choice+fmt.Sprint(tc.designate), func(t *testing.T) {
			_, d, source, _, _ := agentFixture(t, tc.mode)
			args := []string{"files", "--choice", tc.choice, "-json"}
			if tc.designate {
				args = append(args, "--stock-adb", "cli-board")
			}
			var out, errout bytes.Buffer
			if code := Main(args, &out, &errout); code != tc.exit {
				t.Fatalf("exit=%d out=%s err=%s", code, &out, &errout)
			}
			if tc.count == 0 {
				if !strings.Contains(out.String(), "code") {
					t.Fatal("missing typed JSON error")
				}
				return
			}
			var result app.ChoiceFiles
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.AllFound || len(result.Files) != tc.count || result.Choice != tc.choice || result.Profile != "ufi003" {
				t.Fatalf("bad files JSON: %s", &out)
			}
			for _, f := range result.Files {
				entry, ok := source.index.Devices[result.Profile].Lookup(f.Layer, f.Version)
				if !ok || f.Status != "missing" || f.Path != "" || f.Size <= 0 || len(f.SHA256) != 64 {
					t.Fatalf("incomplete file: %+v", f)
				}
				matched := false
				for _, a := range entry.Artifacts {
					if a.Role == f.Role {
						matched = a.Name == f.Name && a.Size == f.Size && a.SHA256 == f.SHA256 && reflect.DeepEqual(a.URLs, f.URLs)
					}
				}
				if !matched {
					t.Fatal("file or URLs differ from signed list", f)
				}
			}
			if d.installs != 0 {
				t.Fatal("files wrote to a device")
			}
		})
	}
}

func TestCLIFilesRechecksRememberedPaths(t *testing.T) {
	svc, _, source, _, dir := agentFixture(t, "Linux")
	var selected []app.SelectedFile
	for _, l := range source.index.Devices["ufi003"].Layers {
		if l.Layer == "base" {
			continue
		}
		for _, a := range l.Artifacts {
			path := filepath.Join(dir, a.Name)
			if err := os.WriteFile(path, source.payloads[a.Name], 0o600); err != nil {
				t.Fatal(err)
			}
			selected = append(selected, app.SelectedFile{Path: path, Size: a.Size, SHA256: a.SHA256, Profile: "ufi003", Layer: l.Layer, Role: a.Role, Name: a.Name, Version: l.Version})
		}
	}
	data, _ := json.Marshal(selected)
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "selected-files.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source)
	for _, change := range []string{"unchanged", "modified", "deleted"} {
		if change == "modified" {
			changed := bytes.Repeat([]byte("x"), int(selected[0].Size))
			if err := os.WriteFile(selected[0].Path, changed, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if change == "deleted" {
			if err := os.Remove(selected[1].Path); err != nil {
				t.Fatal(err)
			}
		}
		var out, errout bytes.Buffer
		code := Main([]string{"files", "--choice", "full", "-json"}, &out, &errout)
		var result app.ChoiceFiles
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.AllFound != (change == "unchanged") || code != map[bool]int{true: 0, false: 3}[result.AllFound] {
			t.Fatalf("%s exit=%d out=%s err=%s", change, code, &out, &errout)
		}
		if change == "unchanged" {
			for _, f := range result.Files {
				if f.Status != "found" || f.Path == "" {
					t.Fatal("remembered matching file missing", f)
				}
			}
		} else if result.Files[0].Status != "missing" || result.Files[0].Path != "" {
			t.Fatal("trusted changed remembered metadata")
		}
	}
	var out, errout bytes.Buffer
	if code := Main([]string{"files", "--choice", "relay"}, &out, &errout); code != 3 || !strings.Contains(out.String(), "SHA-256") || !strings.Contains(out.String(), "https://mirror.invalid/") || !strings.Contains(out.String(), "Missing / 缺少") {
		t.Fatalf("incomplete text: exit=%d %s %s", code, &out, &errout)
	}
}

func TestCLIAgentCommandsSSH(t *testing.T) {
	_, _, _, _, _ = agentFixture(t, "SSH")
	const password = "agent-ssh-test-secret"
	t.Setenv("OXP_FLASHER_SSH_PASSWORD", password)
	board := testssh.NewN1(t, password)
	host, port := board.Server.HostPort()
	flags := []string{"--ssh", fmt.Sprintf("riko@%s:%d", host, port), "--accept-host-key", board.Server.Fingerprint(), "-json"}
	for _, cmd := range []string{"status", "files"} {
		var out, errout bytes.Buffer
		args := append([]string{cmd}, flags...)
		if cmd == "files" {
			args = append(args, "--choice", "full")
		}
		code := Main(args, &out, &errout)
		if cmd == "status" {
			var result app.DeviceStatus
			if code != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.Target == nil || result.Target.Profile != "phicomm-n1" || result.Target.Device.Mode != profile.ModeSSH || result.Serial == "" || result.Installed["relay"] != "1.0" || result.Suggested != "relay" || result.Recommended["relay"] != "3.0" {
				t.Fatalf("SSH status exit=%d out=%s err=%s", code, &out, &errout)
			}
		} else {
			var result app.ChoiceFiles
			if code != 3 || json.Unmarshal(out.Bytes(), &result) != nil || result.Profile != "phicomm-n1" || len(result.Files) != 3 {
				t.Fatalf("SSH files exit=%d out=%s err=%s", code, &out, &errout)
			}
		}
		if strings.Contains(out.String()+errout.String(), password) {
			t.Fatal("SSH secret in agent output")
		}
	}
}

func TestCLIAgentHelpAndErrors(t *testing.T) {
	for _, cmd := range []string{"status", "files"} {
		var out, errout bytes.Buffer
		if code := Main([]string{cmd, "--help"}, &out, &errout); code != 0 || !strings.Contains(errout.String(), "files --choice") || !strings.Contains(errout.String(), "3: missing") {
			t.Fatalf("help exit=%d %s", code, &errout)
		}
		for _, args := range [][]string{{cmd, "--no-signature-check"}, {cmd, "unexpected"}} {
			if code := Main(args, &out, &errout); code != 2 {
				t.Fatalf("invalid arguments accepted: %v", args)
			}
		}
	}
	for _, mode := range []string{"stock", "wrong stock"} {
		_, _, _, _, _ = agentFixture(t, mode)
		var out, errout bytes.Buffer
		serial := "not-this-board"
		if mode == "wrong stock" {
			serial = "cli-board"
		}
		if code := Main([]string{"status", "--stock-adb", serial, "-json"}, &out, &errout); code != 1 || !strings.Contains(out.String(), "error") || mode == "wrong stock" && !strings.Contains(out.String(), "stock-mismatch") {
			t.Fatalf("bad designation accepted: exit=%d %s %s", code, &out, &errout)
		}
	}
	_, _, source, _, _ := agentFixture(t, "Linux")
	for _, unavailable := range []string{"absent", "unverified", "layer missing"} {
		source.verified, source.err = true, nil
		switch unavailable {
		case "absent":
			source.err = errors.New("no cached list")
		case "unverified":
			source.verified = false
		case "layer missing":
			dev := source.index.Devices["ufi003"]
			dev.Recommended["relay"] = "not-listed"
		}
		var out, errout bytes.Buffer
		if code := Main([]string{"files", "--choice", "relay", "-json"}, &out, &errout); code != 1 || !strings.Contains(out.String(), "catalog-") {
			t.Fatalf("missing signed inventory treated as complete: exit=%d %s %s", code, &out, &errout)
		}
	}
}

func TestCLIEnginePromptCancellation(t *testing.T) {
	for _, input := range []string{"n\n", "", "y", "Y\n"} {
		_, dev, file := engineFixture(t)
		var out, errout bytes.Buffer
		if code := runEngineCLI("install", []string{"--choice", "relay", file}, strings.NewReader(input), &out, &errout); code != 1 || dev.installs != 0 || !strings.Contains(errout.String(), "Installation cancelled / 已取消安装") {
			t.Fatalf("exit=%d out=%s err=%s", code, &out, &errout)
		}
	}
}

func TestCLIMaintenanceProgress(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		p := &enginePrinter{out: &out, json: asJSON}
		state := app.InstallState{Job: &stage.Job{Status: "running", Steps: []stage.StepProgress{{Step: stage.Step{Layer: "relay", Action: "install"}, Status: "running"}}}}
		p.Emit(app.EventInstall, state)
		state.Job.Progress = stage.PackageMaintenanceMessage
		p.Emit(app.EventInstall, state)
		p.Emit(app.EventInstall, state)
		if strings.Count(out.String(), stage.PackageMaintenanceMessage) != 1 {
			t.Fatal("waiting progress was lost or duplicated", out.String())
		}
	}
}

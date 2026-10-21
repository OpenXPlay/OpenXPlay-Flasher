// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type cliDevice struct {
	version  string
	data     []byte
	installs int
}

func (d *cliDevice) Shell(_ context.Context, cmd string) (adb.Result, error) {
	out := ""
	switch {
	case cmd == "cat /proc/cmdline":
		out = "androidboot.serialno=cli-board"
	case strings.Contains(cmd, "uname -r"):
		out = "root\t0\nkernel\t5.15.0-handsomekernel+\nmodel\tHandsome Openstick\ndebian\t11\narch\tarm64\n"
	case strings.HasPrefix(cmd, "dpkg-query "):
		out = "installed\t1.0"
		if strings.HasSuffix(cmd, "'openxplay-relay'") {
			out = "installed\t" + d.version
		}
	case strings.Contains(cmd, "df -Pk"):
		out = "/dev/root 1000000 200000 800000 20% /\n"
	case strings.HasPrefix(cmd, "sha256sum "):
		sum := sha256.Sum256(d.data)
		out = hex.EncodeToString(sum[:]) + "  relay.deb\n"
	case strings.Contains(cmd, "openxplay-relay-update --sha256"):
		d.version = "0.1.5~beta.1"
		d.installs++
		out = "OXP-STAGE relay ok\n"
	case strings.Contains(cmd, "systemctl is-active"):
		out = "active\n"
	case strings.HasPrefix(cmd, "date -u -s"), strings.HasPrefix(cmd, "umask 077; mkdir -p"):
	default:
		return adb.Result{}, errors.New("unexpected fake command: " + cmd)
	}
	return adb.Result{Stdout: []byte(out)}, nil
}
func (d *cliDevice) Push(_ context.Context, _ string, b []byte, _ fs.FileMode) error {
	d.data = b
	return nil
}
func (d *cliDevice) Pull(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("unexpected pull")
}
func (d *cliDevice) Close() error { return nil }

type cliConnector struct{ dev *cliDevice }

func (c cliConnector) Connect(context.Context, usb.DeviceInfo) (adb.Device, error) { return c.dev, nil }

func cliInstallFixture(t *testing.T, out *bytes.Buffer) (*app.Service, *cliDevice, string) {
	t.Helper()
	data := []byte("relay fixture")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	m := stage.Manifest{Schema: stage.Schema, Stage: "relay", Name: "openxplay-relay", Version: "0.1.5~beta.1", ReleaseVersion: "0.1.5-beta.1", Writes: "files", Transport: "adb",
		Requires: stage.Requirements{DebianVersion: "11", DPKGArchitecture: "arm64"}, File: "relay.deb", SHA256: hash, DeviceDir: "/var/cache/openxplay/relay", FirstInstall: "dpkg -i /var/cache/openxplay/relay/relay.deb",
		Install: "openxplay-relay-update --sha256 " + hash + " /var/cache/openxplay/relay/relay.deb", Verify: "systemctl is-active openxplay-relay.service", Rollback: "openxplay-relay-update --rollback", Reboot: "none", Success: stage.Success{Install: "OXP-STAGE relay ok", RolledBack: "OXP-STAGE relay rolled-back"}}
	dir := t.TempDir()
	deb := filepath.Join(dir, "relay.deb")
	manifest := filepath.Join(dir, "relay.manifest.json")
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deb, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, b, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := app.NewForBus(usb.NewFakeBus(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 1, Address: 1}), &installPrinter{out: out})
	if err != nil {
		t.Fatal(err)
	}
	dev := &cliDevice{version: "0.1.3"}
	svc.SetADB(cliConnector{dev})
	return svc, dev, deb
}

func TestCLIInstallConfirmationAndYes(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		yes         bool
		want        int
	}{{"cancel", "n\n", false, 0}, {"confirm", "y\n", false, 1}, {"yes", "", true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			svc, dev, deb := cliInstallFixture(t, &out)
			if code := runInstall(svc, []string{deb}, "relay", false, tc.yes, strings.NewReader(tc.input), &out, &errOut); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, &errOut)
			}
			if dev.installs != tc.want {
				t.Fatalf("installed %d times, output=%s", dev.installs, &out)
			}
			if !strings.Contains(out.String(), "Install plan:") || !strings.Contains(out.String(), "Keep the USB cable connected") {
				t.Fatalf("plan or warning missing: %s", &out)
			}
			if tc.want == 1 && !strings.Contains(out.String(), "Installation succeeded") {
				t.Fatalf("no final result: %s", &out)
			}
		})
	}
}

func TestCLIInstallDowngradeNeedsFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	svc, dev, deb := cliInstallFixture(t, &out)
	dev.version = "0.2.0"
	if code := runInstall(svc, []string{deb}, "relay", false, true, strings.NewReader(""), &out, &errOut); code != 1 || dev.installs != 0 || !strings.Contains(errOut.String(), "-downgrade") {
		t.Fatalf("code=%d out=%s stderr=%s", code, &out, &errOut)
	}
	var out2, err2 bytes.Buffer
	svc2, dev2, deb2 := cliInstallFixture(t, &out2)
	dev2.version = "0.2.0"
	if code := runInstall(svc2, []string{deb2}, "relay", true, true, strings.NewReader(""), &out2, &err2); code != 0 || dev2.installs != 1 {
		t.Fatalf("code=%d stderr=%s", code, &err2)
	}
}

func TestCLIInstallUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runInstall(nil, nil, "", false, false, nil, &out, &errOut); code != 2 {
		t.Fatalf("no files=%d", code)
	}
	if code := runInstall(nil, []string{"file"}, "base", false, false, nil, &out, &errOut); code != 2 {
		t.Fatalf("base=%d", code)
	}
	if code := Main([]string{"help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "install [-only device|relay]") {
		t.Fatalf("help=%d %s", code, &out)
	}
}

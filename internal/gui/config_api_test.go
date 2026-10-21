// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// hostJSONDevice answers the editor's probe for /etc/openxplay/host.json and counts uploads.
type hostJSONDevice struct{ pushes int }

func (d *hostJSONDevice) Shell(context.Context, string) (adb.Result, error) {
	return adb.Result{Stdout: []byte("file\n/etc/openxplay/host.json\n644\nroot:root\n17\n")}, nil
}

func (d *hostJSONDevice) Pull(context.Context, string, int64) ([]byte, error) {
	return []byte(`{"model":"UFI003"}`), nil
}

func (d *hostJSONDevice) Push(context.Context, string, []byte, fs.FileMode) error {
	d.pushes++
	return nil
}

func (d *hostJSONDevice) Close() error { return nil }

type oneDevice struct{ dev adb.Device }

func (c oneDevice) Connect(context.Context, usb.DeviceInfo) (adb.Device, error) { return c.dev, nil }

// configFixture serves the handler over a fake bus holding the UFI003 dongle in adb mode.
func configFixture(t *testing.T, withADB bool) (fixture, *hostJSONDevice) {
	t.Helper()
	bus := usb.NewFakeBus(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 0, Path: []int{1, 1, 1},
		Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 0x01}}})
	hub := NewHub()
	svc, err := app.NewForBus(bus, hub)
	if err != nil {
		t.Fatal(err)
	}
	dev := &hostJSONDevice{}
	if withADB {
		svc.SetADB(oneDevice{dev})
	}
	svc.Start(context.Background())
	t.Cleanup(svc.Stop)
	deadline := time.Now().Add(2 * time.Second)
	for len(svc.Devices().Candidates) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	native := &fakeNative{}
	h := Handler(Deps{Service: svc, Hub: hub, Settings: NewSettingsStore(t.TempDir()), Native: native})
	return fixture{handler: h, hub: hub, native: native}, dev
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct{ Code string }
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("not an error body: %s", body)
	}
	return e.Code
}

func TestConfigNeedsADB(t *testing.T) {
	f, _ := configFixture(t, false)
	rec := f.do("GET", "/api/config", "")
	if rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "no-adb" {
		t.Fatalf("GET /api/config = %d %s", rec.Code, rec.Body)
	}
}

func TestConfigRoutes(t *testing.T) {
	f, dev := configFixture(t, true)

	rec := f.do("GET", "/api/config", "")
	var target app.ConfigTarget
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &target) != nil || len(target.Files) != 2 || target.Device.USB.Location() != "0-1.1.1" {
		t.Fatalf("GET /api/config = %d %s", rec.Code, rec.Body)
	}

	rec = f.do("POST", "/api/config/read", `{"path":"/etc/openxplay/host.json"}`)
	if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("read = %d %s", rec.Code, rec.Body)
	}

	rec = f.do("POST", "/api/config/read", `{"path":"/var/lib/openxplay/mfi/identity.pk8"}`)
	if rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "protected" {
		t.Fatalf("protected read = %d %s", rec.Code, rec.Body)
	}

	rec = f.do("POST", "/api/config/write", `{"path":"/etc/openxplay/host.json","content":"{\"model\":"}`)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec.Body.Bytes()) != "invalid" {
		t.Fatalf("invalid write = %d %s", rec.Code, rec.Body)
	}
	if dev.pushes != 0 {
		t.Fatal("invalid content was uploaded")
	}

	if rec := f.do("POST", "/api/config/export", `{"path":"/etc/openxplay/host.json","content":"{}"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("export without dialogs = %d", rec.Code)
	}
}

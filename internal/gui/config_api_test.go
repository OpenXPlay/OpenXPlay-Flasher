// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// hostJSONDevice answers the editor's probe for /etc/openxplay/host.json and counts uploads.
type hostJSONDevice struct{ pushes int }

func (d *hostJSONDevice) Shell(_ context.Context, cmd string) (adb.Result, error) {
	p := cmd[strings.LastIndex(cmd, " '")+2 : len(cmd)-1]
	if p != "/etc/openxplay/host.json" {
		return adb.Result{Stdout: []byte("missing\n" + p + "\n")}, nil
	}
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
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &target) != nil || len(target.Files) != 3 || target.Device.USB.Location() != "0-1.1.1" {
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

// privateDevice serves a private file and can simulate transport errors containing credentials.
type privateDevice struct {
	content string
	fail    bool
	pushes  int
}

func (d *privateDevice) Shell(_ context.Context, cmd string) (adb.Result, error) {
	if d.fail {
		return adb.Result{}, fmt.Errorf("transport diagnostic: %s", d.content)
	}
	if strings.Contains(cmd, "p=$1") {
		p := cmd[strings.LastIndex(cmd, " '")+2 : len(cmd)-1]
		return adb.Result{Stdout: []byte("file\n" + p + "\n600\nroot:root\n200\n")}, nil
	}
	return adb.Result{Stdout: []byte("backup=\n")}, nil
}
func (d *privateDevice) Pull(context.Context, string, int64) ([]byte, error) {
	return []byte(d.content), nil
}
func (d *privateDevice) Push(_ context.Context, _ string, _ []byte, _ fs.FileMode) error {
	d.pushes++
	return nil
}
func (d *privateDevice) Close() error { return nil }

func privateFixture(t *testing.T, dev adb.Device) fixture {
	t.Helper()
	bus := usb.NewFakeBus(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 0, Path: []int{1}, Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 1}}})
	hub := NewHub()
	svc, err := app.NewForBus(bus, hub)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetADB(oneDevice{dev})
	svc.Start(context.Background())
	t.Cleanup(svc.Stop)
	deadline := time.Now().Add(time.Second)
	for len(svc.Devices().Candidates) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	native := &fakeNative{}
	return fixture{handler: Handler(Deps{Service: svc, Hub: hub, Settings: NewSettingsStore(t.TempDir()), Native: native}), native: native}
}

func TestPrivacyAPINeverReturnsToken(t *testing.T) {
	token := strings.Repeat("sensitive", 4)
	content, _ := json.Marshal(map[string]any{"version": 1, "provider": "dynv6", "name": "car.example.net", "token": token, "agreeLetsEncryptTerms": true})
	dev := &privateDevice{content: string(content)}
	f := privateFixture(t, dev)
	rec := f.do("POST", "/api/config/privacy/read", `{"id":"public-name"}`)
	var v struct {
		Exists, TokenSet, AgreeLetsEncryptTerms bool
		Provider                                string
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil || !v.Exists || !v.TokenSet || !v.AgreeLetsEncryptTerms || v.Provider != "dynv6" {
		t.Fatal("redacted private read failed")
	}
	if strings.Contains(rec.Body.String(), token) || strings.Contains(rec.Body.String(), `"token":`) {
		t.Fatal("saved token reached API response")
	}
	for _, request := range []struct{ action, body string }{
		{"write", `{"id":"public-name","enabled":true,"provider":"dynv6","name":"car.example.net","token":"","agreeLetsEncryptTerms":false}`},
		{"import", `{"id":"public-name","content":"{\"version\":1,\"provider\":\"dynv6\",\"name\":\"car.example.net\",\"token\":\"` + token + `\"}"}`},
	} {
		rec = f.do("POST", "/api/config/privacy/"+request.action, request.body)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), token) || strings.Contains(rec.Body.String(), `"token":`) {
			t.Fatal("private write/import response failed redaction")
		}
	}
	for _, body := range []string{
		`{"id":"public-name","token":"` + token + ` ","enabled":true,"provider":"dynv6","name":"car.example.net"}`,
		`{"id":"public-name","` + token + `":true}`,
		`{"id":"public-name","provider":"` + token + `"}`,
		`{"id":"public-name","token":"` + token + `"} trailing`,
	} {
		rec = f.do("POST", "/api/config/privacy/write", body)
		if rec.Code < 400 || strings.Contains(rec.Body.String(), token) {
			t.Fatal("private validation error exposed input")
		}
	}
	rec = f.do("POST", "/api/config/privacy/import", `{"id":"public-name","content":"{\"version\":1,\"token\":\"`+token+` \"}"}`)
	if rec.Code != 422 || strings.Contains(rec.Body.String(), token) || !strings.Contains(rec.Body.String(), `"token":"invalid"`) {
		t.Fatal("import validation did not return safe field codes")
	}
	dev.fail = true
	rec = f.do("POST", "/api/config/privacy/read", `{"id":"public-name"}`)
	if rec.Code < 400 || strings.Contains(rec.Body.String(), token) {
		t.Fatal("private transport error exposed credential")
	}
}

func TestRawAPIsRefusePrivacyAndFlasherStorage(t *testing.T) {
	f, _ := configFixture(t, true)
	for _, p := range []string{"/var/lib/openxplay/public-name.json", "/var/lib/openxplay//public-name.json",
		"/var%lib%openxplay%public-name.json", "/var/lib/openxplay/public-name/account.key", "/var/lib/openxplay/public-name.tmp-abcd", "/var/lib/openxplay/public-name.json.oxpf-tmp", "/var/lib/openxplay-flasher/backups", "/var/lib/openxplay-flasher/backups/var%lib%openxplay%public-name.json/20261021-000000-1", "/var/lib/openxplay-flasher/staging/1.new"} {
		for _, action := range []string{"read", "write", "backups", "restore", "export"} {
			body, _ := json.Marshal(map[string]any{"path": p})
			rec := f.do("POST", "/api/config/"+action, string(body))
			if rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "privacy" {
				t.Errorf("%s did not refuse private path", action)
			}
		}
	}
}

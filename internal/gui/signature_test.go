// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
)

func TestSignatureSettingDefaultsAndPersistence(t *testing.T) {
	f := newFixture(t)
	for _, contents := range []string{"", `{"lang":"en"}`, `{`, `{"verifyPackageListSignature":"invalid"}`} {
		if contents != "" {
			if err := os.WriteFile(f.settings.path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if !f.settings.Load().VerifyPackageListSignature {
			t.Fatalf("default off for %q", contents)
		}
	}
	os.Remove(f.settings.path)
	for _, enabled := range []bool{false, true, false} {
		data, _ := json.Marshal(map[string]bool{"verifyPackageListSignature": enabled})
		rec := f.do("POST", "/api/settings", string(data))
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body)
		}
		if got := NewSettingsStore(filepath.Dir(f.settings.path)).Load(); got.VerifyPackageListSignature != enabled {
			t.Fatal("setting did not persist", got)
		}
		// Updating language through the existing API must preserve this preference.
		f.do("POST", "/api/settings", `{"lang":"zh-CN"}`)
		if f.settings.Load().VerifyPackageListSignature != enabled || f.service.CatalogSnapshot().SignatureCheck != enabled {
			t.Fatal("language update reset signature preference")
		}
		var info app.Info
		json.Unmarshal(f.do("GET", "/api/info", "").Body.Bytes(), &info)
		if info.SignatureCheck != enabled {
			t.Fatal("About reports the wrong mode")
		}
		want := `"verifyPackageListSignature":` + strconv.FormatBool(enabled)
		if !strings.Contains(f.do("GET", "/boot.js", "").Body.String(), want) {
			t.Fatal("boot mode does not match persisted setting")
		}
	}
	// Constructing the shell applies the saved value before its first catalog fetch.
	other := newFixture(t)
	_ = Handler(Deps{Service: other.service, Hub: other.hub, Settings: f.settings, Native: other.native})
	if other.service.Info().SignatureCheck {
		t.Fatal("saved off setting was not loaded")
	}
}

func TestSignatureSettingControlsCatalogAPI(t *testing.T) {
	f, svc, _, _ := guidedFixture(t)
	deb, manifest := relayFiles(t)
	index := relayList(t, svc, deb, manifest).index
	body, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: guiRoundTrip(func(r *http.Request) (*http.Response, error) {
		status, data := 200, body
		if strings.HasSuffix(r.URL.Path, ".minisig") {
			status, data = 404, nil
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
	})}
	dir := t.TempDir()
	source := catalog.NewSource(dir, "test", client)
	source.BaseURLs = []string{"https://fixture.invalid/"}
	svc.ConfigureStorage(dir, filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source)
	if rec := f.do("POST", "/api/catalog/fetch", `{}`); rec.Code != 502 {
		t.Fatal("default mode accepted unsigned index", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/settings", `{"verifyPackageListSignature":false}`); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/catalog/fetch", `{}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"verified":false`) || !strings.Contains(rec.Body.String(), `"reason":"no signature"`) {
		t.Fatal("off mode failed", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/settings", `{"verifyPackageListSignature":true}`); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if state := svc.CatalogSnapshot(); state.Index != nil || !state.SignatureCheck {
		t.Fatal("unverified list remained active after enabling verification", state)
	}
	f.do("POST", "/api/settings", `{"verifyPackageListSignature":false}`)
	if state := svc.CatalogSnapshot(); state.Index == nil || state.Verified || state.Status != "cached/offline" {
		t.Fatal("separate offline cache was not restored", state)
	}
}

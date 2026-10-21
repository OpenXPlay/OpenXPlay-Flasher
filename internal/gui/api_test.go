// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package gui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type fakeNative struct {
	opened []string
	lang   string
}

func (f *fakeNative) OpenURL(url string) error {
	f.opened = append(f.opened, url)
	return nil
}

func (f *fakeNative) SetLang(lang string) { f.lang = lang }

func (f *fakeNative) SaveFile(string, []byte) (string, error) { return "", errNoDialogs }

func (f *fakeNative) OpenFile() (string, []byte, error) { return "", nil, errNoDialogs }

func (f *fakeNative) OpenPackages(string) ([]string, error) { return nil, errNoDialogs }

type fixture struct {
	handler  http.Handler
	hub      *Hub
	native   *fakeNative
	settings *SettingsStore
	service  *app.Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	hub := NewHub()
	native := &fakeNative{}
	settings := NewSettingsStore(t.TempDir())
	svc, err := app.NewForBus(usb.NewFakeBus(), hub)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(Deps{Service: svc, Hub: hub, Settings: settings, Native: native, SystemLang: "zh-Hans-CN"})
	return fixture{handler: h, hub: hub, native: native, settings: settings, service: svc}
}

func (f fixture) do(method, target, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestStaticFilesHaveExplicitTypes(t *testing.T) {
	f := newFixture(t)
	for path, want := range map[string]string{
		"/":                 "text/html; charset=utf-8",
		"/app.js":           "text/javascript; charset=utf-8",
		"/i18n.js":          "text/javascript; charset=utf-8",
		"/settings.js":      "text/javascript; charset=utf-8",
		"/settingslogic.js": "text/javascript; charset=utf-8",
		"/app.css":          "text/css; charset=utf-8",
		"/logo.png":         "image/png",
		"/boot.js":          "text/javascript; charset=utf-8",
		"/api/info":         "application/json",
		"/api/state":        "application/json",
		"/api/profiles":     "application/json",
	} {
		rec := f.do("GET", path, "")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != want {
			t.Errorf("GET %s = %d %q, want 200 %q", path, rec.Code, rec.Header().Get("Content-Type"), want)
		}
	}
	for _, path := range []string{"/api.go", "/../api.go", "/assets/../api.go"} {
		if rec := f.do("GET", path, ""); rec.Code == http.StatusOK {
			t.Errorf("GET %s served a file outside the page", path)
		}
	}
	if rec := f.do("GET", "/api/nothing", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("unknown API = %d %s", rec.Code, rec.Body)
	}
}

func TestInfoIncludesBuildHostAndDataPaths(t *testing.T) {
	f := newFixture(t)
	f.service.SetADB(adb.NewConnector())
	rec := f.do("GET", "/api/info", "")
	var got app.Info
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := f.service.Info()
	want.SettingsFile = f.settings.path
	if rec.Code != http.StatusOK || got != want || got.ADB.KeyDir == "" {
		t.Fatalf("info = %+v, want %+v (status %d)", got, want, rec.Code)
	}
}

func TestBootFollowsSystemThenSavedLanguage(t *testing.T) {
	f := newFixture(t)
	if body := f.do("GET", "/boot.js", "").Body.String(); !strings.Contains(body, `"lang":"zh-CN"`) {
		t.Fatalf("boot.js without a saved language: %s", body)
	}
	if rec := f.do("POST", "/api/settings", `{"lang":"en"}`); rec.Code != http.StatusOK {
		t.Fatalf("save settings = %d %s", rec.Code, rec.Body)
	}
	if body := f.do("GET", "/boot.js", "").Body.String(); !strings.Contains(body, `"lang":"en"`) {
		t.Fatalf("boot.js after choosing English: %s", body)
	}
	if f.native.lang != LangEnglish {
		t.Fatalf("native language %q after choosing English", f.native.lang)
	}
	f.do("POST", "/api/settings", `{"lang":"zh-CN"}`)
	if f.native.lang != LangChinese || appTitle(f.native.lang) != "OpenXPlay 刷机工具" {
		t.Fatalf("native language %q after choosing Chinese", f.native.lang)
	}
	f.do("POST", "/api/settings", `{"lang":"en"}`)
	if rec := f.do("POST", "/api/settings", `{"lang":"fr"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported language accepted: %d", rec.Code)
	}
	if got := f.settings.Load().Lang; got != "en" {
		t.Fatalf("saved language %q", got)
	}
	if rec := f.do("POST", "/api/settings", `{"lang":""}`); rec.Code != http.StatusOK || f.native.lang != LangChinese {
		t.Fatalf("follow system = %d, native language %q", rec.Code, f.native.lang)
	}
	if body := f.do("GET", "/boot.js", "").Body.String(); !strings.Contains(body, `"lang":"zh-CN"`) ||
		!strings.Contains(body, `"saved":""`) || !strings.Contains(body, `"systemLang":"zh-CN"`) {
		t.Fatalf("boot.js after following the system: %s", body)
	}
}

func TestPostsNeedJSON(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(`{"lang":"en"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain POST = %d", rec.Code)
	}
}

func TestOpenOnlyKnownSites(t *testing.T) {
	f := newFixture(t)
	if rec := f.do("POST", "/api/open", `{"url":"https://github.com/OpenXPlay/OpenXPlay-Flasher"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("open project page = %d %s", rec.Code, rec.Body)
	}
	for _, url := range []string{"https://example.com/", "file:///etc/passwd", "https://openxplay.org.evil.test/",
		"http://openxplay.org/", "https://openxplay.org@evil.test/", "https://github.com/OpenXPlay-evil/"} {
		if rec := f.do("POST", "/api/open", `{"url":"`+url+`"}`); rec.Code != http.StatusForbidden {
			t.Errorf("open %s = %d", url, rec.Code)
		}
	}
	if len(f.native.opened) != 1 {
		t.Fatalf("opened %v", f.native.opened)
	}
}

func TestStateLongPoll(t *testing.T) {
	f := newFixture(t)
	done := make(chan State, 1)
	go func() {
		var s State
		_ = json.Unmarshal(f.do("GET", "/api/state?after=0", "").Body.Bytes(), &s)
		done <- s
	}()
	select {
	case s := <-done:
		t.Fatalf("returned before any change: %+v", s)
	case <-time.After(50 * time.Millisecond):
	}
	f.hub.Emit(app.EventDevices, device.Snapshot{Candidates: []device.Candidate{{Mode: device.ModeEDL, Profiles: []string{"ufi003"}}}})
	select {
	case s := <-done:
		if s.Rev != 1 || len(s.Devices.Candidates) != 1 {
			t.Fatalf("state %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("long poll did not wake")
	}

	saved := longPoll
	longPoll = 20 * time.Millisecond
	defer func() { longPoll = saved }()
	var s State
	_ = json.Unmarshal(f.do("GET", "/api/state?after=1", "").Body.Bytes(), &s)
	if s.Rev != 1 {
		t.Fatalf("timed-out poll returned %+v", s)
	}
	if rec := f.do("GET", "/api/state?after=x", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad after = %d", rec.Code)
	}
}

func TestHubIgnoresUnknownEvents(t *testing.T) {
	hub := NewHub()
	hub.Emit("other", 1)
	hub.Emit(app.EventDevices, "not a snapshot")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if s := hub.Wait(ctx, 0); s.Rev != 0 {
		t.Fatalf("rev moved: %+v", s)
	}
}

func TestNativeTextInBothLanguages(t *testing.T) {
	for _, lang := range []string{LangEnglish, LangChinese} {
		m := menuStrings(lang)
		for name, v := range map[string]string{"About": m.About, "Quit": m.Quit, "Help": m.Help, "Website": m.Website, "Source": m.Source,
			"Settings": m.Settings, "Tutorial": m.Tutorial, "Download": m.Download} {
			if v == "" {
				t.Errorf("%s menu %s is empty", lang, name)
			}
		}
		if !canOpen(siteURL(lang)) {
			t.Errorf("%s site URL %s is not openable", lang, siteURL(lang))
		}
	}
	if menuStrings(LangChinese).Quit == menuStrings(LangEnglish).Quit {
		t.Fatal("menu not translated")
	}
}

func TestNormalizeLang(t *testing.T) {
	for in, want := range map[string]string{
		"zh-Hans-CN": LangChinese, "zh_CN.UTF-8": LangChinese, "zh-TW": LangChinese, "zh": LangChinese,
		"en-US": LangEnglish, "en": LangEnglish, "fr-FR": "", "": "", "C": "",
	} {
		if got := NormalizeLang(in); got != want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
	if UILang("", "de-DE") != LangEnglish || UILang("en", "zh-CN") != LangEnglish || UILang("", "zh-CN") != LangChinese {
		t.Fatal("UILang precedence")
	}
}

func TestWebGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := guardWeb(ok, "127.0.0.1:4567", "secret")
	get := func(host, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", target, nil)
		req.Host = host
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("evil.test:4567", "/?key=secret", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("rebinding host admitted: %d", rec.Code)
	}
	if rec := get("127.0.0.1:4567", "/", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no cookie admitted: %d", rec.Code)
	}
	if rec := get("127.0.0.1:4567", "/?key=wrong", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong key admitted: %d", rec.Code)
	}
	rec := get("localhost:4567", "/?key=secret", nil)
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusSeeOther || len(cookies) != 1 || cookies[0].SameSite != http.SameSiteStrictMode || !cookies[0].HttpOnly {
		t.Fatalf("key exchange = %d %+v", rec.Code, cookies)
	}
	if rec := get("127.0.0.1:4567", "/api/state", cookies[0]); rec.Code != http.StatusTeapot {
		t.Fatalf("cookie not admitted: %d", rec.Code)
	}
}

func TestServeWebRefusesNonLoopback(t *testing.T) {
	err := ServeWeb(context.Background(), "0.0.0.0:0", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("ServeWeb on all interfaces: %v", err)
	}
	if err := ServeWeb(context.Background(), "nonsense", io.Discard); err == nil {
		t.Fatal("ServeWeb accepted an address without a port")
	}
}

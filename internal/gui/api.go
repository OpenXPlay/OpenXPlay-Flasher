// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package gui is the desktop shell. One http.Handler serves the embedded page and its JSON API;
// the Wails app plugs it into its windows, and the browser mode (openxplay-flasher web) and the
// tests serve the same handler, so the page never depends on Wails.
package gui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
)

//go:embed assets
var assets embed.FS

// Native is what the page may ask of the program hosting it.
type Native interface {
	OpenURL(url string) error
	// SetLang applies a new UI language to the native window title and menus.
	SetLang(lang string)
	// SaveFile asks where to save data on this computer and writes it there; "" when cancelled.
	SaveFile(name string, data []byte) (string, error)
	// OpenFile asks for a file on this computer and reads it; name is "" when cancelled.
	OpenFile() (name string, data []byte, err error)
	// OpenPackages filters the picker to the selected layer without reading the payloads.
	OpenPackages(layer string) ([]string, error)
	OpenIdentity() ([]string, error)
	OpenBackupFolder() (string, error)
	OpenArtifact() (string, error)
	OpenArtifacts() ([]string, error)
	ShowFolder(path string) error
}

// errNoDialogs: browser mode has no native dialogs; the page uses the browser's own.
var errNoDialogs = errors.New("not available in browser mode")

// Deps are the parts the handler serves.
type Deps struct {
	Service    *app.Service
	Hub        *Hub
	Settings   *SettingsStore
	Native     Native
	SystemLang string
	// Web is true when the page is served to a browser instead of the app's window.
	Web bool
}

// longPoll bounds /api/state?after= so proxies and webviews never see a hung request.
var longPoll = 20 * time.Second

// openable lists the URL prefixes the page may open in the system browser.
var openable = []string{
	"https://openxplay.org/",
	"https://github.com/",
}

// Content types by extension. Go's mime package reads the Windows registry, where .js is
// sometimes text/plain, and a module script with that type does not run.
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".json": "application/json",
}

// Handler serves the page and the API.
func Handler(d Deps) http.Handler {
	d.Service.SetSignatureCheck(context.Background(), d.Settings.Load().VerifyPackageListSignature)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /boot.js", func(w http.ResponseWriter, r *http.Request) {
		settings := d.Settings.Load()
		saved := settings.Lang
		prefs, _ := json.Marshal(map[string]any{
			"lang":                       UILang(saved, d.SystemLang),
			"saved":                      saved,
			"systemLang":                 UILang("", d.SystemLang),
			"links":                      helpLinks,
			"platform":                   runtime.GOOS,
			"web":                        d.Web,
			"verifyPackageListSignature": settings.VerifyPackageListSignature,
		})
		w.Header().Set("Content-Type", contentTypes[".js"])
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "window.bootPrefs = %s;\n", prefs)
	})

	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		info := d.Service.Info()
		info.SettingsFile = d.Settings.path
		writeJSON(w, http.StatusOK, info)
	})

	mux.HandleFunc("GET /api/profiles", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Service.Profiles())
	})

	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		if r.URL.Query().Has("after") && err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "after must be a revision number")
			return
		}
		if !r.URL.Query().Has("after") {
			writeJSON(w, http.StatusOK, d.Hub.State())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), longPoll)
		defer cancel()
		writeJSON(w, http.StatusOK, d.Hub.Wait(ctx, after))
	})

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Settings.Load())
	})

	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		// Merge preference changes as one operation, so changing language cannot undo a
		// simultaneous signature switch in another window.
		var patch struct {
			Lang      *string `json:"lang"`
			Signature *bool   `json:"verifyPackageListSignature"`
		}
		if !readJSON(w, r, &patch) {
			return
		}
		if patch.Lang != nil && *patch.Lang != "" && NormalizeLang(*patch.Lang) != *patch.Lang {
			writeError(w, http.StatusBadRequest, "bad_lang", "unsupported language")
			return
		}
		in, err := d.Settings.Update(func(v *Settings) {
			if patch.Lang != nil {
				v.Lang = *patch.Lang
			}
			if patch.Signature != nil {
				v.VerifyPackageListSignature = *patch.Signature
			}
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "settings_write", err.Error())
			return
		}
		d.Service.SetSignatureCheck(r.Context(), in.VerifyPackageListSignature)
		d.Native.SetLang(UILang(in.Lang, d.SystemLang))
		writeJSON(w, http.StatusOK, in)
	})

	mux.HandleFunc("POST /api/open", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		forward := d.Service.ForwardStatus()
		if !canOpen(in.URL) && !(forward.Active && in.URL == forward.Address) && in.URL != d.Service.InstallSnapshot().NextSteps.AdminURL {
			writeError(w, http.StatusForbidden, "url_not_allowed", "this address cannot be opened")
			return
		}
		if err := d.Native.OpenURL(in.URL); err != nil {
			writeError(w, http.StatusInternalServerError, "open_failed", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	sshRoutes(mux, d)
	configRoutes(mux, d)
	installRoutes(mux, d)
	guidedRoutes(mux, d)
	mux.Handle("/", staticHandler())
	return mux
}

// staticHandler serves the page files, and answers unknown /api/ paths with a JSON error.
func staticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not_found", "no such API")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		data, err := readAsset(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		ctype, ok := contentTypes[path.Ext(name)]
		if !ok {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
}

func canOpen(url string) bool {
	for _, prefix := range openable {
		if strings.HasPrefix(url, prefix) {
			return true
		}
	}
	return false
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	// A JSON content type cannot come from a plain cross-site form, which guards the browser mode.
	if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "send application/json")
		return false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "error": msg})
}

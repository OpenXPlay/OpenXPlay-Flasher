// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/config"
)

// configTimeout bounds one configuration operation; a write that restarts the relay, fails and
// rolls back restarts it twice.
const configTimeout = 90 * time.Second

// configErrors maps errors to the codes the page translates.
var configErrors = []struct {
	err  error
	code string
}{
	{app.ErrNoADB, "no-adb"},
	{app.ErrDeviceBusy, "busy"},
	{app.ErrNoDevice, "no-device"},
	{app.ErrSeveralDevices, "several-devices"},
	{app.ErrNotADB, "not-adb"},
	{app.ErrUnconfirmed, "unconfirmed"},
	{app.ErrNoConfigProfile, "no-config"},
	{config.ErrProtected, "protected"},
	{config.ErrPrivacy, "privacy"},
	{config.ErrNotRegular, "not-file"},
	{config.ErrTooLarge, "too-large"},
	{config.ErrBinary, "binary"},
	{config.ErrBadPath, "bad-path"},
	{adb.ErrNoDevice, "adb-no-device"},
	{adb.ErrBusy, "adb-busy"},
}

func writeConfigError(w http.ResponseWriter, err error) {
	if writeSSHError(w, err) {
		return
	}
	var invalid *config.PrivateValidationError
	if errors.As(err, &invalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"code": "private-invalid", "error": invalid.Error(), "errors": invalid.Fields})
		return
	}
	for _, e := range configErrors {
		if errors.Is(err, e.err) {
			writeError(w, http.StatusConflict, e.code, err.Error())
			return
		}
	}
	writeError(w, http.StatusBadGateway, "device", err.Error())
}

func configRoutes(mux *http.ServeMux, d Deps) {
	privacyRoutes(mux, d)
	op := func(r *http.Request) (context.Context, context.CancelFunc) {
		return context.WithTimeout(r.Context(), configTimeout)
	}

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		t, err := d.Service.ConfigTarget()
		if err != nil {
			writeConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	})

	mux.HandleFunc("POST /api/config/read", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path string `json:"path"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		ctx, cancel := op(r)
		defer cancel()
		f, err := d.Service.ConfigRead(ctx, in.Path)
		if err != nil {
			writeConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	})

	mux.HandleFunc("POST /api/config/write", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Restart bool   `json:"restart"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		ctx, cancel := op(r)
		defer cancel()
		res, err := d.Service.ConfigWrite(ctx, in.Path, in.Content, in.Restart)
		if err != nil {
			if errors.Is(err, config.ErrTooLarge) || errors.Is(err, config.ErrBinary) || errors.Is(err, config.ErrProtected) {
				writeConfigError(w, err)
				return
			}
			var invalid *config.InvalidError
			if errors.As(err, &invalid) {
				writeError(w, http.StatusUnprocessableEntity, "invalid", invalid.Error())
				return
			}
			writeConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("POST /api/config/backups", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path string `json:"path"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		ctx, cancel := op(r)
		defer cancel()
		list, err := d.Service.ConfigBackups(ctx, in.Path)
		if err != nil {
			writeConfigError(w, err)
			return
		}
		if list == nil {
			list = []config.Backup{}
		}
		writeJSON(w, http.StatusOK, list)
	})

	mux.HandleFunc("POST /api/config/restore", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path    string `json:"path"`
			ID      string `json:"id"`
			Restart bool   `json:"restart"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		ctx, cancel := op(r)
		defer cancel()
		res, err := d.Service.ConfigRestore(ctx, in.Path, in.ID, in.Restart)
		if err != nil {
			writeConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("POST /api/config/export", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		ctx, cancel := op(r)
		defer cancel()
		if err := d.Service.ConfigCheckRaw(ctx, in.Path); err != nil {
			writeConfigError(w, err)
			return
		}
		saved, err := d.Native.SaveFile(path.Base(in.Path), []byte(in.Content))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"saved": saved})
	})

	mux.HandleFunc("POST /api/config/import", func(w http.ResponseWriter, r *http.Request) {
		name, data, err := d.Native.OpenFile()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "import", err.Error())
			return
		}
		if name == "" {
			writeJSON(w, http.StatusOK, map[string]string{"name": ""})
			return
		}
		if len(data) > config.MaxSize {
			writeConfigError(w, config.ErrTooLarge)
			return
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			writeConfigError(w, config.ErrBinary)
			return
		}
		// A trusted-HTTPS import must use the private endpoint, never return its token.
		var obj map[string]json.RawMessage
		if json.Unmarshal(data, &obj) == nil && (obj["token"] != nil || obj["agreeLetsEncryptTerms"] != nil) {
			writeConfigError(w, config.ErrPrivacy)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": name, "content": string(data)})
	})
}

// readPrivateJSON intentionally never returns JSON decoder diagnostics or submitted keys.
func readPrivateJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if strings.TrimSpace(mt) != "application/json" {
		writeError(w, 415, "bad_request", "send application/json")
		return false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, 400, "bad_request", "invalid private request")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeError(w, 400, "bad_request", "invalid private request")
		return false
	}
	return true
}

func privacyRoutes(mux *http.ServeMux, d Deps) {
	for _, action := range []string{"read", "write", "import", "remove", "backups", "restore"} {
		mux.HandleFunc("POST /api/config/privacy/"+action, func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				ID string `json:"id"`
				config.PublicNameUpdate
				Content string `json:"content"`
				Backup  string `json:"backup"`
				Native  bool   `json:"native"`
			}
			if !readPrivateJSON(w, r, &in) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), configTimeout)
			defer cancel()
			data := []byte(in.Content)
			if action == "import" && in.Native {
				name, b, err := d.Native.OpenFile()
				if err != nil {
					writeConfigError(w, config.ErrPrivateOperation)
					return
				}
				if name == "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				data = b
			}
			v, backups, err := d.Service.ConfigPrivacy(ctx, in.ID, action, in.PublicNameUpdate, data, in.Backup)
			if err != nil {
				writeConfigError(w, err)
				return
			}
			if action == "backups" {
				if backups == nil {
					backups = []config.Backup{}
				}
				writeJSON(w, 200, backups)
				return
			}
			writeJSON(w, 200, v)
		})
	}
}

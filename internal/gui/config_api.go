// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path"
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
	for _, e := range configErrors {
		if errors.Is(err, e.err) {
			writeError(w, http.StatusConflict, e.code, err.Error())
			return
		}
	}
	writeError(w, http.StatusBadGateway, "device", err.Error())
}

func configRoutes(mux *http.ServeMux, d Deps) {
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
		writeJSON(w, http.StatusOK, map[string]string{"name": name, "content": string(data)})
	})
}

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func writeInstallError(w http.ResponseWriter, err error) {
	var problem *stage.Problem
	if errors.As(err, &problem) {
		writeError(w, http.StatusConflict, problem.Code, problem.Detail)
		return
	}
	if errors.Is(err, stage.ErrPackage) {
		writeError(w, http.StatusUnprocessableEntity, "package", err.Error())
		return
	}
	writeConfigError(w, err)
}

func installRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /api/install", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Service.Install(r.Context()))
	})
	mux.HandleFunc("POST /api/install/packages", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path  string `json:"path"`
			Layer string `json:"layer"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.InstallAddForLayer(in.Layer, in.Path)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/install/pick", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Layer string `json:"layer"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if len(stage.PackageFileTypes(in.Layer)) == 0 {
			writeError(w, http.StatusBadRequest, "wrong-layer", in.Layer)
			return
		}
		if d.Web {
			writeError(w, http.StatusConflict, "no-dialogs", "enter a local package path")
			return
		}
		if state := d.Service.InstallSnapshot(); state.Job != nil && state.Job.Status == "running" {
			writeError(w, http.StatusConflict, "busy", "a device operation is already running")
			return
		}
		paths, err := d.Native.OpenPackages(in.Layer)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "picker", err.Error())
			return
		}
		for _, path := range paths {
			if _, err := d.Service.InstallAddForLayer(in.Layer, path); err != nil {
				writeInstallError(w, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, d.Service.InstallSnapshot())
	})
	mux.HandleFunc("DELETE /api/install/packages/{id}", func(w http.ResponseWriter, r *http.Request) {
		// Deletions use JSON too, keeping the same cross-site request guard as other writes.
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.InstallRemove(r.PathValue("id"))
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/install/plan", func(w http.ResponseWriter, r *http.Request) {
		var in stage.Request
		if !readJSON(w, r, &in) {
			return
		}
		choice, err := d.Service.InstallPlan(in)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, choice)
	})

	mux.HandleFunc("POST /api/install/start", func(w http.ResponseWriter, r *http.Request) {
		var in stage.Request
		if !readJSON(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		state, err := d.Service.InstallStart(ctx, in)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, state)
	})
	mux.HandleFunc("POST /api/install/prepare", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Choice string `json:"choice"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.InstallPrepare(r.Context(), in.Choice)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, d.Service.CatalogSnapshot()) })
	mux.HandleFunc("POST /api/catalog/fetch", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Channel string `json:"channel"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.CatalogFetch(r.Context(), in.Channel)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path string `json:"path"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.ImportFile(r.Context(), in.Path)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/devices/designate-stock", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Location string `json:"location"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.DesignateStock(r.Context(), in.Location)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})

	mux.HandleFunc("GET /api/install/log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(d.Service.InstallLog()))
	})
	mux.HandleFunc("POST /api/install/log/save", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		saved, err := d.Native.SaveFile("openxplay-install.log", []byte(d.Service.InstallLog()))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"saved": saved})
	})
}

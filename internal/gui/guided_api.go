// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"net/http"
	"time"
)

func guidedRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("POST /api/install/retry", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Started time.Time `json:"started"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.InstallRetry(r.Context(), in.Started)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, state)
	})
	mux.HandleFunc("GET /api/artifacts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Service.InstallSnapshot().Artifacts)
	})

	for _, action := range []string{"import", "pick"} {
		mux.HandleFunc("POST /api/artifacts/"+action, func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Choice   string   `json:"choice"`
				Layer    string   `json:"layer"`
				Role     string   `json:"role"`
				Path     string   `json:"path"`
				Paths    []string `json:"paths"`
				Multiple bool     `json:"multiple"`
			}
			if !readJSON(w, r, &in) {
				return
			}
			if action == "pick" {
				if d.Web {
					writeError(w, http.StatusConflict, "no-dialogs", "")
					return
				}
				var err error
				if in.Multiple {
					in.Paths, err = d.Native.OpenArtifacts()
				} else {
					in.Path, err = d.Native.OpenArtifact()
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, "picker", "")
					return
				}
			}
			if in.Path != "" {
				in.Paths = append(in.Paths, in.Path)
			}
			if len(in.Paths) == 0 {
				writeJSON(w, http.StatusOK, d.Service.InstallSnapshot())
				return
			}
			if !in.Multiple && len(in.Paths) == 1 {
				state, err := d.Service.ImportArtifact(r.Context(), in.Layer, in.Role, in.Paths[0])
				if err != nil {
					writeInstallError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, state)
				return
			}
			state, err := d.Service.SelectFiles(r.Context(), in.Choice, in.Layer, in.Role, in.Paths)
			if err != nil {
				writeInstallError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, state)
		})
	}
	mux.HandleFunc("POST /api/artifacts/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		writeJSON(w, http.StatusOK, d.Service.CancelFileCheck())
	})

	mux.HandleFunc("POST /api/backups/pick", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Serial string `json:"serial"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if d.Web {
			writeError(w, http.StatusConflict, "no-dialogs", "")
			return
		}
		dir, err := d.Native.OpenBackupFolder()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "picker", "")
			return
		}
		if dir == "" {
			writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
			return
		}
		result, err := d.Service.BackupOperation(r.Context(), "import", in.Serial, dir)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /api/backups/{serial}/show-folder", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		folder, err := d.Service.BackupFolder(r.Context(), r.PathValue("serial"))
		if err != nil {
			writeInstallError(w, err)
			return
		}
		if err = d.Native.ShowFolder(folder); err != nil {
			writeError(w, http.StatusInternalServerError, "open-folder", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/identity", func(w http.ResponseWriter, r *http.Request) {
		state, err := d.Service.IdentityStatus(r.Context())
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/identity/pick", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		if d.Web {
			writeError(w, http.StatusConflict, "no-dialogs", "")
			return
		}
		paths, err := d.Native.OpenIdentity()
		// Only paths are returned to assemble consecutive selections; contents never leave
		// the service, and neither picker errors nor credential paths go into diagnostics.
		if err != nil {
			writeError(w, http.StatusInternalServerError, "picker", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"paths": paths})
	})
	mux.HandleFunc("POST /api/identity/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Paths []string `json:"paths"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.IdentityImport(r.Context(), in.Paths)
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("GET /api/admin/forward", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Service.ForwardStatus())
	})
	mux.HandleFunc("POST /api/admin/forward/start", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		state, err := d.Service.ForwardStart(r.Context())
		if err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/admin/forward/stop", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		writeJSON(w, http.StatusOK, d.Service.ForwardStop())
	})
}

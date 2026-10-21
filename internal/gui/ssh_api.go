// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"context"
	"errors"
	"net/http"
	"time"

	sshdev "github.com/OpenXPlay/OpenXPlay-Flasher/internal/ssh"
)

// RecentHost is the only persisted SSH form data. Passwords and passphrases are deliberately
// absent, including from settings and all API responses.
type RecentHost struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Mode string `json:"mode"`
}

func writeSSHError(w http.ResponseWriter, err error) bool {
	var host *sshdev.HostKeyError
	if errors.As(err, &host) {
		code := "ssh-host-unknown"
		if host.Changed {
			code = "ssh-host-changed"
		}
		writeJSON(w, http.StatusConflict, map[string]any{"code": code, "host": host.Host, "fingerprint": host.Fingerprint, "keyType": host.KeyType})
		return true
	}
	var pass *sshdev.KeyPassphraseError
	if errors.As(err, &pass) {
		writeError(w, http.StatusConflict, "ssh-key-passphrase", "")
		return true
	}
	var connection *sshdev.ConnectionError
	if errors.As(err, &connection) {
		writeError(w, http.StatusConflict, "ssh-"+connection.Kind, "")
		return true
	}
	var sudo *sshdev.SudoError
	if errors.As(err, &sudo) {
		writeError(w, http.StatusConflict, "ssh-sudo-"+sudo.Kind, "")
		return true
	}
	return false
}
func sshRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /api/connection", func(w http.ResponseWriter, r *http.Request) {
		hosts, err := d.Service.SSHStoredHosts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "ssh-known-hosts", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connection": d.Service.Connection(), "recent": d.Settings.Load().RecentHosts, "knownHosts": hosts})
	})
	mux.HandleFunc("POST /api/devices/select", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Profile string `json:"profile"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if err := d.Service.SelectProfile(in.Profile); err != nil {
			writeInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d.Service.Install(r.Context()))
	})
	mux.HandleFunc("POST /api/ssh/connect", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Mode       string `json:"mode"`
			Host       string `json:"host"`
			Port       int    `json:"port"`
			User       string `json:"user"`
			Password   string `json:"password"`
			KeyFile    string `json:"keyFile"`
			Passphrase string `json:"passphrase"`
			Accept     string `json:"acceptHostKey"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		state, err := d.Service.SSHConnect(ctx, in.Mode, sshdev.Options{Host: in.Host, Port: in.Port, User: in.User, Password: in.Password, KeyFile: in.KeyFile, Passphrase: in.Passphrase, AcceptHostKey: in.Accept})
		in.Password, in.Passphrase = "", ""
		if err != nil {
			if !writeSSHError(w, err) {
				writeInstallError(w, err)
			}
			return
		}
		c := state.Target.Device
		// Merge under the store lock so a simultaneous language change does not lose preferences.
		if err = d.Settings.RememberHost(RecentHost{Host: c.Host, Port: c.Port, User: in.User, Mode: in.Mode}); err != nil {
			writeError(w, http.StatusInternalServerError, "settings_write", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/ssh/forget", func(w http.ResponseWriter, r *http.Request) {
		var in RecentHost
		if !readJSON(w, r, &in) {
			return
		}
		if err := d.Service.SSHForget(in.Host, in.Port); err != nil {
			writeInstallError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/ssh/disconnect", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		if err := d.Service.SSHDisconnect(); err != nil {
			writeInstallError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/ssh/key/pick", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		if d.Web {
			writeError(w, http.StatusConflict, "no-dialogs", "")
			return
		}
		// This optional capability keeps existing native hosts and test doubles source-compatible.
		picker, ok := d.Native.(interface{ OpenSSHKey() (string, error) })
		if !ok {
			writeError(w, http.StatusConflict, "picker", "")
			return
		}
		file, err := picker.OpenSSHKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "picker", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": file})
	})
}

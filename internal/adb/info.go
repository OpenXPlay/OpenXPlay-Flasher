// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import "path/filepath"

// ConnectionInfo describes the configured route and, for automatic routing, the last successful
// connection. Reading it never probes the server or opens USB; no connection need be active.
type ConnectionInfo struct {
	Mode          string `json:"mode"`
	ServerAddr    string `json:"serverAddr,omitempty"`
	KeyDir        string `json:"keyDir,omitempty"`
	LastTransport string `json:"lastTransport,omitempty"`
}

func (a *AutoConnector) Info() ConnectionInfo {
	a.mu.Lock()
	last := a.lastTransport
	a.mu.Unlock()
	return ConnectionInfo{Mode: "auto", ServerAddr: a.Server.Info().ServerAddr,
		KeyDir: a.USB.Info().KeyDir, LastTransport: last}
}

func (s ServerConnector) Info() ConnectionInfo {
	addr := s.Addr
	if addr == "" {
		addr = DefaultServerAddr
	}
	return ConnectionInfo{Mode: "server", ServerAddr: addr}
}

func (u USBConnector) Info() ConnectionInfo {
	key := u.KeyPath
	if key == "" {
		key, _ = defaultKeyPath()
	}
	dir := ""
	if key != "" {
		dir = filepath.Dir(key)
	}
	return ConnectionInfo{Mode: "usb", KeyDir: dir}
}

func (a *AutoConnector) used(transport string) {
	a.mu.Lock()
	a.lastTransport = transport
	a.mu.Unlock()
}

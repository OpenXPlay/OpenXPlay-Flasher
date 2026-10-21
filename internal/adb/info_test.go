// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func TestConnectionInfoDoesNotConnectOrCreateKeys(t *testing.T) {
	dir := t.TempDir()
	a := &AutoConnector{Server: ServerConnector{Addr: "127.0.0.1:12345"}, USB: USBConnector{
		KeyPath: filepath.Join(dir, "keys", "adbkey"),
		Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) {
			t.Fatal("reading info opened USB")
			return nil, nil
		},
	}}
	info := a.Info()
	if info.Mode != "auto" || info.ServerAddr != a.Server.Addr || info.KeyDir != filepath.Join(dir, "keys") || info.LastTransport != "" {
		t.Fatalf("info = %+v", info)
	}
	if _, err := os.Stat(info.KeyDir); !os.IsNotExist(err) {
		t.Fatalf("info created the key folder: %v", err)
	}
	if got := (ServerConnector{}).Info(); got.Mode != "server" || got.ServerAddr != DefaultServerAddr {
		t.Fatalf("server defaults: %+v", got)
	}
	key, err := defaultKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if got := (USBConnector{}).Info(); got.Mode != "usb" || got.KeyDir != filepath.Dir(key) {
		t.Fatalf("USB defaults: %+v", got)
	}
}

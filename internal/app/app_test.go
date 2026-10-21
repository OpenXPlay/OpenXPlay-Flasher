// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package app

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"
)

type recorder struct {
	mu     sync.Mutex
	events []string
	ch     chan device.Snapshot
}

func (r *recorder) Emit(name string, data any) {
	r.mu.Lock()
	r.events = append(r.events, name)
	r.mu.Unlock()
	r.ch <- data.(device.Snapshot)
}

func TestServicePushesDeviceChanges(t *testing.T) {
	bus := usb.NewFakeBus()
	rec := &recorder{ch: make(chan device.Snapshot, 8)}
	s, err := NewForBus(bus, rec)
	if err != nil {
		t.Fatal(err)
	}
	s.interval = 5 * time.Millisecond
	s.Start(context.Background())
	s.Start(context.Background())
	defer s.Stop()

	next := func() device.Snapshot {
		t.Helper()
		select {
		case snap := <-rec.ch:
			return snap
		case <-time.After(2 * time.Second):
			t.Fatal("no device event")
		}
		return device.Snapshot{}
	}
	if snap := next(); len(snap.Candidates) != 0 {
		t.Fatalf("empty bus reported %+v", snap)
	}
	bus.Set(usb.DeviceInfo{VendorID: 0x05c6, ProductID: 0x9008, Bus: 1, Address: 2})
	if snap := next(); len(snap.Candidates) != 1 || snap.Candidates[0].Mode != device.ModeEDL || !snap.Candidates[0].Supported() {
		t.Fatalf("EDL device not reported: %+v", snap)
	}
	if got := s.Devices(); len(got.Candidates) != 1 {
		t.Fatalf("Devices() = %+v", got)
	}
	s.Stop()
	s.Stop()
	if rec.events[0] != EventDevices {
		t.Fatalf("event name %q", rec.events[0])
	}
}

func TestInfo(t *testing.T) {
	oldVersion, oldCommit, oldDate := version.Version, version.Commit, version.BuildDate
	version.Version, version.Commit, version.BuildDate = "1.2.3", "abc1234", "2026-10-21T10:00:00Z"
	t.Cleanup(func() { version.Version, version.Commit, version.BuildDate = oldVersion, oldCommit, oldDate })
	s, _ := NewForBus(usb.NewFakeBus(), &recorder{})
	if len(s.Profiles()) == 0 {
		t.Fatal("no builtin profiles")
	}
	info := s.Info()
	if info.Name != version.Name || info.Version != version.Version || info.Commit != version.Commit ||
		info.BuildDate != version.BuildDate || info.License != version.License || info.SourceURL != version.SourceURL ||
		info.Platform != runtime.GOOS || info.Architecture != runtime.GOARCH {
		t.Fatalf("incomplete info %+v", info)
	}
	if (runtime.GOOS == "darwin" || runtime.GOOS == "windows") && info.OSVersion == "" {
		t.Fatal("missing host OS version")
	}
	version.BuildDate = ""
	if info := s.Info(); info.BuildDate != "" || info.ADB.Mode != "" {
		t.Fatalf("invented optional information: %+v", info)
	}
	s.SetADB(adb.NewConnector())
	if info := s.Info(); info.ADB.Mode != "auto" || info.ADB.ServerAddr != adb.DefaultServerAddr || info.ADB.KeyDir == "" || info.ADB.LastTransport != "" {
		t.Fatalf("connector info: %+v", info.ADB)
	}
}

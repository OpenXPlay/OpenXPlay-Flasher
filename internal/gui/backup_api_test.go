// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
)

func TestBackupMetadataRoutes(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	f.service.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), nil, nil)
	sets := []string{"20261021T010203Z", "20261021T010204Z"}
	for i, set := range sets {
		b := base.Backup{Schema: base.BackupSchema, Set: set, Profile: "ufi003", Serial: "board1", Created: time.Date(2026, 10, 21, 1, 2, 3+i, 0, time.UTC), FlasherVersion: "test", Partitions: map[string]base.Partition{}}
		for _, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
			b.Partitions[name] = base.Partition{Size: 64, SHA256: strings.Repeat("a", 64), Source: "oem-dump"}
		}
		path := filepath.Join(dir, "backups", "ufi003-board1", set)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "backup.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The repair captured another set, but restores the original job's safe source.
	j := base.Journal{Schema: base.JournalSchema, Profile: "ufi003", Serial: "board1", Plan: "repair", Started: time.Now(), Updated: time.Now(), CapturedSet: sets[1], RestoreSet: sets[0], InFlight: "restore-fsc"}
	if err := (base.Journals{Dir: filepath.Join(dir, "data")}).Save(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/backups", "/api/backups/board1"} {
		rec := f.do("GET", path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		var listings []app.BackupListing
		if path == "/api/backups" {
			if err := json.Unmarshal(rec.Body.Bytes(), &listings); err != nil {
				t.Fatal(err)
			}
		} else {
			var listing app.BackupListing
			if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
				t.Fatal(err)
			}
			listings = []app.BackupListing{listing}
		}
		if len(listings) != 1 || len(listings[0].Sets) != 2 || listings[0].Sets[0].Set != sets[1] || listings[0].RepairSet != sets[0] || listings[0].Sets[0].Partitions["fsg"].ShortSHA256 != "aaaaaaaaaaaa" {
			t.Fatalf("wrong metadata: %s", rec.Body)
		}
	}
	if rec := f.do("POST", "/api/backups/import", `{"dir":"missing","serial":"../bad"}`); rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "bad-serial" {
		t.Fatalf("unsafe import: %d %s", rec.Code, rec.Body)
	}
}

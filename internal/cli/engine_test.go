// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func engineFixture(t *testing.T) (*app.Service, *cliDevice, string) {
	t.Helper()
	var ignored bytes.Buffer
	svc, dev, file := cliInstallFixture(t, &ignored)
	dir := t.TempDir()
	source := catalog.NewSource(dir, "test", nil)
	source.BaseURLs = nil
	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source)
	old := commandService
	commandService = func(bool, app.Events) (*app.Service, func(), error) { return svc, func() { svc.Stop() }, nil }
	t.Cleanup(func() { commandService = old })
	return svc, dev, file
}

func TestCLIBackupSetsAndRepairSource(t *testing.T) {
	svc, dev, _ := engineFixture(t)
	dir := t.TempDir()
	source := catalog.NewSource(filepath.Join(dir, "data"), "test", nil)
	source.BaseURLs = nil
	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source)
	sets := []string{"20261021T010203Z", "20261021T010204Z"}
	for i, set := range sets {
		b := base.Backup{Schema: base.BackupSchema, Set: set, Profile: "ufi003", Serial: "board1", Created: time.Date(2026, 10, 21, 1, 2, 3+i, 0, time.UTC), FlasherVersion: "test", Partitions: map[string]base.Partition{}}
		for _, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
			b.Partitions[name] = base.Partition{Size: 64, SHA256: strings.Repeat("a", 64), Source: "imported"}
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
	j := base.Journal{Schema: base.JournalSchema, Profile: "ufi003", Serial: "board1", Plan: "stock", Started: time.Now(), Updated: time.Now(), CapturedSet: sets[0], InFlight: "chain-rpm"}
	if err := (base.Journals{Dir: filepath.Join(dir, "data")}).Save(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"list", "show"} {
		for _, asJSON := range []bool{false, true} {
			args := []string{operation}
			if operation == "show" {
				args = append(args, "board1")
			}
			if asJSON {
				args = append(args, "--json")
			}
			var out, errOut bytes.Buffer
			if code := runEngineCLI("backup", args, nil, &out, &errOut); code != 0 {
				t.Fatalf("exit %d: %s", code, &errOut)
			}
			if asJSON {
				var listings []app.BackupListing
				if operation == "list" {
					if err := json.Unmarshal(out.Bytes(), &listings); err != nil {
						t.Fatal(err)
					}
				} else {
					var listing app.BackupListing
					if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
						t.Fatal(err)
					}
					listings = []app.BackupListing{listing}
				}
				if len(listings) != 1 || len(listings[0].Sets) != 2 || listings[0].Sets[0].Set != sets[1] || listings[0].RepairSet != sets[0] {
					t.Fatal("wrong JSON metadata", out.String())
				}
			} else {
				text := out.String()
				if !strings.Contains(text, "修复使用的备份集: "+sets[0]) || !strings.Contains(text, "modemst1: 64") || !strings.Contains(text, "aaaaaaaaaaaa") || strings.Contains(text, strings.Repeat("a", 64)) || strings.Index(text, "  "+sets[1]+" ") > strings.Index(text, "  "+sets[0]+" ") {
					t.Fatal("incomplete or unsorted display", text)
				}
			}
		}
	}
	if dev.installs != 0 {
		t.Fatal("backup listing wrote to a device")
	}
}
func TestCLIPlanJSONNeverWrites(t *testing.T) {
	_, dev, file := engineFixture(t)
	var out, errOut bytes.Buffer
	code := runEngineCLI("plan", []string{"--choice", "relay", file, "--json"}, strings.NewReader(""), &out, &errOut)
	var choice stage.Choice
	if code != 0 || json.Unmarshal(out.Bytes(), &choice) != nil || choice.ID != "relay" || len(choice.Steps) == 0 || dev.installs != 0 {
		t.Fatalf("exit %d out %s err %s", code, &out, &errOut)
	}
}
func TestCLIInstallJSONAndLocalCompatibility(t *testing.T) {
	_, dev, file := engineFixture(t)
	var out, errOut bytes.Buffer
	code := runEngineCLI("install", []string{"--only", "relay", file, "--json", "--yes"}, strings.NewReader(""), &out, &errOut)
	var state app.InstallState
	if code != 0 || json.Unmarshal(out.Bytes(), &state) != nil || state.Job == nil || state.Job.Status != "succeeded" || dev.installs != 1 {
		t.Fatalf("exit %d out %s err %s", code, &out, &errOut)
	}
}
func TestCLIBaseNeedsTypedSerial(t *testing.T) {
	_, dev, _ := engineFixture(t)
	wantCode := "typed-serial"
	if !usb.TransfersSupported() {
		wantCode = "fastboot-unsupported"
	}
	var out, errOut bytes.Buffer
	code := runEngineCLI("install", []string{"--choice", "base", "--json", "--yes"}, strings.NewReader(""), &out, &errOut)
	var failure struct {
		Code string `json:"code"`
	}
	if code != 1 || json.Unmarshal(out.Bytes(), &failure) != nil || failure.Code != wantCode || dev.installs != 0 {
		t.Fatalf("exit %d out %s err %s", code, &out, &errOut)
	}
}
func TestCLIEngineArguments(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		args []string
	}{{"catalog", nil}, {"catalog", []string{"wrong"}}, {"download", []string{"--layer", "unknown"}}, {"backup", []string{"import", "folder"}}, {"fastboot", []string{"flash", "boot"}}, {"plan", []string{"--choice", "wrong"}}} {
		t.Run(tc.cmd+strings.Join(tc.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := runEngineCLI(tc.cmd, tc.args, nil, &out, &errOut); code != 2 {
				t.Fatalf("exit %d", code)
			}
		})
	}
	want := []string{"--serial", "board1", "--json", "import", "folder"}
	got := interspersed([]string{"import", "folder", "--serial", "board1", "--json"})
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("parsed %v", got)
	}
}

func TestDownloadCommandRemovedAndHelpDescribesLocalFiles(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"download", "--layer", "relay"}, &out, &errOut); code != 2 {
		t.Fatalf("removed command exit %d", code)
	}
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"help"}, &out, &errOut); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(out.String(), "download --layer") || !strings.Contains(out.String(), "Packages are never downloaded") || !strings.Contains(out.String(), "不复制文件") {
		t.Fatal("help still describes downloads")
	}
	if !strings.Contains(out.String(), "--no-signature-check") || !strings.Contains(out.String(), "软件包列表签名") {
		t.Fatal("signature flag is missing from help")
	}
}

func TestCLISignatureFlagIsPerCommand(t *testing.T) {
	for _, cmd := range []string{"catalog", "import", "backup", "plan", "install"} {
		t.Run(cmd, func(t *testing.T) {
			svc, _, file := engineFixture(t)
			args := map[string][]string{
				"catalog": {"fetch"}, "import": {file}, "backup": {"list"},
				"plan": {"--choice", "relay", file}, "install": {"--only", "relay", "--yes", file},
			}[cmd]
			args = append(args, "--no-signature-check")
			var out, errOut bytes.Buffer
			code := runEngineCLI(cmd, args, strings.NewReader(""), &out, &errOut)
			if code == 2 || svc.Info().SignatureCheck {
				t.Fatalf("flag was not applied: %d %s", code, errOut.String())
			}
			if cmd == "install" && (code != 0 || !strings.Contains(svc.InstallLog(), "signature check: off")) {
				t.Fatalf("install did not record off mode: %d %s", code, errOut.String())
			}
			out.Reset()
			errOut.Reset()
			runEngineCLI("catalog", []string{"show"}, nil, &out, &errOut)
			if !svc.Info().SignatureCheck {
				t.Fatal("flag persisted into the next command")
			}
		})
	}
}

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func engineFixture(t *testing.T) (*app.Service, *cliDevice, string) {
	t.Helper()
	var ignored bytes.Buffer
	svc, dev, file := cliInstallFixture(t, &ignored)
	dir := t.TempDir()
	source := catalog.NewSource(dir, "test", nil)
	source.BaseURLs = nil
	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source, nil)
	old := commandService
	commandService = func(bool, app.Events) (*app.Service, func(), error) { return svc, func() { svc.Stop() }, nil }
	t.Cleanup(func() { commandService = old })
	return svc, dev, file
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
	var out, errOut bytes.Buffer
	code := runEngineCLI("install", []string{"--choice", "base", "--json", "--yes"}, strings.NewReader(""), &out, &errOut)
	if code != 1 || !strings.Contains(out.String(), "typed-serial") || dev.installs != 0 {
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

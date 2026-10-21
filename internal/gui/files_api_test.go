// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func relayList(t *testing.T, svc *app.Service, deb, manifest string) *guiCatalog {
	t.Helper()
	artifacts := []catalog.Artifact{}
	for i, path := range []string{deb, manifest} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, catalog.Artifact{Role: []string{"deb", "manifest"}[i], Name: filepath.Base(path), Size: int64(len(data)), SHA256: digestBytes(data), URLs: []string{"https://github.com/OpenXPlay/OpenXPlay-Relay/releases/download/test/" + filepath.Base(path)}})
	}
	source := &guiCatalog{index: &catalog.Index{Schema: catalog.Schema, Channel: "beta", Sequence: 4, Generated: "2026-10-21T00:00:00Z", Devices: map[string]catalog.Device{"ufi003": {Recommended: map[string]string{"relay": "0.1.5~beta.1"}, Layers: []catalog.Layer{{Layer: "relay", Version: "0.1.5~beta.1", Artifacts: artifacts}}}}}}
	dir := t.TempDir()
	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source)
	if _, err := svc.CatalogFetch(t.Context(), "beta"); err != nil {
		t.Fatal(err)
	}
	return source
}
func decodeFilesState(t *testing.T, body []byte) app.InstallState {
	t.Helper()
	var state app.InstallState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
func choiceByID(t *testing.T, state app.InstallState, id string) stage.Choice {
	t.Helper()
	for _, c := range state.Choices {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("choice missing: %s", id)
	return stage.Choice{}
}

func TestSeveralFilesAssignByHashWithShuffledNamesAndReportUnmatched(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "paths", true: "native"}[native], func(t *testing.T) {
			f, svc, _, n := guidedFixture(t)
			deb, manifest := relayFiles(t)
			relayList(t, svc, deb, manifest)
			dir := t.TempDir()
			paths := []string{filepath.Join(dir, "payload with wrong name"), filepath.Join(dir, "not a manifest"), filepath.Join(dir, "unrelated")}
			for i, original := range []string{manifest, deb} {
				data, _ := os.ReadFile(original)
				if err := os.WriteFile(paths[i], data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			os.WriteFile(paths[2], []byte("unmatched fixture"), 0o600)
			body := map[string]any{"choice": "relay", "multiple": true, "paths": paths}
			endpoint := "/api/artifacts/import"
			if native {
				endpoint = "/api/artifacts/pick"
				n.artifacts = paths
				delete(body, "paths")
			}
			rec := f.do("POST", endpoint, jsonBody(body))
			if rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			state := decodeFilesState(t, rec.Body.Bytes())
			state = svc.Install(t.Context())
			c := choiceByID(t, state, "relay")
			if !c.Ready || len(c.Needs[0].Artifacts) != 2 {
				t.Fatalf("assignment not ready: %+v", c.Needs)
			}
			for _, a := range c.Needs[0].Artifacts {
				expected := paths[1]
				if a.Role == "manifest" {
					expected = paths[0]
				}
				if a.Path != expected || !a.Verified || len(a.URLs) != 2 {
					t.Fatalf("row %+v", a)
				}
			}
			if len(state.FileResults) != 3 {
				t.Fatalf("results %+v", state.FileResults)
			}
			unmatched := state.FileResults[2]
			if unmatched.Problem == nil || unmatched.Problem.Code != "unmatched-file" || !strings.Contains(unmatched.Problem.Detail, "17 B") || !strings.Contains(unmatched.Problem.Detail, digestBytes([]byte("unmatched fixture"))[:12]) {
				t.Fatalf("unclear unmatched result %+v", unmatched)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(state.Artifacts[0].Path), "cache")); !os.IsNotExist(err) {
				t.Fatal("unexpected cache")
			}
		})
	}
}

func TestCatalogUnknownRelayRefusedAndOfflineImportRechecked(t *testing.T) {
	f, svc, _, _ := guidedFixture(t)
	deb, manifest := relayFiles(t)
	source := relayList(t, svc, deb, manifest)
	metadata, _ := os.ReadFile(manifest)
	var m stage.Manifest
	json.Unmarshal(metadata, &m)
	data := []byte("!<arch>\nunknown relay fixture")
	m.SHA256 = digestBytes(data)
	metadata, _ = json.Marshal(m)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "renamed-payload"), filepath.Join(dir, "renamed-sidecar")}
	os.WriteFile(paths[0], data, 0o600)
	os.WriteFile(paths[1], metadata, 0o600)
	pick := func() app.InstallState {
		rec := f.do("POST", "/api/artifacts/import", jsonBody(map[string]any{"choice": "relay", "multiple": true, "paths": paths}))
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return decodeFilesState(t, rec.Body.Bytes())
	}
	state := pick()
	if len(state.Packages) != 0 || len(state.FileResults) != 2 {
		t.Fatal("unknown relay accepted")
	}
	for _, r := range state.FileResults {
		if r.Problem == nil || r.Problem.Code != "catalog-unknown" || !strings.Contains(r.Problem.Detail, "0.1.5~beta.1") {
			t.Fatalf("missing listed versions %+v", r)
		}
	}
	source.index = nil
	source.err = os.ErrNotExist
	svc.CatalogFetch(t.Context(), "beta")
	state = pick()
	state = svc.Install(t.Context())
	c := choiceByID(t, state, "relay")
	if !c.Ready || !c.Needs[0].Offline || c.Needs[0].Package == nil {
		t.Fatal("manifest-only offline import refused")
	}
	source.index = relayListIndex(t, deb, manifest)
	source.err = nil
	svc.CatalogFetch(t.Context(), "beta")
	c = choiceByID(t, svc.InstallSnapshot(), "relay")
	if c.Ready || c.Needs[0].Problem == nil || c.Needs[0].Problem.Code != "catalog-unknown" {
		t.Fatal("offline package bypassed catalog")
	}
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "catalog-unknown" {
		t.Fatalf("start bypassed catalog: %d %s", rec.Code, rec.Body)
	}
}
func relayListIndex(t *testing.T, deb, manifest string) *catalog.Index {
	t.Helper()
	pkg, err := stage.Open(deb)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := []catalog.Artifact{}
	for i, path := range []string{deb, manifest} {
		data, _ := os.ReadFile(path)
		artifacts = append(artifacts, catalog.Artifact{Role: []string{"deb", "manifest"}[i], Name: filepath.Base(path), Size: int64(len(data)), SHA256: digestBytes(data)})
	}
	return &catalog.Index{Devices: map[string]catalog.Device{"ufi003": {Recommended: map[string]string{"relay": pkg.Manifest().Version}, Layers: []catalog.Layer{{Layer: "relay", Version: pkg.Manifest().Version, Artifacts: artifacts}}}}}
}

func TestNoCatalogBasePinsAndRelayManifests(t *testing.T) {
	f, svc, _, _ := guidedFixture(t)
	deb, manifest := relayFiles(t)
	p := svc.Profiles()[0]
	data := []byte("pinned fake boot")
	pin := p.Base.Archives["boot"]
	pin.Size = int64(len(data))
	pin.SHA256 = digestBytes(data)
	p.Base.Archives["boot"] = pin
	path := filepath.Join(t.TempDir(), "any boot filename")
	os.WriteFile(path, data, 0o600)
	rec := f.do("POST", "/api/artifacts/import", jsonBody(map[string]any{"multiple": true, "paths": []string{deb, manifest, path}}))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	state := svc.Install(t.Context())
	base := choiceByID(t, state, "base").Needs[0]
	if len(base.Artifacts) != 3 {
		t.Fatal("base pins absent")
	}
	for _, a := range base.Artifacts {
		if a.Role == "boot" && (!a.Verified || a.Path != path || !slices.Contains(a.URLs, p.Base.Source)) {
			t.Fatalf("pin %+v", a)
		}
	}
	relay := choiceByID(t, state, "relay").Needs[0]
	if !relay.Offline || !relay.Ready {
		t.Fatal("offline note or manifest-only readiness absent")
	}
	if rec := f.do("POST", "/api/artifacts/cancel", `{}`); rec.Code != 200 {
		t.Fatal("cancel route missing")
	}
}

func TestChangedSelectedRelayIsRefusedBeforeUpload(t *testing.T) {
	f, svc, d, _ := guidedFixture(t)
	deb, _ := relayFiles(t)
	if _, err := svc.ImportFile(t.Context(), deb); err != nil {
		t.Fatal(err)
	}
	d.installDevice.onClock = func() {
		data, _ := os.ReadFile(deb)
		data = bytes.Clone(data)
		data[0] ^= 1
		os.WriteFile(deb, data, 0o600)
	}
	rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`)
	if rec.Code != 202 {
		t.Fatalf("start %d %s", rec.Code, rec.Body)
	}
	state, err := svc.InstallWait(t.Context())
	if err != nil || state.Job.Status != "failed" || state.Job.Problem.Code != "file-changed" || d.pushes != 0 {
		t.Fatalf("changed file used: %+v %v", state.Job, err)
	}
}

func TestIdentityPEMSendsDERAndClearsSentBuffers(t *testing.T) {
	f, _, d, _ := guidedFixture(t)
	original := identityFiles(t)
	paths := []string{filepath.Join(t.TempDir(), "certificate without extension"), filepath.Join(t.TempDir(), "private without extension")}
	for i, source := range []string{original[1], original[0]} {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		label := "CMS"
		if i == 1 {
			label = "PRIVATE KEY"
		}
		if err = os.WriteFile(paths[i], pem.EncodeToMemory(&pem.Block{Type: label, Bytes: data}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rec := f.do("POST", "/api/identity/import", jsonBody(map[string]any{"paths": paths}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"identity":"present"`) {
		t.Fatalf("PEM: %d %s", rec.Code, rec.Body)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.credentialViews) != 2 {
		t.Fatal("missing DER uploads")
	}
	for _, data := range d.credentialViews {
		if !bytes.Equal(data, make([]byte, len(data))) {
			t.Fatal("credential buffer was not zeroed")
		}
	}
}

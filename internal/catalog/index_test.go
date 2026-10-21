// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func testIndex() *Index {
	return &Index{Schema: Schema, Channel: "beta", Sequence: 1, Generated: "2026-10-22T00:00:00Z", Devices: map[string]Device{
		"ufi003": {Recommended: map[string]string{"base": "openstick-v1"}, Layers: []Layer{{Layer: "base", Version: "openstick-v1", Source: "https://example.org/v1", Artifacts: []Artifact{{Role: "boot", Name: "boot.img", Size: 3, SHA256: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", URLs: []string{"https://example.org/boot.img"}}}}}},
	}}
}

func indexBytes(t *testing.T, i *Index) []byte {
	t.Helper()
	b, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIndexValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Index)
	}{
		{"schema", func(i *Index) { i.Schema = "org.openxplay.flasher-catalog/2" }},
		{"channel empty", func(i *Index) { i.Channel = "" }},
		{"channel path", func(i *Index) { i.Channel = "../beta" }},
		{"channel uppercase", func(i *Index) { i.Channel = "Beta" }},
		{"channel length", func(i *Index) { i.Channel = strings.Repeat("a", 65) }},
		{"sequence", func(i *Index) { i.Sequence = 0 }},
		{"time", func(i *Index) { i.Generated = "2026-10-22" }},
		{"time zone", func(i *Index) { i.Generated = "2026-10-22T00:00:00" }},
		{"time single digit hour", func(i *Index) { i.Generated = "2026-10-22T0:00:00Z" }},
		{"time comma fraction", func(i *Index) { i.Generated = "2026-10-22T00:00:00,5Z" }},
		{"time invalid zone hour", func(i *Index) { i.Generated = "2026-10-22T00:00:00+24:00" }},
		{"time invalid zone minute", func(i *Index) { i.Generated = "2026-10-22T00:00:00+00:60" }},
		{"devices empty", func(i *Index) { i.Devices = nil }},
		{"device ID", func(i *Index) { i.Devices["../ufi003"] = i.Devices["ufi003"] }},
		{"no layers", func(i *Index) { d := i.Devices["ufi003"]; d.Layers = nil; i.Devices["ufi003"] = d }},
		{"layer ID", func(i *Index) { i.Devices["ufi003"].Layers[0].Layer = "other" }},
		{"empty version", func(i *Index) { i.Devices["ufi003"].Layers[0].Version = "" }},
		{"control version", func(i *Index) { i.Devices["ufi003"].Layers[0].Version = "1\n2" }},
		{"duplicate version", func(i *Index) {
			d := i.Devices["ufi003"]
			d.Layers = append(d.Layers, d.Layers[0])
			i.Devices["ufi003"] = d
		}},
		{"no artifacts", func(i *Index) { i.Devices["ufi003"].Layers[0].Artifacts = nil }},
		{"duplicate role", func(i *Index) { l := &i.Devices["ufi003"].Layers[0]; l.Artifacts = append(l.Artifacts, l.Artifacts[0]) }},
		{"recommend version", func(i *Index) { i.Devices["ufi003"].Recommended["base"] = "missing" }},
		{"recommend layer", func(i *Index) { i.Devices["ufi003"].Recommended["other"] = "openstick-v1" }},
		{"no recommendations", func(i *Index) { d := i.Devices["ufi003"]; d.Recommended = nil; i.Devices["ufi003"] = d }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := testIndex()
			tc.change(i)
			_, err := Decode(indexBytes(t, i))
			var invalid *InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want InvalidError", err)
			}
			if tc.name == "schema" && !strings.Contains(err.Error(), "update the Flasher") {
				t.Fatal(err)
			}
		})
	}
	i := testIndex()
	l := i.Devices["ufi003"].Layers[0]
	l.Version = "0.1.5~beta.1"
	d := i.Devices["ufi003"]
	d.Layers = append([]Layer{l}, d.Layers...)
	i.Devices["ufi003"] = d
	if _, err := Decode(indexBytes(t, i)); err != nil {
		t.Fatal(err)
	}
	if l, ok := d.Lookup("base", "openstick-v1"); !ok || l.Version != "openstick-v1" {
		t.Fatal("lookup depended on order")
	}
	if _, ok := d.Lookup("base", "absent"); ok {
		t.Fatal("missing version found")
	}
}

func TestArtifactValidation(t *testing.T) {
	base := testIndex().Artifacts()[0]
	cases := []struct {
		name   string
		change func(*Artifact)
	}{
		{"role", func(a *Artifact) { a.Role = "../boot" }},
		{"empty name", func(a *Artifact) { a.Name = "" }},
		{"dot", func(a *Artifact) { a.Name = "." }},
		{"dot dot", func(a *Artifact) { a.Name = "boot..img" }},
		{"slash", func(a *Artifact) { a.Name = "dir/file" }},
		{"backslash", func(a *Artifact) { a.Name = `dir\file` }},
		{"NUL", func(a *Artifact) { a.Name = "a\x00b" }},
		{"windows stream", func(a *Artifact) { a.Name = "a:b" }},
		{"windows device", func(a *Artifact) { a.Name = "con.zip" }},
		{"windows trailing dot", func(a *Artifact) { a.Name = "boot." }},
		{"zero size", func(a *Artifact) { a.Size = 0 }},
		{"negative size", func(a *Artifact) { a.Size = -1 }},
		{"oversize", func(a *Artifact) { a.Size = MaxArtifactSize + 1 }},
		{"short hash", func(a *Artifact) { a.SHA256 = "ab" }},
		{"uppercase hash", func(a *Artifact) { a.SHA256 = strings.ToUpper(a.SHA256) }},
		{"bad hex", func(a *Artifact) { a.SHA256 = strings.Repeat("g", 64) }},
		{"no URLs", func(a *Artifact) { a.URLs = nil }},
		{"five URLs", func(a *Artifact) {
			a.URLs = []string{base.URLs[0], base.URLs[0], base.URLs[0], base.URLs[0], base.URLs[0]}
		}},
		{"http", func(a *Artifact) { a.URLs = []string{"http://example.org/file"} }},
		{"relative URL", func(a *Artifact) { a.URLs = []string{"/file"} }},
		{"no host", func(a *Artifact) { a.URLs = []string{"https:///file"} }},
		{"bad URL", func(a *Artifact) { a.URLs = []string{"https://example.org/%zz"} }},
		{"credentials", func(a *Artifact) { a.URLs = []string{"https://user:pass@example.org/a"} }},
		{"fragment", func(a *Artifact) { a.URLs = []string{"https://example.org/a#b"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			tc.change(&a)
			var invalid *InvalidError
			if err := a.Validate(); !errors.As(err, &invalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
	base.Size = MaxArtifactSize
	base.Name = "openxplay-relay_0.1.5~beta.1_arm64.deb"
	base.URLs = []string{"https://example.org/" + base.Name}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	// Go keeps '~' in both parsed and requested URLs; GitHub does not require escaping it.
	u, err := url.Parse(base.URLs[0])
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.String() != base.URLs[0] || !strings.Contains(r.URL.EscapedPath(), "~") {
		t.Fatal(r.URL)
	}
}

func TestStrictDecode(t *testing.T) {
	good := string(indexBytes(t, testIndex()))
	for _, data := range []string{
		`null`, `{`, good + ` {}`, strings.Replace(good, `"schema":`, `"unknown":1,"schema":`, 1),
		strings.Replace(good, `"role":`, `"unknown":1,"role":`, 1),
		strings.Replace(good, `"schema":`, `"channel":"other","schema":`, 1),
		strings.Replace(good, `"sequence":1`, `"sequence":-1`, 1),
		strings.Replace(good, `"sequence":1`, `"sequence":1.5`, 1),
		strings.Replace(good, `"size":3`, `"size":null`, 1),
	} {
		var invalid *InvalidError
		if _, err := Decode([]byte(data)); !errors.As(err, &invalid) {
			t.Errorf("accepted %s: %v", data, err)
		}
	}
	if _, err := Decode([]byte(strings.Repeat(" ", MaxIndexSize+1))); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
}

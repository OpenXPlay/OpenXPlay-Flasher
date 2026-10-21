// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package catalog verifies release indexes and downloads the files they describe. Trust comes
// from the embedded signing keys; a URL or a file in the cache is never evidence of authenticity.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	Schema                 = "org.openxplay.flasher-catalog/1"
	MaxIndexSize           = 1 << 20
	MaxSignatureSize       = 4 << 10
	MaxArtifactSize  int64 = 2 << 30
)

var (
	ErrTooLarge  = errors.New("catalog: input exceeds its size limit")
	ErrRollback  = errors.New("catalog: an older catalog was refused")
	ErrIntegrity = errors.New("catalog: file size or SHA-256 does not match")
	ErrNoMatch   = errors.New("catalog: this file is not an artifact of the current index")
)

// InvalidError identifies a field that cannot be used safely or is not understood.
type InvalidError struct{ Field, Reason string }

func (e *InvalidError) Error() string { return fmt.Sprintf("catalog: %s: %s", e.Field, e.Reason) }

// RollbackError includes the sequence already accepted on this computer.
type RollbackError struct {
	Channel           string
	Sequence, Highest uint64
}

func (e *RollbackError) Error() string {
	return fmt.Sprintf("%v: %s sequence %d, already accepted %d", ErrRollback, e.Channel, e.Sequence, e.Highest)
}
func (e *RollbackError) Unwrap() error { return ErrRollback }

// IntegrityError explains why an artifact failed verification.
type IntegrityError struct{ Name, Reason string }

func (e *IntegrityError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrIntegrity, e.Name, e.Reason)
}
func (e *IntegrityError) Unwrap() error { return ErrIntegrity }

// Index is one channel's signed release inventory. Versions are opaque, not sortable numbers.
type Index struct {
	Schema    string            `json:"schema"`
	Channel   string            `json:"channel"`
	Sequence  uint64            `json:"sequence"`
	Generated string            `json:"generated"`
	Devices   map[string]Device `json:"devices"`
}

// Device lists independent layers and the recommended version of each.
type Device struct {
	Recommended map[string]string `json:"recommended"`
	Layers      []Layer           `json:"layers"`
}

// Layer is one version of a layer, with localized release notes.
type Layer struct {
	Layer     string            `json:"layer"`
	Version   string            `json:"version"`
	Source    string            `json:"source"`
	Notes     map[string]string `json:"notes,omitempty"`
	Artifacts []Artifact        `json:"artifacts"`
}

// Artifact describes bytes, independently of the mirror used to obtain them.
type Artifact struct {
	Role   string   `json:"role"`
	Name   string   `json:"name"`
	Size   int64    `json:"size"`
	SHA256 string   `json:"sha256"`
	URLs   []string `json:"urls"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var rfc3339 = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

func validID(s string) bool    { return len(s) <= 64 && identifier.MatchString(s) }
func validLayer(s string) bool { return s == "base" || s == "device" || s == "relay" }

// Decode accepts exactly one index, without unknown fields or trailing JSON values.
func Decode(data []byte) (*Index, error) {
	if len(data) > MaxIndexSize {
		return nil, ErrTooLarge
	}
	var index Index
	if err := DecodeStrict(data, &index); err != nil {
		return nil, err
	}
	if err := index.Validate(); err != nil {
		return nil, err
	}
	return &index, nil
}

// DecodeStrict is shared by the maintainer's source reader and the client. Duplicate object
// keys are refused too, so another JSON implementation cannot see a different signed index.
func DecodeStrict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := checkJSONValue(d); err != nil {
		return &InvalidError{"JSON", err.Error()}
	}
	if _, err := d.Token(); err != io.EOF {
		return &InvalidError{"JSON", "expected one JSON value"}
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return &InvalidError{"JSON", err.Error()}
	}
	return nil
}

func checkJSONValue(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate or invalid object key %q", key)
			}
			seen[s] = true
			if err := checkJSONValue(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			if err := checkJSONValue(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}

// Validate checks the format before either publishing an index or using its paths and URLs.
func (i *Index) Validate() error {
	bad := func(field, why string) error { return &InvalidError{field, why} }
	if i == nil {
		return bad("index", "missing object")
	}
	if i.Schema != Schema {
		return bad("schema", "unsupported catalog schema; update the Flasher")
	}
	if !validID(i.Channel) {
		return bad("channel", "expected a lowercase channel name")
	}
	if i.Sequence < 1 {
		return bad("sequence", "must be at least 1")
	}
	if _, err := time.Parse(time.RFC3339, i.Generated); err != nil || !rfc3339.MatchString(i.Generated) {
		return bad("generated", "expected an RFC 3339 timestamp")
	}
	if len(i.Devices) == 0 {
		return bad("devices", "must not be empty")
	}
	for id, dev := range i.Devices {
		if !validID(id) {
			return bad("devices", "invalid device ID: "+id)
		}
		if len(dev.Layers) == 0 {
			return bad(id, "must list at least one layer")
		}
		versions := make(map[string]map[string]bool)
		for _, layer := range dev.Layers {
			if !validLayer(layer.Layer) {
				return bad(id+".layer", "unknown layer: "+layer.Layer)
			}
			if strings.TrimSpace(layer.Version) == "" || strings.ContainsFunc(layer.Version, unicode.IsControl) {
				return bad(id+".version", "expected a nonempty version without control characters")
			}
			if versions[layer.Layer] == nil {
				versions[layer.Layer] = make(map[string]bool)
			}
			if versions[layer.Layer][layer.Version] {
				return bad(id+".version", "duplicate version of "+layer.Layer)
			}
			versions[layer.Layer][layer.Version] = true
			if len(layer.Artifacts) == 0 {
				return bad(id+".artifacts", "must not be empty")
			}
			roles := make(map[string]bool)
			for _, a := range layer.Artifacts {
				if err := a.Validate(); err != nil {
					return err
				}
				if roles[a.Role] {
					return bad(id+".artifacts", "duplicate role: "+a.Role)
				}
				roles[a.Role] = true
			}
		}
		if len(dev.Recommended) == 0 {
			return bad(id+".recommended", "must not be empty")
		}
		for layer, version := range dev.Recommended {
			if !versions[layer][version] {
				return bad(id+".recommended", "unknown layer/version: "+layer+"/"+version)
			}
		}
	}
	return nil
}

// Validate checks an artifact even when it was supplied separately from an index.
func (a Artifact) Validate() error {
	bad := func(why string) error { return &InvalidError{"artifact " + a.Name, why} }
	if !validID(a.Role) {
		return bad("invalid role")
	}
	if a.Name == "" || a.Name == "." || strings.Contains(a.Name, "..") || strings.ContainsAny(a.Name, `/\:*?"<>|`) || strings.ContainsFunc(a.Name, unicode.IsControl) || strings.TrimRight(a.Name, ". ") != a.Name {
		return bad("name must be a plain file name without '..'")
	}
	// These names address devices on Windows, even when they have an extension.
	stem := strings.ToUpper(strings.SplitN(a.Name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		return bad("name is reserved on Windows")
	}
	if a.Size <= 0 || a.Size > MaxArtifactSize {
		return bad("size must be between 1 byte and 2 GiB")
	}
	if !digest.MatchString(a.SHA256) {
		return bad("SHA-256 must be 64 lowercase hexadecimal characters")
	}
	if len(a.URLs) < 1 || len(a.URLs) > 4 {
		return bad("expected 1–4 HTTPS URLs")
	}
	for _, raw := range a.URLs {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
			return bad("invalid HTTPS URL: " + raw)
		}
	}
	return nil
}

// Lookup finds an exact version without assuming any ordering of the layer entries.
func (d Device) Lookup(layer, version string) (Layer, bool) {
	for _, entry := range d.Layers {
		if entry.Layer == layer && entry.Version == version {
			return entry, true
		}
	}
	return Layer{}, false
}

// Artifacts visits every artifact, including versions other than the recommendations.
func (i *Index) Artifacts() []Artifact {
	var out []Artifact
	for _, d := range i.Devices {
		for _, l := range d.Layers {
			out = append(out, l.Artifacts...)
		}
	}
	return out
}

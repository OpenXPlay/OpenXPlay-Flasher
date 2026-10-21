// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Package profile describes the device models the flasher supports. A profile is data: the USB
// signatures the model shows in each mode and the ordered layers installed on it. Supporting a new
// device starts with a new file in profiles/.
//
// Layers stack from the bottom: a full install writes every layer in order, and an update writes
// one layer on its own when the layers below it are in place (usually only the relay, the top
// layer, changes). Destructive layers erase the device and need a verified backup first; they are
// always the lowest ones.
package profile

import (
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Mode is the USB transport a device offers.
type Mode string

const (
	ModeADB      Mode = "adb"
	ModeFastboot Mode = "fastboot"
	ModeEDL      Mode = "edl"
)

// Text is a string per UI language; "en" and "zh-CN" are required.
type Text map[string]string

// USBMatch is the VID:PID a model shows in one mode, in hex as lsusb prints it.
type USBMatch struct {
	Mode      Mode   `json:"mode"`
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
}

// IDs returns the parsed vendor and product IDs.
func (m USBMatch) IDs() (vendor, product uint16, err error) {
	v, err := strconv.ParseUint(m.VendorID, 16, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("vendorId %q: %w", m.VendorID, err)
	}
	p, err := strconv.ParseUint(m.ProductID, 16, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("productId %q: %w", m.ProductID, err)
	}
	return uint16(v), uint16(p), nil
}

// Layer is one installable part of a device's system, e.g. base, device or relay.
type Layer struct {
	ID   string `json:"id"`
	Name Text   `json:"name"`
	// Transport is the mode the layer is written in.
	Transport Mode `json:"transport"`
	// Destructive layers erase the device; they need a verified backup before any write.
	Destructive bool `json:"destructive"`
}

// ConfigFile is a configuration file on the device that the configuration window offers.
type ConfigFile struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	// PrivatePaths lists extra private roots or filename prefixes ending in *.
	PrivatePaths []string `json:"privatePaths,omitempty"`
	Path         string   `json:"path"`
	Name         Text     `json:"name"`
	// Format is "json", "text" or the typed privacy format "public-name".
	Format string `json:"format"`
	// RestartService: the profile's service reads this file at start, so a write restarts it.
	RestartService bool `json:"restartService"`
}

// Profile is one supported device model.
type Profile struct {
	ID   string `json:"id"`
	Name Text   `json:"name"`
	// Family groups models that share USB IDs, so USB alone cannot tell them apart; the flasher
	// confirms the model over adb or fastboot before writing.
	Family string     `json:"family"`
	USB    []USBMatch `json:"usb"`
	Layers []Layer    `json:"layers"`
	// Service is the systemd unit of the relay on the device, restarted after configuration writes.
	Service string       `json:"service,omitempty"`
	Config  []ConfigFile `json:"config,omitempty"`
}

// Layer returns the layer with the given ID.
func (p Profile) Layer(id string) (Layer, bool) {
	i := slices.IndexFunc(p.Layers, func(l Layer) bool { return l.ID == id })
	if i < 0 {
		return Layer{}, false
	}
	return p.Layers[i], true
}

// Below returns the layers under the given one, bottom first: what must be in place before that
// layer can be written on its own.
func (p Profile) Below(id string) []Layer {
	i := slices.IndexFunc(p.Layers, func(l Layer) bool { return l.ID == id })
	if i < 0 {
		return nil
	}
	return slices.Clone(p.Layers[:i])
}

//go:embed profiles/*.json
var builtin embed.FS

// Builtin returns the profiles shipped in the binary, sorted by ID.
func Builtin() ([]Profile, error) {
	return Load(builtin, "profiles")
}

// Load reads and validates every *.json profile in dir.
func Load(fsys fs.FS, dir string) ([]Profile, error) {
	names, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var p Profile
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if want := p.ID + ".json"; path.Base(name) != want {
			return nil, fmt.Errorf("%s: file must be named %s", name, want)
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Profile) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// Validate checks a profile's structure.
func (p Profile) Validate() error {
	if p.ID == "" || p.Family == "" {
		return fmt.Errorf("id and family are required")
	}
	if err := p.Name.validate("name"); err != nil {
		return err
	}
	if len(p.USB) == 0 {
		return fmt.Errorf("profile %s: no USB signatures", p.ID)
	}
	for _, m := range p.USB {
		if !validMode(m.Mode) {
			return fmt.Errorf("profile %s: unknown USB mode %q", p.ID, m.Mode)
		}
		if _, _, err := m.IDs(); err != nil {
			return fmt.Errorf("profile %s: %w", p.ID, err)
		}
	}
	if len(p.Layers) == 0 {
		return fmt.Errorf("profile %s: no layers", p.ID)
	}
	seen := map[string]bool{}
	destructiveDone := false
	for _, l := range p.Layers {
		if l.ID == "" || seen[l.ID] {
			return fmt.Errorf("profile %s: empty or repeated layer id %q", p.ID, l.ID)
		}
		seen[l.ID] = true
		if err := l.Name.validate("layer " + l.ID + " name"); err != nil {
			return err
		}
		if !validMode(l.Transport) {
			return fmt.Errorf("profile %s: layer %s: unknown transport %q", p.ID, l.ID, l.Transport)
		}
		if l.Destructive && destructiveDone {
			return fmt.Errorf("profile %s: destructive layer %s sits above a non-destructive one", p.ID, l.ID)
		}
		destructiveDone = destructiveDone || !l.Destructive
	}
	return p.validateConfig()
}

func (t Text) validate(what string) error {
	for _, lang := range []string{"en", "zh-CN"} {
		if t[lang] == "" {
			return fmt.Errorf("%s: missing %s text", what, lang)
		}
	}
	return nil
}

var serviceName = regexp.MustCompile(`^[A-Za-z0-9@._-]+\.service$`)

func (p Profile) validateConfig() error {
	if p.Service != "" && !serviceName.MatchString(p.Service) {
		return fmt.Errorf("profile %s: service %q is not a systemd service name", p.ID, p.Service)
	}
	seen := map[string]bool{}
	for _, c := range p.Config {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("profile %s: empty or repeated config id %q", p.ID, c.ID)
		}
		seen[c.ID] = true
		if !path.IsAbs(c.Path) || path.Clean(c.Path) != c.Path {
			return fmt.Errorf("profile %s: config %s: path %q must be absolute and clean", p.ID, c.ID, c.Path)
		}
		if c.Category != "system" && c.Category != "user" && c.Category != "privacy" {
			return fmt.Errorf("profile %s: config %s: unknown category %q", p.ID, c.ID, c.Category)
		}
		if (c.Category == "privacy") != (c.Format == "public-name") {
			return fmt.Errorf("profile %s: config %s: privacy requires a typed public-name form", p.ID, c.ID)
		}
		for _, root := range c.PrivatePaths {
			clean := strings.TrimSuffix(root, "*")
			if c.Category != "privacy" || !path.IsAbs(clean) || path.Clean(clean) != clean || strings.ContainsAny(clean, "*?[]\x00\n\r") {
				return fmt.Errorf("profile %s: invalid private path", p.ID)
			}
		}
		if c.Format != "json" && c.Format != "text" && c.Format != "public-name" {
			return fmt.Errorf("profile %s: config %s: unknown format %q", p.ID, c.ID, c.Format)
		}
		if c.RestartService && p.Service == "" {
			return fmt.Errorf("profile %s: config %s restarts the service, but the profile names none", p.ID, c.ID)
		}
		if err := c.Name.validate("config " + c.ID + " name"); err != nil {
			return err
		}
	}
	return nil
}

func validMode(m Mode) bool {
	return m == ModeADB || m == ModeFastboot || m == ModeEDL
}

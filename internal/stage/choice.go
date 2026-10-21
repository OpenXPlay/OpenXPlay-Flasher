// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"maps"
	"slices"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

// PackageProvider resolves already checked payloads, without doing I/O during planning. Picked
// files checked against a catalog can both supply this boundary; neither defines a choice.
type PackageProvider interface {
	ForLayer(string) (*Package, *Problem)
}

type PackageSet []*Package

func (packages PackageSet) ForLayer(layer string) (*Package, *Problem) {
	var found *Package
	for _, pkg := range packages {
		if pkg != nil && pkg.manifest.Stage == layer {
			if found != nil {
				return nil, &Problem{"duplicate-layer", layer}
			}
			found = pkg
		}
	}
	return found, nil
}

// ChoiceDefinition is independent of packages and device state. Layers and Needs are ordered
// bottom-up. Supporting a new executor (including a destructive base) starts with its recipe.
type ChoiceDefinition struct {
	ID          string   `json:"id"`
	Layers      []string `json:"layers"`
	Needs       []string `json:"needs"`
	Destructive bool     `json:"destructive"`
}

type ArtifactNeed struct {
	URLs           []string  `json:"urls"`
	Path           string    `json:"path,omitempty"`
	Status         string    `json:"status"`
	BytesPerSecond float64   `json:"bytesPerSecond"`
	Updated        time.Time `json:"updated,omitempty"`
	Role           string    `json:"role"`
	Name           string    `json:"name"`
	Size           int64     `json:"size"`
	SHA256         string    `json:"sha256"`
	Source         string    `json:"source"`
	Done           int64     `json:"done"`
	Total          int64     `json:"total"`
	Verified       bool      `json:"verified"`
	Error          *Problem  `json:"error,omitempty"`
}

type PackageNeed struct {
	Offline    bool           `json:"offline"`
	Artifacts  []ArtifactNeed `json:"artifacts,omitempty"`
	Layer      string         `json:"layer"`
	FileTypes  []string       `json:"fileTypes"`
	Status     string         `json:"status"` // missing, checked, unusable
	Package    *PackageInfo   `json:"package,omitempty"`
	Comparison string         `json:"comparison,omitempty"` // new, upgrade, same, downgrade
	Problem    *Problem       `json:"problem,omitempty"`
	Ready      bool           `json:"ready"`
}

type Choice struct {
	BasePlan       string        `json:"basePlan,omitempty"`
	Name           profile.Text  `json:"name,omitempty"`
	Reason         profile.Text  `json:"reason,omitempty"`
	Serial         string        `json:"serial,omitempty"`
	BackupRequired bool          `json:"backupRequired"`
	ID             string        `json:"id"`
	Layers         []string      `json:"layers"`
	Needs          []PackageNeed `json:"needs"`
	Destructive    bool          `json:"destructive"`
	Steps          []Step        `json:"steps"`
	Problem        *Problem      `json:"problem,omitempty"` // device state only, never packages
	Ready          bool          `json:"ready"`
	Downgrade      bool          `json:"downgrade"`
}

type LayerRow struct {
	KernelRelease string       `json:"kernelRelease,omitempty"`
	Guide         string       `json:"guide,omitempty"`
	ReadOnly      bool         `json:"readOnly,omitempty"`
	PrettyName    string       `json:"prettyName,omitempty"`
	Layer         string       `json:"layer"`
	Name          profile.Text `json:"name"`
	Installed     string       `json:"installed"`
	Action        string       `json:"action"` // guide, install, update
	ActionName    profile.Text `json:"actionName,omitempty"`
	Choice        string       `json:"choice,omitempty"`
	Enabled       bool         `json:"enabled"`
	Reason        *Problem     `json:"reason,omitempty"`
}

type layerRecipe struct {
	fileTypes   []string
	actions     []string
	deferVerify bool
	actionName  profile.Text
}

// Stage recipes cover adb layers. The app composes destructive choices with the base executor.
var layerRecipes = map[string]layerRecipe{
	"device": {fileTypes: []string{".tar.gz"}, actions: []string{"requirements", "clock", "prepare", "push", "checksum", "unpack", "preflight", "install", "reboot", "wait", "verify"}, deferVerify: true},
	"relay":  {fileTypes: []string{".deb", ".manifest.json"}, actions: []string{"requirements", "clock", "prepare", "push", "checksum", "install", "verify"}},
}

func PackageFileTypes(layer string) []string {
	if layer == "base" {
		return []string{".zip", ".img"}
	}
	return slices.Clone(layerRecipes[layer].fileTypes)
}

func ChoiceDefinitions(p profile.Profile) []ChoiceDefinition {
	full := ChoiceDefinition{ID: "full", Layers: []string{}, Needs: []string{}}
	choices := []ChoiceDefinition{}
	for _, layer := range p.Layers {
		if _, ok := layerRecipes[layer.ID]; !ok {
			continue
		}
		full.Layers, full.Needs = append(full.Layers, layer.ID), append(full.Needs, layer.ID)
		full.Destructive = full.Destructive || layer.Destructive
		choices = append(choices, ChoiceDefinition{layer.ID, []string{layer.ID}, []string{layer.ID}, layer.Destructive})
	}
	return append([]ChoiceDefinition{full}, choices...)
}

// DescribeChoice builds the scope, preview and dependency problem from the profile and installed
// layers alone. A selected lower layer satisfies the dependency even before its package exists.
func DescribeChoice(p profile.Profile, installed Facts, def ChoiceDefinition) Choice {
	c := Choice{ID: def.ID, Layers: slices.Clone(def.Layers), Destructive: def.Destructive, Needs: []PackageNeed{}, Steps: []Step{}}
	for _, id := range def.Needs {
		c.Needs = append(c.Needs, PackageNeed{Layer: id, FileTypes: slices.Clone(layerRecipes[id].fileTypes), Status: "missing"})
	}
	available := maps.Clone(installed.Layers)
	if available == nil {
		available = map[string]string{}
	}
	if available["base"] == "" && !slices.Contains(def.Layers, "base") {
		c.Problem = &Problem{"base-missing", ""}
	}
	deferred := []Step{}
	for i, id := range def.Layers {
		for _, below := range p.Below(id) {
			if available[below.ID] == "" && c.Problem == nil {
				c.Problem = &Problem{"below-missing", below.ID}
			}
		}
		for _, action := range layerRecipes[id].actions {
			step := Step{Layer: id, Action: action, Resumable: true}
			if action == "verify" && layerRecipes[id].deferVerify && i < len(def.Layers)-1 {
				deferred = append(deferred, step)
			} else {
				c.Steps = append(c.Steps, step)
			}
		}
		available[id] = "planned"
	}
	c.Steps = append(c.Steps, deferred...)
	if len(c.Steps) > 0 {
		c.Steps = append(c.Steps, Step{Layer: c.Steps[len(c.Steps)-1].Layer, Action: "installed", Resumable: true})
	}
	return c
}

// PrepareChoice checks only the declared needs. Unrelated packages cannot affect a choice.
// Earlier usable packages supply their declared Debian packages to later requirement checks.
func PrepareChoice(p profile.Profile, installed Facts, c Choice, provider PackageProvider, downgrade bool) Choice {
	c.Needs = slices.Clone(c.Needs)
	c.Downgrade = false
	f := installed
	f.Layers, f.Packages = maps.Clone(f.Layers), maps.Clone(f.Packages)
	if f.Layers == nil {
		f.Layers = map[string]string{}
	}
	if f.Packages == nil {
		f.Packages = map[string]string{}
	}
	c.Ready = c.Problem == nil && len(c.Needs) > 0
	for i := range c.Needs {
		n := &c.Needs[i]
		*n = PackageNeed{Layer: n.Layer, FileTypes: slices.Clone(n.FileTypes), Status: "missing"}
		pkg, problem := provider.ForLayer(n.Layer)
		n.Problem = problem
		if pkg != nil {
			info, m := pkg.Info(), pkg.manifest
			n.Package = &info
			n.Comparison = "new"
			if old := installed.Layers[n.Layer]; old != "" {
				switch CompareVersions(m.Version, old) {
				case -1:
					n.Comparison = "downgrade"
				case 0:
					n.Comparison = "same"
				default:
					n.Comparison = "upgrade"
				}
			}
			if n.Problem == nil && m.Stage != n.Layer {
				n.Problem = &Problem{"wrong-layer", n.Layer}
			}
			if n.Problem == nil && m.Device != "" && m.Device != p.ID {
				n.Problem = &Problem{"wrong-device", m.Device}
			}
			if n.Problem == nil {
				n.Problem = CheckRequirements(f, pkg)
			}
			if n.Comparison == "downgrade" {
				c.Downgrade = true
				if n.Problem == nil && !downgrade {
					n.Problem = &Problem{"downgrade", m.Version}
				}
			}
			n.Ready = n.Problem == nil
			if n.Ready || n.Problem != nil && n.Problem.Code == "downgrade" {
				f.Layers[n.Layer] = m.Version
				for _, deb := range m.Packages {
					f.Packages[deb.Name] = deb.Version
				}
			}
		}
		n.Status = "missing"
		if n.Problem != nil {
			n.Status = "unusable"
		} else if n.Ready {
			n.Status = "checked"
		}
		c.Ready = c.Ready && n.Ready
	}
	return c
}

func BuildChoices(p profile.Profile, f Facts, provider PackageProvider, downgrade bool) []Choice {
	choices := []Choice{}
	for _, def := range ChoiceDefinitions(p) {
		choices = append(choices, PrepareChoice(p, f, DescribeChoice(p, f, def), provider, downgrade))
	}
	return choices
}

func LayerRows(p profile.Profile, f Facts, choices []Choice) []LayerRow {
	rows := []LayerRow{}
	for _, layer := range p.Layers {
		row := LayerRow{Guide: layer.Guide, ReadOnly: layer.ReadOnly, Layer: layer.ID, Name: layer.Name, Installed: f.Layers[layer.ID], Action: "guide", Enabled: true}
		if layer.ReadOnly {
			row.KernelRelease, row.PrettyName = f.KernelRelease, f.PrettyName
		}
		for _, choice := range choices {
			if choice.ID != layer.ID {
				continue
			}
			row.Action, row.Choice = "install", choice.ID
			row.ActionName = layerRecipes[layer.ID].actionName
			if row.Installed != "" {
				row.Action = "update"
			}
			row.Enabled, row.Reason = choice.Problem == nil, choice.Problem
		}
		rows = append(rows, row)
	}
	return rows
}

func SuggestedChoice(choices []Choice, f Facts) string {
	for _, c := range choices {
		if c.ID == "full" && c.Problem == nil {
			for _, id := range c.Layers {
				if f.Layers[id] == "" {
					return "full"
				}
			}
		}
	}
	for i := len(choices) - 1; i >= 0; i-- {
		if choices[i].Problem == nil {
			return choices[i].ID
		}
	}
	return ""
}

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"maps"
	"slices"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

// Problem codes are translated by the shells; Detail identifies the unmet requirement.
type Problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

type Request struct {
	Choice    string `json:"choice"`
	Downgrade bool   `json:"downgrade"`
	YesErase  string `json:"yesErase,omitempty"`
	BasePlan  string `json:"basePlan,omitempty"`
}

type Step struct {
	Layer     string       `json:"layer"`
	Action    string       `json:"action"`
	PackageID string       `json:"packageId"`
	ID        string       `json:"id,omitempty"`
	Name      profile.Text `json:"name,omitempty"`
	Critical  bool         `json:"critical"`
	Resumable bool         `json:"resumable"`
}

type Plan struct {
	Choice         string   `json:"choice"`
	Steps          []Step   `json:"steps"`
	Problem        *Problem `json:"problem,omitempty"`
	Downgrade      bool     `json:"downgrade"`
	allowDowngrade bool
}

// CheckRequirements also runs immediately before each layer on the actual device.
func CheckRequirements(f Facts, pkg *Package) *Problem {
	r := pkg.manifest.Requires
	if !f.Root {
		return &Problem{"not-root", ""}
	}
	for _, v := range []struct{ want, got, name string }{
		{r.KernelRelease, f.KernelRelease, "kernelRelease"}, {r.DeviceTreeModel, f.DeviceTreeModel, "deviceTreeModel"},
		{r.DebianVersion, f.DebianVersion, "debianVersion"}, {r.DPKGArchitecture, f.DPKGArchitecture, "dpkgArchitecture"},
	} {
		if v.want != "" && v.want != v.got {
			return &Problem{"requirement", v.name + "=" + v.want}
		}
	}
	for _, stage := range r.Stages {
		if f.Layers[stage] == "" {
			return &Problem{"below-missing", stage}
		}
	}
	for _, name := range r.Packages {
		if f.Packages[name] == "" {
			return &Problem{"required-package", name}
		}
	}
	need := max(r.FreeKiB, (int64(len(pkg.data))+1023)/1024*2)
	if f.FreeKiB[pkg.manifest.DeviceDir] < need {
		return &Problem{"free-space", pkg.manifest.DeviceDir}
	}
	return nil
}

// BuildPlan is pure: it simulates earlier layers' packages and versions without using adb.
func BuildPlan(p profile.Profile, installed Facts, packages []*Package, req Request) Plan {
	plan := Plan{Choice: req.Choice, Steps: []Step{}, allowDowngrade: req.Downgrade}
	fail := func(code, detail string) Plan {
		plan.Steps = []Step{}
		plan.Problem = &Problem{code, detail}
		return plan
	}
	if req.Choice != "full" && req.Choice != "device" && req.Choice != "relay" {
		return fail("bad-choice", req.Choice)
	}
	if installed.Layers["base"] == "" {
		return fail("base-missing", "")
	}
	f := installed
	f.Layers, f.Packages = maps.Clone(f.Layers), maps.Clone(f.Packages)
	if f.Packages == nil {
		f.Packages = map[string]string{}
	}
	loaded := map[string]*Package{}
	for _, pkg := range packages {
		m := pkg.manifest
		if m.Device != "" && m.Device != p.ID {
			return fail("wrong-device", m.Device)
		}
		if loaded[m.Stage] != nil {
			return fail("duplicate-layer", m.Stage)
		}
		loaded[m.Stage] = pkg
	}
	var deferred *Package
	for _, layer := range p.Layers {
		pkg := loaded[layer.ID]
		if layer.Transport != profile.ModeADB || layer.Destructive || req.Choice != "full" && req.Choice != layer.ID || pkg == nil {
			continue
		}
		for _, below := range p.Below(layer.ID) {
			if f.Layers[below.ID] == "" {
				return fail("below-missing", below.ID)
			}
		}
		if problem := CheckRequirements(f, pkg); problem != nil {
			plan.Problem = problem
			plan.Steps = []Step{}
			return plan
		}
		m := pkg.manifest
		if layer.ID == "relay" && f.Layers[layer.ID] != "" && CompareVersions(m.Version, f.Layers[layer.ID]) < 0 {
			plan.Downgrade = true
			if !req.Downgrade {
				return fail("downgrade", m.Version)
			}
		}
		add := func(actions ...string) {
			for _, a := range actions {
				plan.Steps = append(plan.Steps, Step{Layer: layer.ID, Action: a, PackageID: pkg.digest, Resumable: true})
			}
		}
		add("requirements", "clock", "prepare", "push", "checksum")
		if layer.ID == "device" {
			add("unpack", "preflight")
		}
		add("install")
		if m.Reboot == "required" {
			add("reboot", "wait")
		}
		if layer.ID == "device" && req.Choice == "full" && loaded["relay"] != nil {
			deferred = pkg
		} else {
			add("verify")
		}
		f.Layers[layer.ID] = m.Version
		for _, deb := range m.Packages {
			f.Packages[deb.Name] = deb.Version
		}
	}
	if len(plan.Steps) == 0 {
		return fail("no-package", req.Choice)
	}
	if deferred != nil {
		plan.Steps = append(plan.Steps, Step{Layer: "device", Action: "verify", PackageID: deferred.digest, Resumable: true})
	}
	// A final inspection records actual installed versions, including after verification retries.
	last := plan.Steps[len(plan.Steps)-1]
	plan.Steps = append(plan.Steps, Step{Layer: last.Layer, Action: "installed", PackageID: last.PackageID, Resumable: true})
	return plan
}

func PackageInfos(packages []*Package) []PackageInfo {
	infos := make([]PackageInfo, 0, len(packages))
	for _, p := range packages {
		infos = append(infos, p.Info())
	}
	slices.SortFunc(infos, func(a, b PackageInfo) int {
		return slices.Index([]string{"device", "relay"}, a.Layer) - slices.Index([]string{"device", "relay"}, b.Layer)
	})
	return infos
}

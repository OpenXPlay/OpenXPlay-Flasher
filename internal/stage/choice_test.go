// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"reflect"
	"testing"
)

func TestLayerFirstChoices(t *testing.T) {
	for _, tc := range []struct {
		name          string
		device, relay bool
		baseMissing   bool
		problems      []string
		suggested     string
	}{
		{"fresh base only", false, false, false, []string{"", "", "below-missing"}, "full"},
		{"device only", true, false, false, []string{"", "", ""}, "full"},
		{"device and relay", true, true, false, []string{"", "", ""}, "relay"},
		{"base missing", true, true, true, []string{"base-missing", "base-missing", "base-missing"}, ""},
		{"relay without device", false, true, false, []string{"", "", "below-missing"}, "full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := installedFacts()
			if !tc.device {
				delete(f.Layers, "device")
			}
			if !tc.relay {
				delete(f.Layers, "relay")
			}
			if tc.baseMissing {
				delete(f.Layers, "base")
			}
			p := builtinProfile(t)
			choices := BuildChoices(p, f, PackageSet(nil), false)
			for i, c := range choices {
				code := ""
				if c.Problem != nil {
					code = c.Problem.Code
				}
				if code != tc.problems[i] || c.Ready || len(c.Steps) == 0 {
					t.Fatalf("choice=%+v", c)
				}
				for _, need := range c.Needs {
					if need.Status != "missing" || need.Ready {
						t.Fatalf("need=%+v", need)
					}
				}
			}
			full := choices[0]
			if !reflect.DeepEqual(full.Layers, []string{"device", "relay"}) || len(full.Needs) != 2 || full.Destructive {
				t.Fatalf("full=%+v", full)
			}
			if suggested := SuggestedChoice(choices, f); suggested != tc.suggested {
				t.Fatalf("suggested=%s", suggested)
			}
			rows := LayerRows(p, f, choices)
			if len(rows) != 3 || rows[0].Action != "guide" || rows[0].Choice != "" {
				t.Fatalf("rows=%+v", rows)
			}
			for i, present := range []bool{tc.device, tc.relay} {
				want := "install"
				if present {
					want = "update"
				}
				if rows[i+1].Action != want || rows[i+1].Enabled != (tc.problems[i+1] == "") {
					t.Fatalf("row=%+v", rows[i+1])
				}
			}
			if !tc.device && !tc.baseMissing && rows[2].Reason.Detail != "device" {
				t.Fatalf("reason=%+v", rows[2].Reason)
			}
		})
	}
}

func TestChoicePackageReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, choice        string
		change              func(*Facts, *Package, *Package)
		loaded              string
		allow               bool
		ready               bool
		problem, comparison string
	}{
		{name: "full needs both", choice: "full", loaded: "relay"},
		{name: "checked full", choice: "full", loaded: "both", ready: true, comparison: "same"},
		{name: "fresh full simulates lower packages", choice: "full", loaded: "both", ready: true, comparison: "new", change: func(f *Facts, d, r *Package) {
			delete(f.Layers, "device")
			delete(f.Layers, "relay")
			delete(f.Packages, "bluez")
			delete(f.Packages, "iw")
		}},
		{name: "unused wrong-device package", choice: "relay", loaded: "both", ready: true, comparison: "upgrade", change: func(f *Facts, d, r *Package) { d.manifest.Device = "other" }},
		{name: "new install", choice: "relay", loaded: "relay", ready: true, comparison: "new", change: func(f *Facts, d, r *Package) { delete(f.Layers, "relay") }},
		{name: "same version", choice: "relay", loaded: "relay", ready: true, comparison: "same", change: func(f *Facts, d, r *Package) { f.Layers["relay"] = r.manifest.Version }},
		{name: "downgrade refused", choice: "relay", loaded: "relay", problem: "downgrade", comparison: "downgrade", change: func(f *Facts, d, r *Package) { f.Layers["relay"] = "9.0" }},
		{name: "downgrade allowed", choice: "relay", loaded: "relay", allow: true, ready: true, comparison: "downgrade", change: func(f *Facts, d, r *Package) { f.Layers["relay"] = "9.0" }},
		{name: "device downgrade refused", choice: "device", loaded: "both", problem: "downgrade", comparison: "downgrade", change: func(f *Facts, d, r *Package) { f.Layers["device"] = "9.0" }},
		{name: "wrong device", choice: "device", loaded: "both", problem: "wrong-device", comparison: "same", change: func(f *Facts, d, r *Package) { d.manifest.Device = "other" }},
		{name: "requirements unmet", choice: "relay", loaded: "relay", problem: "requirement", comparison: "upgrade", change: func(f *Facts, d, r *Package) { f.DebianVersion = "12" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r := checkedPackages(t)
			f, p := installedFacts(), builtinProfile(t)
			if tc.change != nil {
				tc.change(&f, d, r)
			}
			packages := PackageSet{r}
			if tc.loaded == "both" {
				packages = append(packages, d)
			}
			for _, def := range ChoiceDefinitions(p) {
				if def.ID != tc.choice {
					continue
				}
				before := DescribeChoice(p, f, def)
				c := PrepareChoice(p, f, before, packages, tc.allow)
				if c.Problem != nil || c.Ready != tc.ready || !reflect.DeepEqual(c.Steps, before.Steps) {
					t.Fatalf("choice=%+v", c)
				}
				n := c.Needs[0]
				if tc.comparison != "" && n.Comparison != tc.comparison {
					t.Fatalf("need=%+v", n)
				}
				if tc.problem != "" && (n.Problem == nil || n.Problem.Code != tc.problem || n.Status != "unusable") {
					t.Fatalf("need=%+v", n)
				}
				if tc.ready && n.Status != "checked" {
					t.Fatalf("need=%+v", n)
				}
			}
		})
	}
}

func TestChoicePreviewMatchesFinalPlan(t *testing.T) {
	d, r := checkedPackages(t)
	p, f := builtinProfile(t), installedFacts()
	for _, def := range ChoiceDefinitions(p) {
		c := DescribeChoice(p, f, def)
		plan := BuildPlan(p, f, []*Package{d, r}, Request{Choice: def.ID})
		if len(c.Steps) != len(plan.Steps) {
			t.Fatalf("%s preview=%v plan=%v", def.ID, c.Steps, plan.Steps)
		}
		for i, s := range c.Steps {
			if s.Action != plan.Steps[i].Action || s.Layer != plan.Steps[i].Layer {
				t.Fatalf("%s step=%+v vs %+v", def.ID, s, plan.Steps[i])
			}
		}
	}
}

func TestPrepareChoiceCanReevaluateWithoutMutatingItsInput(t *testing.T) {
	_, r := checkedPackages(t)
	p, f := builtinProfile(t), installedFacts()
	c := DescribeChoice(p, f, ChoiceDefinitions(p)[2])
	checked := PrepareChoice(p, f, c, PackageSet{r}, false)
	if !checked.Ready || c.Needs[0].Package != nil || c.Needs[0].Status != "missing" {
		t.Fatal("preparing mutated its descriptor")
	}
	missing := PrepareChoice(p, f, checked, PackageSet(nil), false)
	if missing.Ready || missing.Needs[0].Package != nil || missing.Needs[0].Status != "missing" || missing.Needs[0].Comparison != "" {
		t.Fatalf("removed package stayed usable: %+v", missing.Needs[0])
	}
	f.Layers["relay"] = "9.0"
	older := PrepareChoice(p, f, checked, PackageSet{r}, false)
	if !older.Downgrade || older.Ready {
		t.Fatal("older version did not need opt-in")
	}
	delete(f.Layers, "relay")
	newInstall := PrepareChoice(p, f, older, PackageSet{r}, false)
	if newInstall.Downgrade || !newInstall.Ready || newInstall.Needs[0].Comparison != "new" {
		t.Fatalf("stale downgrade decision: %+v", newInstall)
	}
}

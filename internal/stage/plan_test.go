// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"reflect"
	"testing"
)

func installedFacts() Facts {
	return Facts{Root: true, KernelRelease: "5.15.0-handsomekernel+", DeviceTreeModel: "Handsome Openstick", DebianVersion: "11", DPKGArchitecture: "arm64",
		Layers:   map[string]string{"base": "5.15.0-handsomekernel+", "device": "0.1.0", "relay": "0.1.3"},
		Packages: map[string]string{"bluez": "5.55", "network-manager": "1.30", "iw": "5.9"},
		FreeKiB:  map[string]int64{"/var/cache/openxplay/stages/device": 800000, "/var/cache/openxplay/relay": 800000}}
}

func TestPlans(t *testing.T) {
	for _, tc := range []struct {
		name, choice, code string
		packages           string
		change             func(*Facts, *Package, *Package)
		downgrade          bool
		installs           []string
	}{
		{name: "full", choice: "full", packages: "both", installs: []string{"device", "relay"}},
		{name: "relay alone", choice: "relay", packages: "relay", installs: []string{"relay"}},
		{name: "full loaded relay", choice: "full", packages: "relay", installs: []string{"relay"}},
		{name: "device alone", choice: "device", packages: "device", installs: []string{"device"}},
		{name: "same version", choice: "relay", packages: "relay", change: func(f *Facts, d, r *Package) { f.Layers["relay"] = r.manifest.Version }, installs: []string{"relay"}},
		{name: "base missing", choice: "full", packages: "both", change: func(f *Facts, d, r *Package) { delete(f.Layers, "base") }, code: "base-missing"},
		{name: "device missing", choice: "relay", packages: "relay", change: func(f *Facts, d, r *Package) { delete(f.Layers, "device") }, code: "below-missing"},
		{name: "full supplies device", choice: "full", packages: "both", change: func(f *Facts, d, r *Package) {
			delete(f.Layers, "device")
			delete(f.Packages, "bluez")
			delete(f.Packages, "iw")
		}, installs: []string{"device", "relay"}},
		{name: "different device", choice: "full", packages: "both", change: func(f *Facts, d, r *Package) { d.manifest.Device = "other" }, code: "wrong-device"},
		{name: "Debian version unmet", choice: "relay", packages: "relay", change: func(f *Facts, d, r *Package) { f.DebianVersion = "12" }, code: "requirement"},
		{name: "kernel unmet", choice: "device", packages: "device", change: func(f *Facts, d, r *Package) { f.KernelRelease = "other" }, code: "requirement"},
		{name: "arch unmet", choice: "relay", packages: "relay", change: func(f *Facts, d, r *Package) { f.DPKGArchitecture = "amd64" }, code: "requirement"},
		{name: "requires package", choice: "relay", packages: "relay", change: func(f *Facts, d, r *Package) { delete(f.Packages, "iw") }, code: "required-package"},
		{name: "space", choice: "device", packages: "device", change: func(f *Facts, d, r *Package) { f.FreeKiB[d.manifest.DeviceDir] = 1 }, code: "free-space"},
		{name: "root", choice: "full", packages: "both", change: func(f *Facts, d, r *Package) { f.Root = false }, code: "not-root"},
		{name: "no package", choice: "full", code: "no-package"},
		{name: "bad choice", choice: "base", packages: "both", code: "bad-choice"},
		{name: "downgrade refused", choice: "relay", packages: "relay", change: func(f *Facts, d, r *Package) { f.Layers["relay"] = "0.2.0" }, code: "downgrade"},
		{name: "downgrade allowed", choice: "relay", packages: "relay", change: func(f *Facts, d, r *Package) { f.Layers["relay"] = "0.2.0" }, downgrade: true, installs: []string{"relay"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r := checkedPackages(t)
			f := installedFacts()
			if tc.change != nil {
				tc.change(&f, d, r)
			}
			var packages []*Package
			switch tc.packages {
			case "both":
				packages = []*Package{r, d}
			case "device":
				packages = []*Package{d}
			case "relay":
				packages = []*Package{r}
			}
			plan := BuildPlan(builtinProfile(t), f, packages, Request{Choice: tc.choice, Downgrade: tc.downgrade})
			if tc.code != "" {
				if plan.Problem == nil || plan.Problem.Code != tc.code || len(plan.Steps) != 0 {
					t.Fatalf("plan=%+v", plan)
				}
				return
			}
			if plan.Problem != nil {
				t.Fatal(plan.Problem)
			}
			var installs []string
			for _, s := range plan.Steps {
				if s.Action == "install" {
					installs = append(installs, s.Layer)
				}
			}
			if !reflect.DeepEqual(installs, tc.installs) {
				t.Fatalf("installs=%v", installs)
			}
			if len(installs) == 2 {
				n := len(plan.Steps)
				if plan.Steps[n-2].Layer != "device" || plan.Steps[n-2].Action != "verify" {
					t.Fatalf("device verification not deferred: %v", plan.Steps)
				}
			}
		})
	}
}

func TestPlanningDoesNotChangeFacts(t *testing.T) {
	d, r := checkedPackages(t)
	f := installedFacts()
	before := f.Layers["relay"]
	BuildPlan(builtinProfile(t), f, []*Package{d, r}, Request{Choice: "full"})
	if f.Layers["relay"] != before || f.Packages["openxplay-ufi003"] != "" {
		t.Fatal("planner changed its input")
	}
}

func TestDebianVersionOrder(t *testing.T) {
	for _, pair := range [][2]string{{"0.1.5~beta.1", "0.1.5"}, {"0.1.5~beta.1", "0.1.5~beta.2"}, {"0.1.9", "0.1.10"}, {"1.0-1", "1.0-2"}, {"99.0", "1:1.0"}, {"1.0~rc1", "1.0~~rc1"}, {"1.0+a", "1.0.a"}} {
		want := -1
		if pair[0] == "1.0~rc1" {
			want = 1
		}
		if got := CompareVersions(pair[0], pair[1]); got != want {
			t.Errorf("%s vs %s=%d", pair[0], pair[1], got)
		}
		if got := CompareVersions(pair[1], pair[0]); got != -want {
			t.Errorf("reverse %v=%d", pair, got)
		}
	}
	for _, pair := range [][2]string{{"1.0", "1.0-0"}, {"1.01", "1.1"}, {"1:1", "01:1"}} {
		if CompareVersions(pair[0], pair[1]) != 0 {
			t.Errorf("not equal: %v", pair)
		}
	}
}

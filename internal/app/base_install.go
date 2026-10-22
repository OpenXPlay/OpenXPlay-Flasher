// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/power"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func isBaseChoice(id string) bool {
	return id == "stock" || id == "base" || id == "full-reinstall" || id == "repair"
}
func choiceNeeds(id string) []string {
	switch id {
	case "stock", "full-reinstall":
		return []string{"base", "device", "relay"}
	case "base", "repair":
		return []string{"base"}
	case "full":
		return []string{"device", "relay"}
	case "device", "relay":
		return []string{id}
	}
	return nil
}
func baseKind(id string) string {
	if id == "stock" || id == "repair" {
		return id
	}
	return "system"
}
func baseName(id string) profile.Text {
	switch id {
	case "stock":
		return profile.Text{"en": "Install OpenXPlay from the original firmware", "zh-CN": "从原厂固件安装 OpenXPlay"}
	case "base":
		return profile.Text{"en": "Reinstall the base system", "zh-CN": "重新安装基础系统"}
	case "full-reinstall":
		return profile.Text{"en": "Full reinstall", "zh-CN": "完整重装"}
	case "repair":
		return profile.Text{"en": "Repair", "zh-CN": "修复"}
	}
	return nil
}

func (s *Service) basePlanLocked(p profile.Profile, kind string, identity base.Identity) base.Plan {
	if kind == "repair" {
		plan := base.BuildRepairPlan(p, identity, s.unfinished)
		if base.IncompleteChain(s.unfinished) && (s.installTarget == nil || !s.unfinished.SameChainConnection(s.installTarget.Device.USB)) {
			plan.Problem = &stage.Problem{Code: "chain-incomplete"}
			plan.Steps = []base.Step{}
		}
		return plan
	}
	plan := base.BuildPlan(p, kind, identity)
	if base.IncompleteChain(s.unfinished) {
		plan.Problem = &stage.Problem{Code: "chain-incomplete"}
		plan.Steps = []base.Step{}
	}
	return plan
}

func chainNotice(plan base.Plan) profile.Text {
	if plan.Problem != nil || len(plan.Steps) == 0 {
		return profile.Text{
			"en":    "The boot chain was not finished. The dongle is no longer available in the same lk1st fastboot session. Stop here; do not restart the dongle.",
			"zh-CN": "引导链尚未写完，设备已不在原来的 lk1st fastboot 会话中。请停止操作，不要重启设备。",
		}
	}
	step := plan.Steps[0].ID
	return profile.Text{
		"en":    fmt.Sprintf("The boot chain was not finished. Keep the dongle connected; Repair continues from %s without restarting it.", step),
		"zh-CN": fmt.Sprintf("引导链尚未写完。请保持设备连接；修复将从 %s 继续，不会先重启设备。", step),
	}
}

func (s *Service) extendBaseChoicesLocked(state *InstallState, p profile.Profile) {
	// Add artifact provenance to the existing adb needs without changing their planning rules.
	for i := range state.Choices {
		for k := range state.Choices[i].Needs {
			n := &state.Choices[i].Needs[k]
			n.Artifacts = s.artifactNeedsLocked(p, n.Layer)
			n.Offline = s.catalogState.Index == nil
			if pkg, _ := stage.PackageSet(s.packages).ForLayer(n.Layer); pkg != nil {
				if problem := s.catalogPackageProblemLocked(pkg); problem != nil {
					n.Problem = problem
					n.Ready = false
					state.Choices[i].Ready = false
				}
			}
		}
	}
	if p.Base == nil {
		return
	}
	ids := []string{"base", "full-reinstall"}
	if s.baseIdentity != nil && (s.baseIdentity.Mode == profile.ModeStock || s.baseIdentity.Product == p.Base.Fastboot.StockProduct) {
		ids = []string{"stock"}
	}
	if s.baseIdentity != nil && s.baseIdentity.Mode == profile.ModeFastboot || base.ResumeChoice(s.unfinished) != "" {
		ids = append(ids, "repair")
	}
	incomplete := base.IncompleteChain(s.unfinished)
	if incomplete {
		state.Choices = []stage.Choice{}
		ids = []string{"repair"}
		state.Suggested = "repair"
	}
	for _, id := range ids {
		c := stage.Choice{BasePlan: baseKind(id), ID: id, Name: baseName(id), Destructive: true, BackupRequired: true, Layers: choiceNeeds(id), Needs: []stage.PackageNeed{}, Steps: []stage.Step{}}
		if incomplete {
			original := s.unfinished.Choice
			if original == "" {
				original = s.unfinished.Plan
			}
			if original == "stock" || original == "full-reinstall" {
				c.Layers = choiceNeeds(original)
			}
		}
		identity := base.Identity{Mode: profile.ModeADB, Serial: "unknown"}
		if s.baseIdentity != nil {
			identity = *s.baseIdentity
			c.Serial = identity.Serial
		}
		plan := s.basePlanLocked(p, baseKind(id), identity)
		c.Steps = base.Steps(plan)
		if len(c.Steps) == 0 && !incomplete {
			// An unavailable choice still explains its scope and steps in the existing page.
			preview := identity
			if id == "stock" {
				preview.Mode = profile.ModeStock
			} else {
				preview.Mode = profile.ModeADB
			}
			preview.Product = ""
			c.Steps = base.Steps(base.BuildPlan(p, baseKind(id), preview))
		}
		if len(c.Layers) > 1 && (!incomplete || plan.Problem == nil) {
			f := stage.Facts{Layers: map[string]string{"base": p.Base.Release}}
			c.Steps = append(c.Steps, stage.DescribeChoice(p, f, stage.ChoiceDefinition{ID: "full", Layers: []string{"device", "relay"}, Needs: []string{"device", "relay"}}).Steps...)
		}
		switch {
		case incomplete && plan.Problem != nil:
			c.Problem = plan.Problem
			state.Problem = plan.Problem
		case !s.fastbootSupported:
			c.Problem = &stage.Problem{Code: "fastboot-unsupported"}
			c.Reason = profile.Text{"en": "Flashing the base system is not available in the Windows version yet; use a Mac, or the manual guide.", "zh-CN": "Windows 版暂不支持刷写基础系统，请使用 Mac，或按照手动安装指南操作。"}
		case s.baseIdentity == nil:
			c.Problem = &stage.Problem{Code: "base-unconfirmed"}
			c.Reason = profile.Text{"en": "Confirm this board's serial before installing the base system.", "zh-CN": "安装基础系统前，请先确认此设备的序列号。"}
		case plan.Problem != nil:
			c.Problem = plan.Problem
			c.Reason = profile.Text{"en": "This plan does not apply to the connected device's current mode.", "zh-CN": "此方案不适用于已连接设备的当前模式。"}
		case state.Problem != nil:
			c.Problem = state.Problem
		}
		if incomplete {
			state.ChainNotice = chainNotice(plan)
			c.Reason = state.ChainNotice
		}
		c.Ready = c.Problem == nil
		for _, layer := range c.Layers {
			n := stage.PackageNeed{Layer: layer, FileTypes: stage.PackageFileTypes(layer), Status: "missing", Artifacts: s.artifactNeedsLocked(p, layer), Offline: s.catalogState.Index == nil}
			if layer == "base" {
				n.FileTypes = []string{".zip", ".img"}
				ready := len(n.Artifacts) == 3
				var size int64
				for _, a := range n.Artifacts {
					size += a.Size
					ready = ready && a.Verified && a.Error == nil
				}
				info := stage.PackageInfo{ID: p.Base.Release, Name: p.Base.DisplayName(), Layer: "base", Device: p.ID, Version: p.Base.DisplayName(), Size: size, Checked: ready}
				n.Package = &info
				n.Ready = ready
			} else {
				pkg, problem := stage.PackageSet(s.packages).ForLayer(layer)
				n.Problem = problem
				if pkg != nil {
					info := pkg.Info()
					n.Package = &info
					if m := pkg.Manifest(); m.Device != "" && m.Device != p.ID {
						n.Problem = &stage.Problem{Code: "wrong-device", Detail: m.Device}
					}
					if n.Problem == nil {
						n.Problem = s.catalogPackageProblemLocked(pkg)
					}
					n.Ready = n.Problem == nil
				}
			}
			if n.Ready {
				n.Status = "checked"
			}
			if n.Problem != nil {
				n.Status = "unusable"
			}
			c.Ready = c.Ready && n.Ready
			c.Needs = append(c.Needs, n)
		}
		state.Choices = append(state.Choices, c)
	}
	if s.baseIdentity != nil {
		if s.baseIdentity.Mode == profile.ModeFastboot && s.baseIdentity.Product != p.Base.Fastboot.StockProduct || base.ResumeChoice(s.unfinished) != "" {
			state.Suggested = "repair"
		} else if s.baseIdentity.Mode == profile.ModeStock || s.baseIdentity.Product == p.Base.Fastboot.StockProduct {
			state.Suggested = "stock"
		}
	}
	for i := range state.Layers {
		if state.Layers[i].Layer == "base" {
			state.Layers[i].Name = profile.Text{"en": p.Base.DisplayName(), "zh-CN": p.Base.DisplayName()}
			state.Layers[i].KernelRelease = p.Base.Linux.KernelRelease
			id := "base"
			if state.Suggested == "stock" || state.Suggested == "repair" {
				id = state.Suggested
			}
			for _, c := range state.Choices {
				if c.ID == id {
					state.Layers[i].Action = "install"
					state.Layers[i].Choice = id
					state.Layers[i].ActionName = c.Name
					state.Layers[i].Enabled = c.Problem == nil
					state.Layers[i].Reason = c.Problem
				}
			}
		}
	}
}
func (s *Service) artifactNeedsLocked(p profile.Profile, layer string) []stage.ArtifactNeed {
	artifacts := []catalog.Artifact{}
	version := ""
	for _, picked := range s.picks {
		if picked.Layer == layer && picked.Profile == p.ID && picked.Version != "" {
			version = picked.Version
			break
		}
	}
	entry := s.catalogLayerLocked(p, layer, version)
	if entry == nil && version != "" {
		entry = s.catalogLayerLocked(p, layer, "")
	}
	if layer == "base" && p.Base != nil {
		for _, role := range []string{"base-generic", "rootfs-zip", "boot"} {
			pin := p.Base.Archives[role]
			a := catalog.Artifact{Role: role, Name: pin.File, Size: pin.Size, SHA256: pin.SHA256}
			if entry != nil {
				for _, ca := range entry.Artifacts {
					if ca.Role == role && ca.Size == pin.Size && ca.SHA256 == pin.SHA256 {
						a.URLs = slices.Clone(ca.URLs)
					}
				}
			}
			artifacts = append(artifacts, a)
		}
	} else if entry != nil {
		artifacts = entry.Artifacts
	} else if s.catalogState.Index == nil {
		if layer == "device" {
			artifacts = []catalog.Artifact{{Role: "bundle", Name: "*.tar.gz"}}
		}
		if layer == "relay" {
			artifacts = []catalog.Artifact{{Role: "deb", Name: "*.deb"}, {Role: "manifest", Name: "*.manifest.json"}}
		}
	}
	out := []stage.ArtifactNeed{}
	for _, a := range artifacts {
		n := stage.ArtifactNeed{Role: a.Role, Name: a.Name, Size: a.Size, SHA256: a.SHA256, Source: "catalog", Total: a.Size, Status: "waiting", URLs: slices.Clone(a.URLs)}
		if picked, ok := s.picks[artifactKey(layer, a.Role)]; ok && picked.Profile == p.ID && (a.SHA256 == "" || a.Size == picked.Size && a.SHA256 == picked.SHA256) {
			n.Name, n.Path, n.Size, n.SHA256, n.Total = picked.Name, picked.Path, picked.Size, picked.SHA256, picked.Size
			n.Source = "selected"
			if s.checkedFiles[picked.SHA256] {
				n.Status, n.Done, n.Verified = "verified", picked.Size, true
			}
		}
		page := ""
		switch layer {
		case "base":
			if p.Base != nil {
				page = p.Base.Source
			}
		case "device":
			page = "https://github.com/OpenXPlay/OpenXPlay-Hardware/releases"
		case "relay":
			page = "https://github.com/OpenXPlay/OpenXPlay-Relay/releases"
		}
		if page != "" && !slices.Contains(n.URLs, page) {
			n.URLs = append(n.URLs, page)
		}
		out = append(out, n)
	}
	return out
}

func (s *Service) baseFilesLocked(p profile.Profile) *base.Files {
	paths := map[string]string{}
	for _, f := range s.picks {
		if f.Layer == "base" && f.Profile == p.ID {
			paths[f.SHA256] = f.Path
		}
	}
	return &base.Files{Dir: s.cacheDir, Profile: p, Provider: base.Provider{Paths: paths}}
}
func (s *Service) baseStore(p profile.Profile) *base.Store {
	return &base.Store{Dir: s.backupDir, Profile: p, Version: s.Info().Version}
}

// startBase requires prepared inputs and a typed serial. The executor rechecks every input and
// identity under the shared device lock; neither the request nor a preview authorizes a write.
func (s *Service) startBase(ctx context.Context, req stage.Request) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	unlock := true
	defer func() {
		if unlock {
			s.configMu.Unlock()
		}
	}()
	p, err := s.inspectInstall(ctx, req.Choice)
	if err != nil {
		return s.emitInstall(), err
	}
	s.mu.Lock()
	state := s.installSnapshotLocked()
	identity := s.baseIdentity
	var selected *stage.Choice
	for _, c := range state.Choices {
		if c.ID == req.Choice {
			copy := c
			selected = &copy
			break
		}
	}
	if selected == nil {
		s.mu.Unlock()
		return state, &stage.Problem{Code: "bad-choice", Detail: req.Choice}
	}
	if req.BasePlan != "" {
		changed, problem := s.baseVariantLocked(p, *selected, req.BasePlan)
		if problem != nil {
			s.mu.Unlock()
			return state, problem
		}
		selected = &changed
	}
	if selected.Problem != nil {
		s.mu.Unlock()
		return state, selected.Problem
	}
	if identity == nil || req.YesErase == "" || req.YesErase != identity.Serial {
		s.mu.Unlock()
		return state, &stage.Problem{Code: "typed-serial"}
	}
	if !selected.Ready {
		s.mu.Unlock()
		return state, &stage.Problem{Code: "no-package", Detail: req.Choice}
	}
	if s.dataDir == "" || s.cacheDir == "" || s.backupDir == "" {
		s.mu.Unlock()
		return state, &stage.Problem{Code: "storage-unavailable"}
	}
	start := *identity
	plan := s.basePlanLocked(p, selected.BasePlan, start)
	if plan.Problem != nil {
		s.mu.Unlock()
		return state, plan.Problem
	}
	files := s.baseFilesLocked(p)
	packages := slices.Clone(s.packages)
	open, connector := s.fastbootOpen, s.adb
	inhibitor := s.sleepInhibitor
	if inhibitor == nil {
		inhibitor = power.Noop{}
	}
	release, err := inhibitor.Acquire()
	if err != nil {
		s.mu.Unlock()
		return state, &stage.Problem{Code: "keep-awake", Detail: err.Error()}
	}
	life := s.lifeCtx
	if life == nil {
		life = context.Background()
	}
	jobCtx, cancel := context.WithCancel(life)
	done := make(chan struct{})
	s.installCancel, s.installDone = cancel, done
	initial := stage.Job{Status: "running", Started: time.Now(), Failed: -1, Steps: []stage.StepProgress{}}
	for _, step := range selected.Steps {
		initial.Steps = append(initial.Steps, stage.StepProgress{Step: step, Status: "pending"})
	}
	s.installJob = &initial
	s.installCatalogLog = "Install / 安装: " + s.signatureLogLocked()
	s.installStale = true
	s.mu.Unlock()
	executor := base.NewExecutor(p, files, s.baseStore(p), base.Journals{Dir: s.dataDir})
	executor.Detector, executor.ADB, executor.Open = jobDetector{s}, connector, open
	executor.Continue = len(selected.Layers) > 1
	executor.Choice = req.Choice
	executor.Emit = func(job stage.Job) {
		// Completion of the base is an intermediate result of a full install.
		if executor.Continue && job.Status == "succeeded" {
			job.Status = "running"
			job.Finished = time.Time{}
		}
		if executor.Continue {
			for _, step := range selected.Steps[len(plan.Steps):] {
				job.Steps = append(job.Steps, stage.StepProgress{Step: step, Status: "pending"})
			}
		}
		s.updateInstallJob(job)
	}
	state = s.emitInstall()
	unlock = false
	go func() {
		defer close(done)
		defer cancel()
		defer s.configMu.Unlock()
		defer release()
		// Catalog replacement cannot race with cache operations performed by Files.
		s.catalogMu.Lock()
		job := executor.Run(jobCtx, plan)
		s.catalogMu.Unlock()
		if job.Status == "succeeded" && executor.Continue {
			job = s.runLayersAfterBase(jobCtx, p, packages, req, job, executor.Journal)
		}
		if job.Status == "failed" && executor.Continue && len(job.Steps) == len(plan.Steps) {
			for _, step := range selected.Steps[len(plan.Steps):] {
				job.Steps = append(job.Steps, stage.StepProgress{Step: step, Status: "pending"})
			}
		}
		if job.Observed {
			s.recordLinuxTarget(jobCtx, p, start.Serial)
		}
		s.mu.Lock()
		if executor.Journal != nil {
			s.unfinished = executor.Journal
		}
		s.mu.Unlock()
		s.updateInstallJob(job)
		if job.Status == "succeeded" || base.IncompleteChain(executor.Journal) {
			readCtx, readCancel := context.WithTimeout(context.WithoutCancel(jobCtx), 20*time.Second)
			_, _ = s.inspectInstall(readCtx, "")
			readCancel()
			s.emitInstall()
		}
	}()
	return state, nil
}
func (s *Service) updateInstallJob(job stage.Job) {
	s.mu.Lock()
	s.installJob = &job
	if job.Installed != nil {
		s.installed = job.Installed
	}
	s.installStale = job.Status == "running" || !job.Observed
	s.mu.Unlock()
	s.emitInstall()
}

func (s *Service) boundLinux(ctx context.Context, p profile.Profile, serial string) (adb.Device, error) {
	target, got, err := s.installDevice(ctx)
	if err != nil {
		return nil, err
	}
	if got.ID != p.ID {
		return nil, &stage.Problem{Code: "wrong-device", Detail: got.ID}
	}
	s.mu.Lock()
	connector := s.adb
	s.mu.Unlock()
	d, err := connector.Connect(ctx, target.Device.USB)
	if err != nil {
		return nil, err
	}
	actual, err := base.LinuxSerial(ctx, d)
	if err == nil && actual != serial {
		err = &stage.Problem{Code: "serial-mismatch", Detail: actual}
	}
	if err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
func (s *Service) runLayersAfterBase(ctx context.Context, p profile.Profile, packages []*stage.Package, req stage.Request, baseJob stage.Job, journal *base.Journal) stage.Job {
	fail := func(err error) stage.Job {
		baseJob.Status = "failed"
		baseJob.Problem = installProblem(err)
		baseJob.Left = "partial"
		baseJob.Observed = false
		baseJob.Finished = time.Now()
		return baseJob
	}
	d, err := s.boundLinux(ctx, p, journal.Serial)
	if err != nil {
		return fail(err)
	}
	facts, err := stage.Inspect(ctx, d.Shell, p, packages)
	d.Close()
	if err != nil {
		return fail(err)
	}
	layerPlan := stage.BuildPlan(p, facts, packages, stage.Request{Choice: "full", Downgrade: req.Downgrade})
	if layerPlan.Problem != nil {
		return fail(layerPlan.Problem)
	}
	executor := stage.NewExecutor(p, packages)
	if s.newExecutor != nil {
		executor = s.newExecutor(p, packages)
	}
	executor.Connect = func(ctx context.Context) (adb.Device, error) { return s.boundLinux(ctx, p, journal.Serial) }
	executor.Confirm = func(ctx context.Context, d adb.Device) error {
		serial, err := base.LinuxSerial(ctx, d)
		if err != nil {
			return err
		}
		if serial != journal.Serial {
			return &stage.Problem{Code: "serial-mismatch", Detail: serial}
		}
		return nil
	}

	executor.Guard = func(ctx context.Context) error {
		c, err := base.OneCandidate(s.scanDevices(ctx, false), profile.ModeADB)
		if err != nil {
			return err
		}
		if !slices.Contains(c.Profiles, p.ID) {
			return &stage.Problem{Code: "wrong-device"}
		}
		return nil
	}

	executor.Checkpoint = func(ctx context.Context, step stage.Step, status string) error {
		id := step.Layer + "/" + step.Action
		if status == "running" {
			journal.InFlight = id
		} else {
			journal.InFlight = ""
			journal.Completed = append(journal.Completed, id)
			if journal.CompletedAt == nil {
				journal.CompletedAt = map[string]time.Time{}
			}
			journal.CompletedAt[id] = time.Now().UTC()
		}
		journal.Updated = time.Now().UTC()
		return (base.Journals{Dir: s.dataDir}).Save(ctx, *journal)
	}

	executor.Emit = func(job stage.Job) {
		merged := mergeBaseJob(baseJob, job)
		if merged.Finished.IsZero() {
			merged.Status = "running"
		}
		s.updateInstallJob(merged)
	}
	job := executor.Run(ctx, layerPlan)
	if job.Status == "succeeded" {
		journal.Finished = true
		journal.Updated = time.Now().UTC()
		if err = (base.Journals{Dir: s.dataDir}).Save(context.WithoutCancel(ctx), *journal); err != nil {
			return fail(err)
		}
	}
	return mergeBaseJob(baseJob, job)
}
func mergeBaseJob(baseJob, job stage.Job) stage.Job {
	job.Retryable = false
	offset := len(baseJob.Steps)
	job.Steps = append(slices.Clone(baseJob.Steps), job.Steps...)
	job.Profile, job.Serial, job.Plan = baseJob.Profile, baseJob.Serial, baseJob.Plan
	job.Started = baseJob.Started
	if job.Failed >= 0 {
		job.Failed += offset
	}
	job.Findings = baseJob.Findings
	job.Written = append(slices.Clone(baseJob.Written), job.Written...)
	if job.Status == "failed" {
		job.Left = "partial"
	}
	return job
}

type BackupPartition struct {
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ShortSHA256 string `json:"shortSHA256"`
	Source      string `json:"source"`
}

type BackupSet struct {
	Set        string                     `json:"set"`
	Created    time.Time                  `json:"created"`
	Partitions map[string]BackupPartition `json:"partitions"`
}

type BackupListing struct {
	Folder       string      `json:"folder"`
	Profile      string      `json:"profile"`
	Serial       string      `json:"serial"`
	Sets         []BackupSet `json:"sets"`
	RepairSet    string      `json:"repairSet,omitempty"`
	RepairSource string      `json:"repairSource"`
}

func (s *Service) backupListing(ctx context.Context, p profile.Profile, serial string, sets []base.Backup) (BackupListing, error) {
	folder, err := s.baseStore(p).Folder(serial)
	if err != nil {
		return BackupListing{}, err
	}
	listing := BackupListing{Folder: folder, Profile: p.ID, Serial: serial, Sets: []BackupSet{}, RepairSource: "fresh-capture"}
	previous, err := (base.Journals{Dir: s.dataDir}).RepairSource(ctx, p.ID, serial)
	if err != nil {
		return listing, err
	}
	if previous != nil {
		listing.RepairSource, listing.RepairSet = "previous-job", previous.RepairSet()
	}
	for _, b := range sets {
		set := BackupSet{Set: b.Set, Created: b.Created, Partitions: map[string]BackupPartition{}}
		for name, part := range b.Partitions {
			set.Partitions[name] = BackupPartition{Size: part.Size, SHA256: part.SHA256, ShortSHA256: part.SHA256[:min(12, len(part.SHA256))], Source: part.Source}
		}
		listing.Sets = append(listing.Sets, set)
	}
	return listing, nil
}

// Backup operations require author fingerprints, but only base-generic is needed to obtain
// them. The CLI and API never expose partition bytes through these metadata operations.
func (s *Service) BackupOperation(ctx context.Context, operation, serial, dir string) (any, error) {
	if operation != "list" && !base.ValidSerial(serial) {
		return nil, &stage.Problem{Code: "bad-serial"}
	}
	if !s.configMu.TryLock() {
		return nil, ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	var p profile.Profile
	for _, candidate := range s.profiles {
		if candidate.ID == "ufi003" {
			p = candidate
		}
	}
	store := s.baseStore(p)
	switch operation {
	case "list":
		backups, err := store.List(ctx)
		if err != nil {
			return nil, err
		}
		out := []BackupListing{}
		for len(backups) > 0 {
			serial := backups[0].Serial
			end := 1
			for end < len(backups) && backups[end].Serial == serial {
				end++
			}
			listing, err := s.backupListing(ctx, p, serial, backups[:end])
			if err != nil {
				return nil, err
			}
			out = append(out, listing)
			backups = backups[end:]
		}
		return out, nil
	case "show":
		sets, err := store.Sets(ctx, serial)
		if err != nil {
			return nil, err
		}
		return s.backupListing(ctx, p, serial, sets)
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	files := s.baseFilesLocked(p)
	s.mu.Unlock()
	if err := files.PrepareAuthor(ctx); err != nil {
		return nil, err
	}
	store.Author = files.AuthorHashes()
	switch operation {
	case "verify":
		sets, err := store.Sets(ctx, serial)
		if err != nil {
			return nil, err
		}
		if len(sets) == 0 {
			return nil, &stage.Problem{Code: "backup-invalid", Detail: serial}
		}
		for _, set := range sets {
			if _, err := store.Validate(ctx, serial, set.Set); err != nil {
				return nil, &stage.Problem{Code: "backup-restore-set", Detail: set.Set}
			}
		}
		return s.backupListing(ctx, p, serial, sets)
	case "import":
		return store.Import(ctx, dir, serial)
	}
	return nil, errors.New("unknown backup operation / 未知备份操作")
}

func (s *Service) recordLinuxTarget(ctx context.Context, p profile.Profile, serial string) {
	c, err := base.OneCandidate(s.scanDevices(ctx, false), profile.ModeADB)
	if err != nil {
		return
	}
	identity := base.Identity{Mode: profile.ModeADB, Serial: serial, Location: c.USB.Location()}
	s.mu.Lock()
	s.installTarget = &InstallTarget{Device: *c, Profile: p.ID, Name: p.Name}
	s.baseIdentity = &identity
	s.mu.Unlock()
}

// InstallPlan returns the cached preview, including the explicit repair-plus-layers variant
// of full reinstall. It performs no I/O and never infers permission to rebuild the chain.
func (s *Service) InstallPlan(req stage.Request) (stage.Choice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.installSnapshotLocked()
	var p profile.Profile
	if state.Target != nil {
		for _, candidate := range s.profiles {
			if candidate.ID == state.Target.Profile {
				p = candidate
			}
		}
	}
	for _, c := range state.Choices {
		if c.ID == req.Choice {
			if req.BasePlan != "" {
				changed, problem := s.baseVariantLocked(p, c, req.BasePlan)
				if problem != nil {
					return changed, problem
				}
				return changed, nil
			}
			return c, nil
		}
	}
	return stage.Choice{}, &stage.Problem{Code: "bad-choice", Detail: req.Choice}
}
func (s *Service) baseVariantLocked(p profile.Profile, c stage.Choice, kind string) (stage.Choice, *stage.Problem) {
	if c.ID != "full-reinstall" || kind != "repair" {
		return c, &stage.Problem{Code: "bad-plan"}
	}
	if s.baseIdentity == nil {
		return c, &stage.Problem{Code: "base-unconfirmed"}
	}
	plan := s.basePlanLocked(p, kind, *s.baseIdentity)
	if plan.Problem != nil {
		return c, plan.Problem
	}
	tail := []stage.Step{}
	for _, step := range c.Steps {
		if step.Layer != "base" {
			tail = append(tail, step)
		}
	}
	c.BasePlan = kind
	c.Steps = append(base.Steps(plan), tail...)
	if c.Problem != nil && c.Problem.Code == "wrong-mode" {
		c.Problem = nil
		c.Reason = nil
	}
	c.Ready = c.Problem == nil
	for _, need := range c.Needs {
		c.Ready = c.Ready && need.Ready
	}
	return c, nil
}

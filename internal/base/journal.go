// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

const JournalSchema = "org.openxplay.flasher-job/1"

type Journal struct {
	CompletedAt map[string]time.Time `json:"completedAt,omitempty"`
	Schema      string               `json:"schema"`
	Profile     string               `json:"profile"`
	Serial      string               `json:"serial"`
	Plan        string               `json:"plan"`
	Choice      string               `json:"choice"`
	Started     time.Time            `json:"started"`
	Updated     time.Time            `json:"updated"`
	Completed   []string             `json:"completed"`
	InFlight    string               `json:"inFlight,omitempty"`
	CapturedSet string               `json:"capturedSet,omitempty"`
	RestoreSet  string               `json:"restoreSet,omitempty"`
	Finished    bool                 `json:"finished"`
}

type Journals struct{ Dir string }

func (j Journals) path(profile, serial string) (string, error) {
	if !ValidSerial(profile) || !ValidSerial(serial) {
		return "", &stage.Problem{Code: "bad-serial"}
	}
	return filepath.Join(j.Dir, "jobs", profile+"-"+serial), nil
}

func (j Journals) read(ctx context.Context, path, profile, serial string) (*Journal, error) {
	data, err := catalog.ReadFile(ctx, path, 1<<20)
	if err != nil {
		return nil, err
	}
	var v Journal
	if err = catalog.DecodeStrict(data, &v); err != nil {
		return nil, err
	}
	if v.Schema != JournalSchema || v.Profile != profile || v.Serial != serial || v.Started.IsZero() || v.Updated.IsZero() || v.Plan != "stock" && v.Plan != "system" && v.Plan != "repair" {
		return nil, &stage.Problem{Code: "journal-invalid"}
	}
	return &v, nil
}

// History keeps every job, newest first, and still reads the pre-versioning flat journal.
func (j Journals) History(ctx context.Context, profile, serial string) ([]Journal, error) {
	dir, err := j.path(profile, serial)
	if err != nil {
		return nil, err
	}
	out := []Journal{}
	legacy, err := j.read(ctx, dir+".json", profile, serial)
	if err == nil {
		out = append(out, *legacy)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		v, err := j.read(ctx, filepath.Join(dir, entry.Name()), profile, serial)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b Journal) int { return b.Started.Compare(a.Started) })
	return out, nil
}

func (j Journals) Load(ctx context.Context, profile, serial string) (*Journal, error) {
	history, err := j.History(ctx, profile, serial)
	if err != nil || len(history) == 0 {
		return nil, err
	}
	return &history[0], nil
}

func enteredChain(j Journal) bool {
	chain := func(id string) bool { return strings.HasPrefix(id, "chain-") || strings.HasPrefix(id, "restore-") }
	return chain(j.InFlight) || slices.ContainsFunc(j.Completed, chain)
}

// RepairSource ignores newer jobs that never entered the chain. RestoreSet preserves the
// original safe source across repeated repairs whose own fresh captures may be unusable.
func (j Journals) RepairSource(ctx context.Context, profile, serial string) (*Journal, error) {
	history, err := j.History(ctx, profile, serial)
	if err != nil {
		return nil, err
	}
	for _, v := range history {
		if enteredChain(v) {
			return &v, nil
		}
	}
	return nil, nil
}

func (j Journal) RepairSet() string {
	if j.RestoreSet != "" {
		return j.RestoreSet
	}
	if j.CapturedSet != "" {
		return j.CapturedSet
	}
	// Journals written before backup sets always used the flat backup directory.
	return "legacy"
}

func (j Journals) Save(ctx context.Context, v Journal) error {
	dir, err := j.path(v.Profile, v.Serial)
	if err != nil {
		return err
	}
	if v.Started.IsZero() {
		return &stage.Problem{Code: "journal-invalid"}
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, v.Started.UTC().Format("20060102T150405.000000000Z")+".json")
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = catalog.WriteFile(ctx, path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err = syncDir(dir); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dir))
}

// ResumeChoice never automatically retries a critical write. It offers the explicit repair
// procedure, which validates and restores this serial's backup before rebuilding the chain.
func ResumeChoice(j *Journal) string {
	if j != nil && !j.Finished {
		return "repair"
	}
	return ""
}

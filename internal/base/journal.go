// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	Finished    bool                 `json:"finished"`
}

type Journals struct{ Dir string }

func (j Journals) path(profile, serial string) (string, error) {
	if !ValidSerial(profile) || !ValidSerial(serial) {
		return "", &stage.Problem{Code: "bad-serial"}
	}
	return filepath.Join(j.Dir, "jobs", profile+"-"+serial+".json"), nil
}
func (j Journals) Load(ctx context.Context, profile, serial string) (*Journal, error) {
	path, err := j.path(profile, serial)
	if err != nil {
		return nil, err
	}
	data, err := catalog.ReadFile(ctx, path, 1<<20)
	if os.IsNotExist(err) {
		return nil, nil
	}
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
func (j Journals) Save(ctx context.Context, v Journal) error {
	path, err := j.path(v.Profile, v.Serial)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = catalog.WriteFile(ctx, path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// ResumeChoice never automatically retries a critical write. It offers the explicit repair
// procedure, which validates and restores this serial's backup before rebuilding the chain.
func ResumeChoice(j *Journal) string {
	if j != nil && !j.Finished {
		return "repair"
	}
	return ""
}

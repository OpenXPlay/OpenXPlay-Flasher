// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
)

// The source format differs only in artifact metadata. Pointers distinguish omitted metadata
// from an explicitly invalid zero size or empty digest; "file" never reaches a published index.
type sourceIndex struct {
	Schema    string                  `json:"schema"`
	Channel   string                  `json:"channel"`
	Sequence  uint64                  `json:"sequence"`
	Generated string                  `json:"generated"`
	Devices   map[string]sourceDevice `json:"devices"`
}
type sourceDevice struct {
	Recommended map[string]string `json:"recommended"`
	Layers      []sourceLayer     `json:"layers"`
}
type sourceLayer struct {
	Layer     string            `json:"layer"`
	Version   string            `json:"version"`
	Source    string            `json:"source"`
	Notes     map[string]string `json:"notes,omitempty"`
	Artifacts []sourceArtifact  `json:"artifacts"`
}
type sourceArtifact struct {
	Role   string   `json:"role"`
	Name   string   `json:"name"`
	Size   *int64   `json:"size,omitempty"`
	SHA256 *string  `json:"sha256,omitempty"`
	URLs   []string `json:"urls"`
	File   string   `json:"file,omitempty"`
}

func build(ctx context.Context, path, previous string, fetch bool, client *http.Client, now time.Time) (*catalog.Index, error) {
	b, err := catalog.ReadFile(ctx, path, catalog.MaxIndexSize)
	if err != nil {
		return nil, err
	}
	var source sourceIndex
	if err := catalog.DecodeStrict(b, &source); err != nil {
		return nil, err
	}
	i := &catalog.Index{Schema: source.Schema, Channel: source.Channel, Sequence: source.Sequence, Generated: now.UTC().Format(time.RFC3339), Devices: make(map[string]catalog.Device)}
	if previous != "" {
		b, err := catalog.ReadFile(ctx, previous, catalog.MaxIndexSize)
		if err != nil {
			return nil, err
		}
		old, err := catalog.Decode(b)
		if err != nil {
			return nil, err
		}
		if old.Channel != i.Channel {
			return nil, &catalog.InvalidError{Field: "previous", Reason: "belongs to a different channel"}
		}
		if i.Sequence <= old.Sequence {
			return nil, &catalog.RollbackError{Channel: i.Channel, Sequence: i.Sequence, Highest: old.Sequence}
		}
	}
	for id, dev := range source.Devices {
		d := catalog.Device{Recommended: dev.Recommended}
		for _, layer := range dev.Layers {
			l := catalog.Layer{Layer: layer.Layer, Version: layer.Version, Source: layer.Source, Notes: layer.Notes}
			for _, entry := range layer.Artifacts {
				a := catalog.Artifact{Role: entry.Role, Name: entry.Name, URLs: entry.URLs, Size: 1, SHA256: strings.Repeat("0", 64)}
				if err := a.Validate(); err != nil {
					return nil, err
				}
				switch {
				case entry.File != "":
					if entry.Size != nil || entry.SHA256 != nil {
						return nil, &catalog.InvalidError{Field: a.Name, Reason: "file and size/SHA-256 are mutually exclusive"}
					}
					a.Size, a.SHA256, err = catalog.Hash(ctx, entry.File)
				case entry.Size != nil && entry.SHA256 != nil:
					a.Size, a.SHA256 = *entry.Size, *entry.SHA256
				case entry.Size != nil || entry.SHA256 != nil:
					return nil, &catalog.InvalidError{Field: a.Name, Reason: "size and SHA-256 must be supplied together"}
				case !fetch:
					return nil, &catalog.InvalidError{Field: a.Name, Reason: "missing file or metadata; use --fetch to download and hash"}
				default:
					a.Size, a.SHA256, err = catalog.ArtifactSize(ctx, client, a.URLs[0])
				}
				if err != nil {
					return nil, fmt.Errorf("hash %s: %w", a.Name, err)
				}
				l.Artifacts = append(l.Artifacts, a)
			}
			d.Layers = append(d.Layers, l)
		}
		i.Devices[id] = d
	}
	if err := i.Validate(); err != nil {
		return nil, err
	}
	return i, nil
}

func encodeIndex(i *catalog.Index) ([]byte, error) {
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	// Apply the client's byte cap to the final encoding too, not only to the source.
	if _, err := catalog.Decode(b); err != nil {
		return nil, err
	}
	return b, nil
}

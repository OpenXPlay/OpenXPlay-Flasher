// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Source fetches and remembers verified catalogs. BaseURLs are channel directories; {channel}
// is expanded when present. An environment override is a complete channel directory too.
// Use one Source per data directory; operations on that Source are serialized.
type Source struct {
	DataDir  string
	Version  string
	BaseURLs []string
	Client   *http.Client
	mu       sync.Mutex
	offline  bool
}

// NewSource uses the project website unless OXP_FLASHER_CATALOG_URL supplies a test mirror.
// The override changes only the location, never the keys used by VerifyIndex.
func NewSource(dataDir, version string, client *http.Client) *Source {
	base := os.Getenv("OXP_FLASHER_CATALOG_URL")
	if base == "" {
		base = "https://openxplay.org/flasher/{channel}/"
	}
	if client == nil {
		client = NewHTTPClient()
	}
	return &Source{DataDir: dataDir, Version: version, BaseURLs: []string{base}, Client: client}
}

type channelState struct {
	Sequence uint64 `json:"sequence"`
	Digest   string `json:"digest"`
}
type sourceState struct {
	Channels map[string]channelState `json:"channels"`
}

func (s *Source) root() string { return filepath.Join(s.DataDir, "catalog") }
func (s *Source) readState(ctx context.Context) (sourceState, error) {
	var state sourceState
	b, err := ReadFile(ctx, filepath.Join(s.root(), "state.json"), MaxIndexSize)
	if os.IsNotExist(err) {
		return sourceState{Channels: make(map[string]channelState)}, nil
	}
	if err != nil {
		return state, err
	}
	if err := DecodeStrict(b, &state); err != nil {
		return state, err
	}
	if state.Channels == nil {
		return state, &InvalidError{"state", "missing channels"}
	}
	for channel, entry := range state.Channels {
		if !validID(channel) || entry.Sequence < 1 || !digest.MatchString(entry.Digest) {
			return state, &InvalidError{"state", "invalid channel record"}
		}
	}
	return state, nil
}

func checkSequence(i *Index, channel string, state sourceState) error {
	if i.Channel != channel {
		return &InvalidError{"channel", "catalog does not belong to the requested channel"}
	}
	if highest := state.Channels[channel].Sequence; i.Sequence < highest {
		return &RollbackError{channel, i.Sequence, highest}
	}
	return nil
}

// Fetch tries each mirror, then the last verified copy if all mirrors are unavailable. Invalid
// signatures, formats, oversized responses and rollbacks are returned, not hidden by fallback.
func (s *Source) Fetch(ctx context.Context, channel string) (*Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validID(channel) {
		return nil, &InvalidError{"channel", "invalid channel name"}
	}
	state, err := s.readState(ctx)
	if err != nil {
		return nil, err
	}
	var failures []error
	for _, base := range s.BaseURLs {
		base = strings.TrimRight(strings.ReplaceAll(base, "{channel}", channel), "/") + "/"
		data, err := get(ctx, s.Client, base+"index.json", "OpenXPlay-Flasher/"+s.Version, MaxIndexSize)
		var sig []byte
		if err == nil {
			sig, err = get(ctx, s.Client, base+"index.json.minisig", "OpenXPlay-Flasher/"+s.Version, MaxSignatureSize)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, ErrTooLarge) {
				return nil, err
			}
			failures = append(failures, err)
			continue
		}
		i, err := VerifyIndex(data, sig)
		if err != nil {
			return nil, err
		}
		if err := checkSequence(i, channel, state); err != nil {
			return nil, err
		}
		if err := s.accept(ctx, i, data, sig, state); err != nil {
			return nil, err
		}
		s.offline = false
		return i, nil
	}
	i, err := s.loadCached(ctx, channel, state)
	if err == nil {
		s.offline = true
		return i, nil
	}
	return nil, errors.Join(append(failures, err)...)
}

// LoadCached re-verifies both files and enforces the persisted sequence without network access.
func (s *Source) LoadCached(ctx context.Context, channel string) (*Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(channel) {
		return nil, &InvalidError{"channel", "invalid channel name"}
	}
	state, err := s.readState(ctx)
	if err != nil {
		return nil, err
	}
	return s.loadCached(ctx, channel, state)
}

func (s *Source) loadCached(ctx context.Context, channel string, state sourceState) (*Index, error) {
	entry, ok := state.Channels[channel]
	if !ok {
		return nil, fmt.Errorf("catalog: no verified offline index for %s", channel)
	}
	dir := filepath.Join(s.root(), channel, entry.Digest)
	data, err := ReadFile(ctx, filepath.Join(dir, "index.json"), MaxIndexSize)
	if err != nil {
		return nil, err
	}
	sig, err := ReadFile(ctx, filepath.Join(dir, "index.json.minisig"), MaxSignatureSize)
	if err != nil {
		return nil, err
	}
	i, err := VerifyIndex(data, sig)
	if err != nil {
		return nil, err
	}
	if err := checkSequence(i, channel, state); err != nil {
		return nil, err
	}
	return i, nil
}

func (s *Source) accept(ctx context.Context, i *Index, data, sig []byte, state sourceState) error {
	parent := filepath.Join(s.root(), i.Channel)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	h := sha256.New()
	h.Write(data)
	h.Write(sig)
	id := hex.EncodeToString(h.Sum(nil))
	final := filepath.Join(parent, id)
	if _, err := os.Stat(final); os.IsNotExist(err) {
		tmp, err := os.MkdirTemp(parent, ".verified-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := WriteFile(ctx, filepath.Join(tmp, "index.json"), data, 0o600); err != nil {
			return err
		}
		if err := WriteFile(ctx, filepath.Join(tmp, "index.json.minisig"), sig, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, final); err != nil {
			return err
		}
	} else {
		if err != nil {
			return err
		}
		// A previous copy may have been damaged on disk. Refresh it from the bytes we just
		// verified, rather than letting an existing directory prevent offline recovery.
		if err := WriteFile(ctx, filepath.Join(final, "index.json"), data, 0o600); err != nil {
			return err
		}
		if err := WriteFile(ctx, filepath.Join(final, "index.json.minisig"), sig, 0o600); err != nil {
			return err
		}
	}
	// Commit the pointer and high-water mark together, only after both files are complete.
	state.Channels[i.Channel] = channelState{i.Sequence, id}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(ctx, filepath.Join(s.root(), "state.json"), append(b, '\n'), 0o600)
}

// Offline describes the last successful Fetch; it never changes catalog trust checks.
func (s *Source) Offline() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.offline }

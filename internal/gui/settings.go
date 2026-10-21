// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Languages the UI ships, as BCP 47 tags.
const (
	LangEnglish = "en"
	LangChinese = "zh-CN"
)

// Settings are the user's GUI preferences, kept in settings.json in the app's config folder.
type Settings struct {
	// Lang is "" to follow the system, or one of the shipped languages.
	RecentHosts                []RecentHost `json:"recentHosts,omitempty"`
	Lang                       string       `json:"lang"`
	VerifyPackageListSignature bool         `json:"verifyPackageListSignature"`
}

func defaultSettings() Settings { return Settings{VerifyPackageListSignature: true} }

// SettingsStore reads and writes Settings in one file.
type SettingsStore struct {
	mu   sync.Mutex
	path string
}

// ConfigDir is the app's folder in the user's config directory
// (~/Library/Application Support, %AppData%, $XDG_CONFIG_HOME).
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "OpenXPlay Flasher"), nil
}

func NewSettingsStore(dir string) *SettingsStore {
	return &SettingsStore{path: filepath.Join(dir, "settings.json")}
}

// Load returns the saved settings; a missing or unreadable file gives the defaults.
func (s *SettingsStore) Load() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *SettingsStore) loadLocked() Settings {
	out := defaultSettings()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return out
	}
	if json.Unmarshal(data, &out) != nil {
		return defaultSettings()
	}
	out.Lang = NormalizeLang(out.Lang)
	return out
}

// Save writes the settings atomically.
func (s *SettingsStore) Save(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(v)
}

func (s *SettingsStore) saveLocked(v Settings) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// NormalizeLang maps a system or user language tag to a shipped language, or "" when it is
// neither. Every Chinese variant gets Simplified Chinese, the only Chinese the UI ships.
func NormalizeLang(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag, _, _ = strings.Cut(tag, ".") // zh_CN.UTF-8
	tag = strings.ReplaceAll(tag, "_", "-")
	switch {
	case tag == "":
		return ""
	case tag == "zh" || strings.HasPrefix(tag, "zh-"):
		return LangChinese
	case tag == "en" || strings.HasPrefix(tag, "en-"):
		return LangEnglish
	}
	return ""
}

// UILang is the language the page starts in: the user's choice, else the system's, else English.
func UILang(saved, system string) string {
	if l := NormalizeLang(saved); l != "" {
		return l
	}
	if l := NormalizeLang(system); l != "" {
		return l
	}
	return LangEnglish
}

// RememberHost persists only the allowlisted form metadata, newest first.
func (s *SettingsStore) RememberHost(host RecentHost) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.loadLocked()
	recent := []RecentHost{host}
	for _, old := range v.RecentHosts {
		if old.Host != host.Host || old.Port != host.Port || old.User != host.User || old.Mode != host.Mode {
			recent = append(recent, old)
		}
	}
	v.RecentHosts = recent[:min(10, len(recent))]
	return s.saveLocked(v)
}

// Update merges a preference patch with recent hosts under the same store lock.
func (s *SettingsStore) Update(change func(*Settings)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.loadLocked()
	change(&v)
	return v, s.saveLocked(v)
}

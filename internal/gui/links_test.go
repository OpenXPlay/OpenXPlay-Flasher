// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHelpLinksInBothLanguagesAreOpenable(t *testing.T) {
	want := map[string][2]string{
		LangEnglish: {"https://openxplay.org/en/ufi003-relay#flasher", "https://openxplay.org/en/ufi003-relay#flasher-download"},
		LangChinese: {"https://openxplay.org/ufi003-relay#flasher", "https://openxplay.org/ufi003-relay#flasher-download"},
	}
	if len(helpLinks) != len(want) {
		t.Fatalf("help link languages = %v", helpLinks)
	}
	f := newFixture(t)
	for lang, urls := range want {
		links, ok := helpLinks[lang]
		if !ok || linksFor(lang) != links || links.Tutorial != urls[0] || links.Download != urls[1] ||
			links.Issues != "https://github.com/OpenXPlay/OpenXPlay-Flasher/issues" {
			t.Fatalf("%s links = %+v", lang, links)
		}
		for _, url := range []string{links.Website, links.Tutorial, links.Download, links.Source, links.Issues} {
			if url == "" || !canOpen(url) {
				t.Errorf("%s URL %q is not openable", lang, url)
			}
			body, _ := json.Marshal(map[string]string{"url": url})
			if rec := f.do("POST", "/api/open", string(body)); rec.Code != http.StatusNoContent {
				t.Errorf("opening %s: %d %s", url, rec.Code, rec.Body)
			}
		}
	}
	if linksFor("fr") != helpLinks[LangEnglish] {
		t.Fatal("unknown language did not fall back to English")
	}
}

func TestPageReceivesTheSharedLinkTable(t *testing.T) {
	f := newFixture(t)
	body := f.do("GET", "/boot.js", "").Body.String()
	var prefs struct {
		Links map[string]Links `json:"links"`
	}
	data := strings.TrimSuffix(strings.TrimPrefix(body, "window.bootPrefs = "), ";\n")
	if err := json.Unmarshal([]byte(data), &prefs); err != nil {
		t.Fatal(err)
	}
	if len(prefs.Links) != len(helpLinks) {
		t.Fatalf("page link table = %+v", prefs.Links)
	}
	for lang, links := range helpLinks {
		if prefs.Links[lang] != links {
			t.Errorf("%s page links = %+v, want %+v", lang, prefs.Links[lang], links)
		}
	}
}

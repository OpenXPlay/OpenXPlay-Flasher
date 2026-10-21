// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import "github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"

// helpLinks is shared by the native menus and the page (through boot.js).
var helpLinks = map[string]Links{
	LangEnglish: {
		Website:  "https://openxplay.org/en/",
		Tutorial: "https://openxplay.org/en/ufi003-relay#flasher",
		Download: "https://openxplay.org/en/ufi003-relay#flasher-download",
		Source:   version.SourceURL,
		Issues:   version.SourceURL + "/issues",
	},
	LangChinese: {
		Website:  "https://openxplay.org/",
		Tutorial: "https://openxplay.org/ufi003-relay#flasher",
		Download: "https://openxplay.org/ufi003-relay#flasher-download",
		Source:   version.SourceURL,
		Issues:   version.SourceURL + "/issues",
	},
}

type Links struct {
	Website  string `json:"website"`
	Tutorial string `json:"tutorial"`
	Download string `json:"download"`
	Source   string `json:"source"`
	Issues   string `json:"issues"`
}

func linksFor(lang string) Links {
	if links, ok := helpLinks[lang]; ok {
		return links
	}
	return helpLinks[LangEnglish]
}

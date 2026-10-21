// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import "github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"

// helpLinks is shared by the native menus and the page (through boot.js).
var helpLinks = map[string]Links{
	LangEnglish: {
		Website:   "https://openxplay.org/en/",
		PhicommN1: "https://openxplay.org/en/phicomm-n1",
		Tutorial:  "https://openxplay.org/en/ufi003-relay#flasher",
		Download:  "https://openxplay.org/en/ufi003-relay#flasher-download",
		Manual:    "https://openxplay.org/en/ufi003-flash",
		Source:    version.SourceURL,
		Issues:    version.SourceURL + "/issues",
	},
	LangChinese: {
		Website:   "https://openxplay.org/",
		PhicommN1: "https://openxplay.org/phicomm-n1",
		Tutorial:  "https://openxplay.org/ufi003-relay#flasher",
		Download:  "https://openxplay.org/ufi003-relay#flasher-download",
		Manual:    "https://openxplay.org/ufi003-flash",
		Source:    version.SourceURL,
		Issues:    version.SourceURL + "/issues",
	},
}

type Links struct {
	PhicommN1 string `json:"phicommN1"`
	Website   string `json:"website"`
	Tutorial  string `json:"tutorial"`
	Download  string `json:"download"`
	Manual    string `json:"manual"`
	Source    string `json:"source"`
	Issues    string `json:"issues"`
}

func linksFor(lang string) Links {
	if links, ok := helpLinks[lang]; ok {
		return links
	}
	return helpLinks[LangEnglish]
}

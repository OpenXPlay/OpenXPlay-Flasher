// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { ZH, t, setLang } from "../assets/i18n.js";

const read = (name) => readFileSync(new URL("../assets/" + name, import.meta.url), "utf8");
const html = read("index.html");
const scripts = ["app.js", "devices.js", "config.js", "configlogic.js", "install.js", "installlogic.js", "settings.js", "settingslogic.js"].map(read).join("\n");
const nativeText = readFileSync(new URL("../text.go", import.meta.url), "utf8");
// Native menus keep their own strings; the new labels must match the page's translations.
const nativeEnglish = nativeText.slice(nativeText.lastIndexOf("return menuText{"));

// Every English string the page can show: data-t attributes, t("...") calls and the mode labels.
function usedStrings() {
  const out = new Set();
  for (const m of html.matchAll(/data-ta?="([^"]+)"/g)) out.add(m[1]);
  for (const m of scripts.matchAll(/\bt\(\s*"((?:[^"\\]|\\.)*)"/g)) out.add(JSON.parse('"' + m[1] + '"'));
  for (const m of read("devices.js").matchAll(/^\s+\w+: "([^"]+)",$/gm)) out.add(m[1]);
  for (const m of read("configlogic.js").matchAll(/^\s+"[\w-]+": "([^"]+)",$/gm)) out.add(m[1]);
  for (const m of read("installlogic.js").matchAll(/^\s+"[\w-]+": "([^"]+)",$/gm)) out.add(m[1]);
  for (const m of scripts.matchAll(/status\("\w+", "([^"]+)"(?:,\s*"([^"]+)")?/g)) {
    out.add(m[1]);
    if (m[2]) out.add(m[2]);
  }
  for (const m of nativeEnglish.matchAll(/\b(?:Settings|Tutorial|Download): "([^"]+)"/g)) out.add(m[1]);
  const nativeQuit = nativeText.slice(nativeText.indexOf('return quitText{Warning: "Flashing'), nativeText.indexOf('// siteURL'));
  for (const m of nativeQuit.matchAll(/\b(?:Warning|Keep|Quit): "([^"]+)"/g)) out.add(m[1]);
  return out;
}

test("every shown string has a Chinese translation", () => {
  const missing = [...usedStrings()].filter((s) => !(s in ZH));
  assert.deepEqual(missing, []);
});

test("every translation is used", () => {
  const used = usedStrings();
  const stale = Object.keys(ZH).filter((s) => !used.has(s));
  assert.deepEqual(stale, []);
});

test("translations keep their placeholders", () => {
  const holes = (s) => (s.match(/\{\w+\}/g) || []).sort().join(",");
  for (const [en, zh] of Object.entries(ZH)) assert.equal(holes(zh), holes(en), en);
});

test("new native menu labels match the page translations", () => {
  for (const key of ["Settings", "Tutorial", "Download"]) {
    const labels = [...nativeText.matchAll(new RegExp("\\b" + key + ': "([^"]+)"', "g"))].map((m) => m[1]);
    assert.equal(labels.length, 2, key);
    assert.equal(ZH[labels[1]], labels[0], key);
  }
});

test("native quit warnings and buttons match the page translations", () => {
  const section = nativeText.slice(nativeText.indexOf("func quitStrings"), nativeText.indexOf("// siteURL"));
  for (const key of ["Warning", "Keep", "Quit"]) {
    const labels = [...section.matchAll(new RegExp("\\b" + key + ': "([^"]+)"', "g"))].map((m) => m[1]);
    assert.equal(labels.length, 2, key);
    assert.equal(ZH[labels[1]], labels[0], key);
  }
});

test("t fills placeholders and falls back to English", () => {
  setLang("zh-CN");
  assert.equal(t("Version {v}", { v: "0.1.0" }), "版本 0.1.0");
  assert.equal(t("Not translated"), "Not translated");
  setLang("fr");
  assert.equal(t("Connect"), "Connect");
});

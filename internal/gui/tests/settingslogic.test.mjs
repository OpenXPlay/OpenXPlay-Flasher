// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { aboutRows, aboutText, effectiveLanguage, linksFor, viewFromHash, viewAllowed } from "../assets/settingslogic.js";
import { t, setLang } from "../assets/i18n.js";

const info = {
  name: "OpenXPlay Flasher", version: "0.1.0", commit: "abc1234", buildDate: "2026-10-21T12:00:00Z",
  license: "AGPL-3.0-only", sourceUrl: "https://github.com/OpenXPlay/OpenXPlay-Flasher",
  platform: "darwin", architecture: "arm64", osVersion: "26.0",
  adb: { mode: "auto", serverAddr: "127.0.0.1:5037", lastTransport: "usb" },
};

test("About and its plain text report contain localized build and connection details", () => {
  setLang("en");
  const rows = aboutRows(info, t);
  assert.equal(rows.find((r) => r.label === "License").href, info.sourceUrl);
  assert.equal(aboutText(info, t), [
    "Product: OpenXPlay Flasher", "Version: 0.1.0", "Commit: abc1234", "Build date: 2026-10-21T12:00:00Z",
    "License: AGPL-3.0-only (https://github.com/OpenXPlay/OpenXPlay-Flasher)", "Platform: darwin/arm64",
    "OS version: 26.0", "Verify the package list signature: On", "adb connection: Automatic: adb server at 127.0.0.1:5037, otherwise native USB",
    "Last adb connection: Native USB",
  ].join("\n"));
  setLang("zh-CN");
  const zh = aboutText(info, t);
  assert.ok(zh.includes("产品：OpenXPlay 刷机工具"));
  assert.ok(zh.includes("构建日期：2026-10-21T12:00:00Z"));
  assert.ok(zh.includes("上次 adb 连接：原生 USB"));
  assert.ok(!zh.includes("<"));
  setLang("en");
});

test("unavailable metadata stays unknown and optional fields are omitted", () => {
  setLang("en");
  assert.deepEqual(aboutRows(null, t), []);
  assert.equal(aboutText(null, t), "");
  const text = aboutText({ ...info, commit: "", buildDate: "", osVersion: "", adb: null }, t);
  assert.ok(text.includes("Commit: Unknown"));
  assert.ok(text.includes("adb connection: Unknown"));
  assert.ok(!text.includes("Build date:"));
  assert.ok(!text.includes("OS version:"));
  assert.ok(!text.includes("Last adb connection:"));
});

test("server and native USB routes are reported without assuming an active connection", () => {
  setLang("en");
  const text = (adb) => aboutText({ ...info, adb }, t);
  assert.ok(text({ mode: "server", serverAddr: "127.0.0.1:5037" }).includes("adb connection: adb server at 127.0.0.1:5037"));
  assert.ok(text({ mode: "usb" }).includes("adb connection: Native USB"));
  assert.ok(!text({ mode: "auto", serverAddr: "127.0.0.1:5037" }).includes("Last adb connection:"));
  assert.ok(text({ ...info.adb, lastTransport: "server" }).includes("Last adb connection: adb server at 127.0.0.1:5037"));
});

test("language choices override the system and an empty choice follows it", () => {
  assert.equal(effectiveLanguage("en", "zh-CN"), "en");
  assert.equal(effectiveLanguage("zh-CN", "en"), "zh-CN");
  assert.equal(effectiveLanguage("", "zh-CN"), "zh-CN");
  assert.equal(effectiveLanguage("", "en"), "en");
  assert.equal(effectiveLanguage("", "fr"), "en");
});

test("page links select the Go-provided language table with English fallback", () => {
  const links = { en: { tutorial: "english", download: "english-download" }, "zh-CN": { tutorial: "chinese", download: "chinese-download" } };
  assert.equal(linksFor(links, "en"), links.en);
  assert.equal(linksFor(links, "zh-CN"), links["zh-CN"]);
  assert.equal(linksFor(links, "fr"), links.en);
  assert.deepEqual(linksFor({}, "en"), {});
});

test("hash navigation opens Settings and keeps it and install progress reachable during an install", () => {
  for (const view of ["install", "config", "settings"]) {
    assert.equal(viewFromHash("#" + view), view);
    assert.equal(viewAllowed(view, false), true);
  }
  assert.equal(viewFromHash(""), "install");
  assert.equal(viewFromHash("#unknown"), "install");
  assert.equal(viewAllowed("settings", true), true);
  assert.equal(viewAllowed("install", true), true);
  assert.equal(viewAllowed("config", true), false);
  assert.equal(viewAllowed("unknown", false), false);
});

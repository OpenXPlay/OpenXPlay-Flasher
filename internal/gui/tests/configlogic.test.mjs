// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { checkContent, errorText } from "../assets/configlogic.js";
import { t, setLang } from "../assets/i18n.js";

test("JSON content must be one object, like the Go check", () => {
  assert.equal(checkContent("json", '{"hotspotChannel":36}').ok, true);
  assert.equal(checkContent("json", '{"a":').ok, false);
  assert.equal(checkContent("json", "[1]").ok, false);
  assert.equal(checkContent("json", "null").ok, false);
  assert.equal(checkContent("text", "anything = 1").ok, true);
  assert.equal(checkContent("text", "a\u0000b").ok, false);
});

test("API error codes become sentences in the UI language", () => {
  setLang("zh-CN");
  assert.equal(errorText(t, { code: "protected" }), "此文件受保护，无法打开。");
  assert.equal(errorText(t, { code: "invalid", message: "invalid JSON: x" }), "内容无效：invalid JSON: x");
  assert.equal(errorText(t, { code: "device", message: "exit 1: boom" }), "设备返回错误：exit 1: boom");
  setLang("en");
  assert.equal(errorText(t, { code: "no-device" }), "Connect a supported device that runs Linux; its configuration files appear here.");
});

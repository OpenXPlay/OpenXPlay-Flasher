// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { NodeFake, walk } from "./dom-fixture.mjs";
import { t, setLang } from "../assets/i18n.js";
import { aboutText } from "../assets/settingslogic.js";
import * as page from "../assets/settings.js";

for (const language of ["en", "zh-CN"]) test("signature switch confirms only turning off in " + language, async () => {
  setLang(language);
  globalThis.Node = NodeFake;
  const root = new NodeFake("div"); root.id = "settings";
  globalThis.document = {
    activeElement: null,
    createElement: (tag) => new NodeFake(tag), createTextNode: (text) => new NodeFake("", text),
    getElementById: (id) => walk(root).find((n) => n.id === id),
  };
  page.init();
  const calls = [];
  const prefs = { info: { name: "OpenXPlay Flasher", license: "AGPL-3.0-only", platform: "darwin", architecture: "arm64", verifyPackageListSignature: true },
    links: {}, signatureCheck: true, savedLang: "en", onSignatureChange: (enabled) => {
      calls.push(enabled); prefs.signatureCheck = enabled; prefs.info.verifyPackageListSignature = enabled; page.render(prefs);
    } };
  const get = document.getElementById;
  const change = (checked) => get("settings-signature").listeners.change[0]({ target: { checked } });
  page.render(prefs);
  assert.equal(get("settings-signature").checked, true);
  assert.equal(get("settings-signature").attributes.role, "switch");
  change(false);
  assert.deepEqual(calls, [], "opening the confirmation must not save");
  assert.ok(root.textContent.includes(t("The app can then no longer tell whether the package list comes from OpenXPlay. Turn off the signature check?")));
  assert.equal(get("settings-signature").checked, true, "the switch stays on until confirmed");
  await get("signature-keep").click();
  assert.deepEqual(calls, []);
  assert.equal(get("signature-confirm-title"), undefined);
  change(false);
  await get("signature-turn-off").click();
  assert.deepEqual(calls, [false]);
  assert.equal(get("signature-confirm-title"), undefined);
  assert.ok(aboutText(prefs.info, t).includes(t("{label}: {value}", { label: t("Verify the package list signature"), value: t("Off") })));
  page.render(prefs);
  assert.equal(get("signature-confirm-title"), undefined, "refresh does not ask again");
  change(true);
  assert.deepEqual(calls, [false, true], "turning on is immediate");
  assert.equal(get("signature-confirm-title"), undefined);
  assert.ok(aboutText(prefs.info, t).includes(t("{label}: {value}", { label: t("Verify the package list signature"), value: t("On") })));
  change(false);
  assert.ok(get("signature-confirm-title"), "a later turn-off asks once again");
});

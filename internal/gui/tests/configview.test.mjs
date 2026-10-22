// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { resetPrivacy } from "../assets/privacy.js";
import { setLang } from "../assets/i18n.js";

// A small DOM adapter drives the actual view modules, without a browser or a new dependency.
class Node {
  constructor(tag = "#text", text = "") {
    this.tag = tag; this.text = text; this.children = []; this.attrs = {}; this.events = {};
    this.value = ""; this.scrollTop = 0;
    if (tag === "input" || tag === "textarea") { this.selectionStart = 0; this.selectionEnd = 0; }
  }
  append(child) { this.children.push(child); child.parent = this; }
  replaceChildren(...children) { this.children = []; children.forEach((child) => this.append(child)); }
  setAttribute(key, value) {
    this.attrs[key] = value;
    if (["id", "value", "type"].includes(key)) this[key] = value;
  }
  get disabled() { return Object.hasOwn(this.attrs, "disabled"); }
  set disabled(value) { if (value) this.attrs.disabled = ""; else delete this.attrs.disabled; }
  get checked() { return Object.hasOwn(this.attrs, "checked"); }
  set checked(value) { if (value) this.attrs.checked = ""; else delete this.attrs.checked; }
  set className(value) { this.attrs.class = value; }
  get textContent() { return this.text + this.children.map((child) => child.textContent).join(""); }
  set textContent(text) { this.text = text; this.children = []; }
  addEventListener(name, action) { this.events[name] = action; }
  contains(node) { return this === node || this.children.some((child) => child.contains(node)); }
  focus() { document.activeElement = this; }
  setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  remove() { this.parent.children = this.parent.children.filter((child) => child !== this); }
  async fire(name, extra = {}) { return this.events[name]?.({ target: this, preventDefault() {}, ...extra }); }
  async click() { if (!this.disabled) return this.fire("click"); }
}
function all(root) { return [root, ...root.children.flatMap(all)]; }
function button(root, text) {
  const node = all(root).find((node) => node.tag === "button" && node.textContent === text);
  assert.ok(node, "button exists: " + text); return node;
}
const tick = () => new Promise(setImmediate);
let serial = 0;
async function fixture() {
  resetPrivacy(); setLang("en");
  const root = new Node("section"); root.id = "config";
  const created = [];
  globalThis.Node = Node;
  globalThis.document = {
    activeElement: null,
    createElement(tag) { const node = new Node(tag); created.push(node); return node; },
    createTextNode(text) { return new Node("#text", text); },
    getElementById(id) { return all(root).find((node) => node.id === id) || null; },
  };
  let targetError = "";
  const target = { profile: "ufi003", device: { serial: "a" }, files: [
    { id: "settings", category: "user", path: "/settings.json", format: "json", restartService: true, name: { en: "Settings" } },
    { id: "host", category: "system", path: "/host.json", format: "json", restartService: true, name: { en: "Host" } },
    { id: "public-name", category: "privacy", path: "/public-name.json", format: "public-name" },
  ] };
  let privateValue = { exists: true, enabled: true, provider: "dynv6", name: "car.example.net", tokenSet: true, agreeLetsEncryptTerms: true, errors: {} };
  let failSave = false;
  let failRemove = false;
  let readError = "";
  let backupVersion = 0;
  let removed = false;
  const calls = [];
  globalThis.fetch = async (url, options) => {
    const route = url.slice(5), body = options.body ? JSON.parse(options.body) : undefined;
    calls.push({ route, body });
    let data, code = "";
    if (route === "config") { data = structuredClone(target); code = targetError; }
    else if (route === "config/read") { data = { exists: true, content: "{}", mode: "644", owner: "root:root" }; code = readError; }
    else if (route === "config/privacy/read") data = { ...privateValue };
    else if (route === "config/privacy/write") {
      if (failSave) code = "device";
      else { const { id, token, ...patch } = body; privateValue = { ...privateValue, ...patch }; data = { ...privateValue }; }
    } else if (route === "config/privacy/backups") data = removed ? [] : backupVersion ? [{ id: "20261021-010000-2", time: "2026-10-21T01:00:00Z" }] : [{ id: "20261020-010000-1", time: "2026-10-20T01:00:00Z" }];
    else if (route === "config/privacy/restore") { backupVersion++; data = { ...privateValue }; }
    else if (route === "config/privacy/remove") {
      if (failRemove) code = "device";
      else { removed = true; privateValue = { ...privateValue, exists: false, enabled: false, agreeLetsEncryptTerms: false }; data = { ...privateValue }; }
    }
    else data = { healthy: true };
    return { ok: !code, status: code ? 409 : 200, statusText: "error", json: async () => code ? { code, error: code } : data };
  };
  const module = await import("../assets/config.js?test=" + (++serial));
  module.init({ web: true }); await module.refresh();
  return { root, target, calls, created, module,
    setTargetError(value) { targetError = value; }, setReadError(value) { readError = value; },
    setFailSave(value) { failSave = value; }, setFailRemove(value) { failRemove = value; }, withdrawTerms() { privateValue.agreeLetsEncryptTerms = false; },
    async privacy() { await button(root, "Privacy").click(); await tick(); },
  };
}

test("failed privacy saves retain a new token for retry; successful saves clear it and patch only edits", async () => {
  const f = await fixture(); await f.privacy();
  const token = document.getElementById("privacy-token"); token.value = "new-private-token"; await token.fire("input");
  f.setFailSave(true); await button(f.root, "Save").click();
  assert.equal(document.getElementById("privacy-token").value, "new-private-token");
  f.setFailSave(false); f.withdrawTerms(); await button(f.root, "Save").click();
  const saved = f.calls.filter((call) => call.route === "config/privacy/write");
  assert.deepEqual(saved[1].body, { id: "public-name", token: "new-private-token" });
  assert.equal(document.getElementById("privacy-token").value, "");
  assert.equal(document.getElementById("privacy-agreeLetsEncryptTerms").checked, false);
});

test("privacy re-entry rereads consent, refresh events preserve focus and caret", async () => {
  const f = await fixture(); await f.privacy();
  const name = document.getElementById("privacy-name"); name.focus(); name.setSelectionRange(2, 4);
  name.value = "edited.example.net"; await name.fire("input");
  await f.module.refresh();
  assert.equal(document.activeElement.id, "privacy-name");
  assert.equal(document.activeElement.value, "edited.example.net");
  assert.equal(document.activeElement.selectionStart, 2);
  await button(f.root, "User").click(); f.withdrawTerms(); await f.privacy();
  assert.equal(document.getElementById("privacy-agreeLetsEncryptTerms").checked, false);
  const reads = () => f.calls.filter((call) => call.route === "config/privacy/read").length;
  const count = reads(); await f.module.refresh(true); await tick(); assert.equal(reads(), count + 1);
  f.target.device.serial = "replacement";
  await f.module.refresh(); await tick(); assert.equal(reads(), count + 2);
});

test("raw System/User edits survive unavailable or changed devices and Save stays blocked", async () => {
  for (const category of ["User", "System"]) {
    const f = await fixture(); await button(f.root, category).click();
    const file = all(f.root).find((node) => node.tag === "button" && node.textContent.startsWith(category === "User" ? "Settings" : "Host"));
    await file.click();
    let text = document.getElementById("config-text"); text.value = '{"unsaved":true}'; await text.fire("input");
    assert.equal(document.getElementById("config-save").disabled, false);
    f.setTargetError("several-devices"); await f.module.refresh();
    assert.equal(document.getElementById("config-text").value, '{"unsaved":true}');
    assert.equal(document.getElementById("config-save").disabled, true);
    f.setTargetError(""); f.target.device.serial = "b"; await f.module.refresh();
    assert.equal(document.getElementById("config-text").value, '{"unsaved":true}');
    assert.equal(document.getElementById("config-save").disabled, true);
    f.target.device.serial = "a"; await f.module.refresh();
    assert.equal(document.getElementById("config-text").value, '{"unsaved":true}');
    assert.equal(document.getElementById("config-save").disabled, false);
  }
});

test("offline profile editors retain JSON validation and their restart policy", async () => {
  for (const category of ["User", "System"]) {
    const f = await fixture(); await button(f.root, category).click();
    await all(f.root).find((node) => node.tag === "button" && node.textContent.startsWith(category === "User" ? "Settings" : "Host")).click();
    f.setTargetError("no-device"); await f.module.refresh();
    assert.match(f.root.textContent, /Saving restarts the relay/);
    assert.doesNotMatch(f.root.textContent, /Restart the relay after saving/);
    const text = document.getElementById("config-text"); text.value = "invalid-json"; await text.fire("input");
    assert.match(document.getElementById("config-check").textContent, /Not valid JSON/);
    assert.equal(document.getElementById("config-save").disabled, true);
  }
});

test("raw open refusal is visible without an editor, for privacy and protected paths", async () => {
  for (const code of ["privacy", "protected"]) {
    const f = await fixture(); f.setReadError(code);
    const file = all(f.root).find((node) => node.tag === "button" && node.textContent.startsWith("Settings")); await file.click();
    assert.equal(document.getElementById("config-text"), null);
    assert.match(document.getElementById("config-result").textContent, code === "privacy" ? /Open it under Privacy/ : /protected/);
  }
});

test("web raw imports reject credential JSON and keep the previous editor text", async () => {
  for (const content of ['{"token":"secret"}', '{"agreeLetsEncryptTerms":false}']) {
    const f = await fixture();
    await all(f.root).find((node) => node.tag === "button" && node.textContent.startsWith("Settings")).click();
    await button(f.root, "Import…").click();
    const input = f.created.findLast((node) => node.type === "file");
    input.files = [{ text: async () => content }]; await input.fire("change"); await tick();
    assert.equal(document.getElementById("config-text").value, "{}");
    assert.match(document.getElementById("config-result").textContent, /Open it under Privacy/);
  }
});

test("Restore and Remove have distinct messages, and Restore refreshes the backup list", async () => {
  const f = await fixture(); await f.privacy(); await button(f.root, "Earlier versions").click();
  await button(f.root, "Restore").click();
  const restoreButtons = all(f.root).filter((node) => node.tag === "button" && node.textContent === "Restore");
  await restoreButtons[0].click();
  assert.match(f.root.textContent, /Restored\. The relay/);
  assert.equal(f.calls.filter((call) => call.route === "config/privacy/backups").length, 2);
  assert.ok(f.root.textContent.includes(new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "medium" }).format(new Date("2026-10-21T01:00:00Z"))));
  await button(f.root, "Remove").click();
  assert.match(f.root.textContent, /Its earlier versions are deleted too, and this cannot be undone/);
  const removes = all(f.root).filter((node) => node.tag === "button" && node.textContent === "Remove"); await removes[1].click();
  assert.match(f.root.textContent, /Removed\. The relay returns/);
  assert.match(f.root.textContent, /No earlier versions yet/);
  assert.equal(f.calls.filter((call) => call.route === "config/privacy/backups").length, 3);
});

test("failed private removal shows an error instead of success", async () => {
  const f = await fixture(); await f.privacy(); await button(f.root, "Earlier versions").click();
  f.setFailRemove(true); await button(f.root, "Remove").click();
  const removes = all(f.root).filter((node) => node.tag === "button" && node.textContent === "Remove");
  await removes[1].click();
  assert.match(f.root.textContent, /The device reported an error/);
  assert.doesNotMatch(f.root.textContent, /Removed\. The relay/);
  assert.doesNotMatch(f.root.textContent, /No earlier versions yet/);
});

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang, t } from "../assets/i18n.js";
import { WARNING_TEXT } from "../assets/installlogic.js";

// A small DOM fake exercises the actual view without a browser dependency or a device service.
class NodeFake {
  constructor(tag = "", text = "") {
    this.tag = tag; this.children = []; this.attributes = {}; this.listeners = {};
    this.className = ""; this.id = ""; this.dataset = {}; this.hidden = false; this.disabled = false; this.text = text;
    this.classList = { toggle: (name, enabled) => {
      const classes = new Set(this.className.split(" ").filter(Boolean));
      if (enabled) classes.add(name); else classes.delete(name);
      this.className = [...classes].join(" ");
    } };
  }
  setAttribute(key, value) {
    this.attributes[key] = value;
    if (["disabled", "checked", "open", "hidden"].includes(key)) this[key] = true;
    else this[key] = value;
    if (key.startsWith("data-")) this.dataset[key.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = value;
  }
  removeAttribute(key) { delete this.attributes[key]; }
  addEventListener(event, fn) { (this.listeners[event] ||= []).push(fn); }
  append(node) { if (!(node instanceof NodeFake)) node = new NodeFake("", String(node)); node.parent = this; this.children.push(node); }
  replaceChildren(...nodes) { this.children = []; for (const node of nodes) this.append(node); }
  contains(node) { return this === node || this.children.some((c) => c.contains(node)); }
  get textContent() { return this.text + this.children.map((c) => c.textContent).join(""); }
  set textContent(text) { this.text = text; this.children = []; }
  focus() { document.activeElement = this; }
  scrollIntoView() {}
  setSelectionRange() {}
  async click() {
    assert.equal(this.disabled, false, "clicked a disabled control: " + this.textContent);
    for (const fn of this.listeners.click || []) await fn({ target: this });
  }
}
const walk = (node) => [node, ...node.children.flatMap(walk)];

function dom() {
  globalThis.Node = NodeFake;
  const body = new NodeFake("body");
  const add = (parent, tag, id) => { const node = new NodeFake(tag); node.id = id; parent.append(node); return node; };
  add(body, "div", "install-running-warning").hidden = true;
  add(body, "section", "install-connect");
  const steps = add(body, "ol", "steps"); steps.className = "steps";
  for (let i = 0; i < 6; i++) {
    const li = add(steps, "li", "step-" + i);
    if (i < 3) add(li, "button", "nav-" + i).setAttribute("data-install-step", String(i));
  }
  const root = add(body, "section", "install");
  globalThis.document = {
    activeElement: body,
    createElement: (tag) => new NodeFake(tag), createTextNode: (text) => new NodeFake("", text),
    getElementById: (id) => walk(body).find((n) => n.id === id),
    querySelectorAll: (selector) => walk(body).filter((n) => selector === "[data-install-step]" ? "installStep" in n.dataset :
      selector === ".steps li" && n.tag === "li" && n.parent === steps),
  };
  const listeners = {};
  globalThis.window = { addEventListener: (name, fn) => { listeners[name] = fn; } };
  return { root, listeners, get: document.getElementById, nodes: () => walk(root),
    button: (text) => walk(root).find((n) => n.tag === "button" && n.textContent === t(text)) };
}

function snapshot() {
  const layers = ["base", "device", "relay"].map((layer, i) => ({ layer, name: {
    en: ["Base system", "Device support", "OpenXPlay relay"][i], "zh-CN": ["基础系统", "设备支持", "OpenXPlay 中继"][i],
  }, installed: "1.0", action: layer === "base" ? "guide" : "update", choice: layer === "base" ? undefined : layer, enabled: true }));
  const choice = (id, ids) => ({ id, layers: ids, destructive: false, ready: false,
    steps: ids.map((layer) => ({ layer, action: "install" })),
    needs: ids.map((layer) => ({ layer, status: "missing", fileTypes: layer === "device" ? [".tar.gz"] : [".deb", ".manifest.json"], ready: false })) });
  return { target: { profile: "ufi003", device: { usb: { serial: "fake-device" } } },
    installed: { layers: { base: "kernel", device: "1.0", relay: "1.0" } }, installedStale: false,
    layers, packages: [], choices: [choice("full", ["device", "relay"]), choice("device", ["device"]), choice("relay", ["relay"])], suggested: "relay" };
}
function ready(s) {
  for (const c of s.choices) {
    c.ready = true;
    for (const n of c.needs) Object.assign(n, { ready: true, status: "checked", comparison: "upgrade",
      package: { id: n.layer, name: n.layer, version: "2.0", size: 1, sha256: "a".repeat(64), checked: true } });
  }
  return s;
}
const boot = { web: true, links: { en: { manual: "https://openxplay.org/en/ufi003-flash" }, "zh-CN": { manual: "https://openxplay.org/ufi003-flash" } } };

for (const language of ["en", "zh-CN"]) test("rendered layer-first flow, warnings and refresh in " + language, async () => {
  const d = dom(); setLang(language);
  const savedInterval = globalThis.setInterval;
  globalThis.setInterval = () => 0;
  const page = await import("../assets/install.js?" + language);
  page.init(boot);
  globalThis.setInterval = savedInterval;
  const s = snapshot(); page.receive(s);
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => snapshot() });
  assert.equal(d.get("install-continue").disabled, false);
  assert.equal(d.get("choice-relay").disabled, false);
  assert.equal(d.nodes().some((n) => n.tag === "input"), false, "Choose shows no package inputs");
  await d.get("install-continue").click();
  assert.deepEqual(d.nodes().filter((n) => n.dataset.packageLayer).map((n) => n.dataset.packageLayer), ["relay"]);
  assert.equal(d.get("install-continue").disabled, true);
  // Picked files use the layer-specific endpoint. Unneeded packages never create rows.
  let request;
  globalThis.fetch = async (url, options) => {
    request ||= { url, body: JSON.parse(options.body) };
    return { ok: true, status: 200, json: async () => ready(snapshot()) };
  };
  const input = d.get("install-path-relay");
  input.listeners.input[0]({ target: { value: "/fake/relay.deb" } });
  await d.get("pick-relay").click();
  assert.deepEqual(request, { url: "/api/install/packages", body: { layer: "relay", path: "/fake/relay.deb" } });
  assert.equal(d.get("install-continue").disabled, false);
  await d.button("Back").click();
  assert.equal(d.get("choice-relay").attributes["aria-pressed"], "true");
  await d.get("install-continue").click();
  await d.get("install-continue").click();
  assert.ok(d.root.textContent.includes(t("Not needed: these layers do not erase the device.")));
  await d.get("install-continue").click();
  await d.get("install-start").click();
  const warning = d.get("install-confirm-warning");
  assert.equal(warning.textContent, t(WARNING_TEXT.confirmation));
  assert.ok(warning.className.includes("warning"));
  assert.ok(d.root.textContent.includes("fake-device"));
  const run = ready(snapshot());
  run.installedStale = true;
  run.job = { status: "running", started: "2026-10-21T00:00:00Z", steps: [{ layer: "relay", action: "install", status: "running" }], log: "" };
  page.receive(run);
  assert.equal(d.get("install-running-warning").hidden, false);
  assert.equal(d.get("install-running-warning").textContent, t(WARNING_TEXT.running));
  for (const i of [0, 1, 2]) assert.equal(d.get("nav-" + i).disabled, true);
  let prevented = false;
  d.listeners.beforeunload({ preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  const failed = { ...run, job: { ...run.job, status: "failed", finished: "2026-10-21T00:00:01Z", left: "partial", failed: 0,
    problem: { code: "device" }, observed: false, installed: run.installed } };
  page.receive(failed);
  assert.equal(d.get("install-running-warning").hidden, true);
  assert.equal(d.get("install-confirm-warning"), undefined);
  assert.ok(d.root.textContent.includes(t("Installed layers may have changed; these versions are the last observation.")));
  assert.ok(d.button("Read again"));
  prevented = false; d.listeners.beforeunload({ preventDefault() { prevented = true; } });
  assert.equal(prevented, false);
  let reads = 0;
  globalThis.fetch = async () => { reads++; return { ok: true, status: 200, json: async () => ({ ...failed, installedStale: false }) }; };
  await d.get("install-back-layers").click();
  assert.equal(reads, 1);
  assert.ok(d.get("choice-relay"));
  assert.ok(!d.root.textContent.includes(t("Installed layers may have changed; these versions are the last observation.")));
  await d.get("nav-0").click();
  assert.equal(d.get("install-connect").hidden, false);
  page.receive({ ...run, installedStale: true, job: { ...run.job, started: "2026-10-21T00:01:00Z", status: "succeeded",
    finished: "2026-10-21T00:01:01Z", observed: true, installed: run.installed } });
  assert.ok(d.button("Read again"), "a completed result also flags a later stale observation");
  assert.ok(d.root.textContent.includes(t("Installed layers may have changed; these versions are the last observation.")));
});

test("a missing base offers only the localized guide", async () => {
  const d = dom(); setLang("zh-CN");
  const savedInterval = globalThis.setInterval; globalThis.setInterval = () => 0;
  const page = await import("../assets/install.js?base-missing"); page.init(boot); globalThis.setInterval = savedInterval;
  const s = snapshot();
  delete s.installed.layers.base;
  for (const c of s.choices) c.problem = { code: "base-missing" };
  page.receive(s);
  assert.equal(d.get("choice-full"), undefined);
  assert.equal(d.get("install-continue"), undefined);
  const guide = d.nodes().find((n) => n.tag === "a");
  assert.equal(guide.attributes.href, boot.links["zh-CN"].manual);
  assert.equal(guide.textContent, t("Manual base installation guide"));
});

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
    this.className = ""; this.id = ""; this.dataset = {}; this.hidden = false; this.disabled = false; this.open = false; this.text = text;
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
    needs: ids.map((layer) => ({ layer, offline:true, artifacts: (layer === "relay" ? ["deb", "manifest"] : ["bundle"]).map((role)=>({role, name:role, verified:false, urls:["https://github.com/OpenXPlay/OpenXPlay-Relay/releases"]})), status: "missing", fileTypes: layer === "device" ? [".tar.gz"] : [".deb", ".manifest.json"], ready: false })) });
  return { target: { profile: "ufi003", device: { usb: { serial: "fake-device" } } },
    installed: { layers: { base: "kernel", device: "1.0", relay: "1.0" } }, installedStale: false,
    layers, packages: [], choices: [choice("full", ["device", "relay"]), choice("device", ["device"]), choice("relay", ["relay"])], suggested: "relay" };
}
function ready(s) {
  for (const c of s.choices) {
    c.ready = true;
    for (const n of c.needs) { n.artifacts?.forEach((a)=>{a.verified=true;}); Object.assign(n, { ready: true, status: "checked", comparison: "upgrade",
      package: { id: n.layer, name: n.layer, version: "2.0", size: 1, sha256: "a".repeat(64), checked: true } }); }
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
  await d.get("install-continue").click();
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
  const input = d.get("artifact-path-relay-deb");
  input.listeners.input[0]({ target: { value: "/fake/relay.deb" } });
  await d.get("pick-relay-deb").click();
  assert.deepEqual(request, { url: "/api/artifacts/import", body: { choice: "relay", layer: "relay", role: "deb", multiple:false, path: "/fake/relay.deb" } });
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
  const baseOnly = { layers: { base: "kernel" } };
  page.receive({ ...run, installed: baseOnly, job: { ...run.job, started: "2026-10-21T00:02:00Z", status: "succeeded",
    finished: "2026-10-21T00:02:01Z", observed: true, installed: baseOnly } });
  assert.ok(d.root.textContent.includes(t("Next steps")));
  assert.ok(d.root.textContent.includes(t("Install the remaining layers to start the relay.")));
  assert.ok(d.nodes().some((n) => n.tag === "a" && n.attributes.href.includes("#step-network")));
  assert.equal(d.get("identity-pick"), undefined);
});

test("a missing base offers only the localized guide", async () => {
  const d = dom(); setLang("zh-CN");
  const savedInterval = globalThis.setInterval; globalThis.setInterval = () => 0;
  const page = await import("../assets/install.js?base-missing"); page.init(boot); globalThis.setInterval = savedInterval;
  const s = snapshot();
  delete s.installed.layers.base;
  for (const c of s.choices) c.problem = { code: "base-missing" };
  page.receive(s);
  await d.get("install-continue").click();
  assert.equal(d.get("choice-full"), undefined);
  assert.equal(d.get("install-continue"), undefined);
  const guide = d.nodes().find((n) => n.tag === "a");
  assert.equal(guide.attributes.href, boot.links["zh-CN"].manual);
  assert.equal(guide.textContent, t("Manual base installation guide"));
});

test("base choices render their localized names, reasons and named steps", async () => {
  const d = dom(); setLang("zh-CN");
  const savedInterval = globalThis.setInterval; globalThis.setInterval = () => 0;
  const page = await import("../assets/install.js?base-choices"); page.init(boot); globalThis.setInterval = savedInterval;
  const s = snapshot();
  s.choices.push({ id: "stock", name: { en: "Install from stock", "zh-CN": "从原厂固件安装" },
    reason: { en: "Needs original firmware", "zh-CN": "需要原厂固件" }, problem: { code: "wrong-mode" },
    layers: ["base", "device", "relay"], destructive: true, needs: [], steps: [] });
  s.choices.push({ id: "base", name: { en: "Reinstall base", "zh-CN": "重新安装基础系统" },
    layers: ["base"], destructive: true, needs: [{ layer: "base" }], steps: [{ layer: "base", action: "flash", name: { en: "Write rootfs", "zh-CN": "写入根文件系统" } }] });
  page.receive(s);
  await d.get("install-continue").click();
  assert.equal(d.get("choice-stock"), undefined);
  const reflash = d.nodes().find(n=>n.className === "reflash-choices");
  assert.equal(reflash.open, false);
  await d.get("choice-base").click();
  assert.ok(d.root.textContent.includes("写入根文件系统"));
  assert.ok(!d.root.textContent.includes("undefined"));
});

for (const language of ["en", "zh-CN"]) test("Prepare links, multiple paths, unmatched files and offline states in " + language, async () => {
  const d = dom(); setLang(language);
  const savedInterval = globalThis.setInterval; globalThis.setInterval = () => 0;
  const page = await import("../assets/install.js?files-"+language); page.init(boot); globalThis.setInterval = savedInterval;
  let s = snapshot();
  const sha = "b609b91c94b07d7c" + "a".repeat(48);
  s.catalog = {status:"fresh", channel:"beta", index:{sequence:17, generated:"2026-10-21T08:00:00Z"}};
  const base = {layer:"base",ready:false,package:{checked:false,version:"openstick-v1"},artifacts:[{role:"boot",name:"boot.img",size:1024,sha256:sha,verified:false,urls:["https://github.com/OpenStick/OpenStick/releases/tag/v1"]}]};
  for (const c of s.choices) for (const n of c.needs) {
    n.offline=false;
    for (const a of n.artifacts) Object.assign(a,{size:2048,sha256:sha,urls:["https://github.com/OpenXPlay/OpenXPlay-Relay/releases/download/v1/"+a.name,"https://github.com/OpenXPlay/OpenXPlay-Relay/releases"]});
  }
  s.choices.push({id:"full-reinstall",name:{en:"Full reinstall","zh-CN":"完整重装"},destructive:true,layers:["base","device","relay"],needs:[base,...s.choices[0].needs],steps:[]});
  const calls=[];
  globalThis.fetch=async(url,options)=>{ calls.push({url,body:options.body ? JSON.parse(options.body) : undefined}); return {ok:true,status:200,json:async()=>s}; };
  page.receive(s); await d.get("nav-1").click(); await d.get("choice-full-reinstall").click(); await d.get("install-continue").click();
  assert.deepEqual(calls.map(c=>c.url),["/api/install/prepare","/api/install"],"Prepare has no automatic download request");
  assert.equal(d.get("install-continue").disabled,true);
  const links=d.nodes().filter(n=>n.tag === "a");
  assert.ok(links.some(n=>n.attributes.href.includes("OpenStick")));
  assert.ok(d.root.textContent.includes(sha.slice(0,12)));
  assert.ok(d.root.textContent.includes(t("Download each file from its link or any other source you trust. The app checks its SHA-256, so the file name does not matter.")));
  let copied="";
  Object.defineProperty(globalThis,"navigator",{configurable:true,value:{clipboard:{writeText:async(value)=>{copied=value;}}}});
  await d.nodes().find(n=>n.tag === "button" && n.attributes["aria-label"] === t("Show and copy full SHA-256")).click();
  assert.equal(copied,sha); assert.ok(d.root.textContent.includes(sha));
  d.get("multiple-paths").listeners.input[0]({target:{value:"/tmp/renamed one\n/tmp/another name"}});
  s=ready(s); for(const c of s.choices)for(const n of c.needs)for(const a of n.artifacts||[])a.path="/tmp/renamed one";
  s.fileResults=[{problem:{code:"unmatched-file",detail:"other.zip · 42 B · SHA-256 abcdef012345"}}];
  await d.get("pick-several").click();
  assert.deepEqual(calls.find(c=>c.url === "/api/artifacts/import").body,{choice:"full-reinstall",layer:"",role:"",multiple:true,paths:["/tmp/renamed one","/tmp/another name"]});
  assert.equal(d.get("install-continue").disabled,false);
  assert.ok(d.root.textContent.includes(problemTextForTest("unmatched-file","other.zip · 42 B · SHA-256 abcdef012345")));
  s.catalog.status="cached/offline";page.receive(s);
  assert.ok(d.root.textContent.includes(t("Package list: saved copy (offline), from {date}",{date:""}).split(language === "en" ? ", from " : "，日期：")[0]));
  assert.ok(d.root.textContent.includes("2026"));
  s.catalog={status:"failed"};for(const c of s.choices)for(const n of c.needs)n.offline=true;
  page.receive(s);
  assert.ok(d.root.textContent.includes(t("Package list unavailable. Base files use the profile pins; other packages use their manifests.")));
  assert.ok(d.root.textContent.includes(t("Not checked against the package list (offline)")));
  assert.ok(d.root.textContent.includes("boot.img"));assert.ok(d.root.textContent.includes(sha.slice(0,12)));
  s.fileResults=[{problem:{code:"catalog-unknown",detail:"0.1.5~beta.1"}},{problem:{code:"file-changed",detail:"renamed file"}}];
  s.fileCheck={status:"checking",name:"big file",done:1024,total:2048};page.receive(s);
  assert.equal(d.get("install-continue").disabled,true);
  assert.ok(d.root.textContent.includes("0.1.5~beta.1"));assert.ok(d.root.textContent.includes("renamed file"));
  await d.get("cancel-file-check").click();assert.equal(calls.at(-1).url,"/api/artifacts/cancel");
  s={...s,nextSteps:{serial:"fake-device",identity:"present"},job:{status:"succeeded",started:"2026-10-21T01:00:00Z",finished:"2026-10-21T01:01:00Z",steps:[],installed:s.installed,observed:true,log:""}};page.receive(s);
  const success=d.nodes().find(n=>n.className === "identity-success result ok");
  assert.ok(success);assert.ok(success.children.some(n=>n.tag === "strong" && n.textContent === t("Identity added")));
  assert.ok(success.textContent.includes("identity.pk8 · certificate.p7b"));assert.ok(!d.root.textContent.includes("debian-11"));
});
function problemTextForTest(code,detail) {
  return t("This file is not one of the files listed here: {detail}.",{detail});
}

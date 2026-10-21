// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang, t } from "../assets/i18n.js";
import { WARNING_TEXT } from "../assets/installlogic.js";

// A small DOM fake exercises the actual view without a browser dependency or a device service.
import { NodeFake, walk } from "./dom-fixture.mjs";

function dom() {
  globalThis.Node = NodeFake;
  const body = new NodeFake("body");
  const add = (parent, tag, id) => { const node = new NodeFake(tag); node.id = id; parent.append(node); return node; };
  add(body, "div", "install-running-warning").hidden = true;
  add(body, "div", "signature-off-notice").hidden = true;
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

for (const language of ["en", "zh-CN"]) test("signature notice persists through steps and Prepare explains trust in " + language, async () => {
  const d = dom(); setLang(language);
  const savedInterval = globalThis.setInterval; globalThis.setInterval = () => 0;
  const page = await import("../assets/install.js?signature-" + language);
  page.init(boot); globalThis.setInterval = savedInterval;
  const s = snapshot();
  s.catalog = { status: "fresh", signatureCheck: false, verified: false, reason: "no signature", index: { sequence: 2 } };
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => s });
  page.receive(s);
  const notice = d.get("signature-off-notice");
  assert.equal(notice.hidden, false);
  assert.ok(notice.className !== "error-box");
  assert.equal(notice.textContent, t("Signature check is off: the package list is not verified"));
  await d.get("nav-1").click();
  assert.equal(notice.hidden, false);
  await d.get("install-continue").click();
  for (const [reason, label] of [["no signature", "No signature"], ["bad signature", "Bad signature"], ["signature off", "Signature check is off"]]) {
    s.catalog.reason = reason; page.receive(s);
    assert.ok(d.root.textContent.includes(t("Package list: not verified ({reason})", { reason: t(label) })));
    const line = d.nodes().find((n) => n.attributes.role === "status" && n.textContent.includes(t(label)));
    assert.ok(line.className.includes("warning"));
  }
  s.catalog.status = "cached/offline"; page.receive(s);
  assert.ok(d.root.textContent.includes(t("Saved copy (offline)")));
  s.catalog.verified = true; s.catalog.status = "fresh"; page.receive(s);
  assert.equal(notice.hidden, false, "off mode stays visible even when this signature is valid");
  assert.ok(!d.root.textContent.includes(t("Package list: not verified ({reason})", { reason: t("Signature check is off") })));
  s.catalog.signatureCheck = true; page.receive(s);
  assert.equal(notice.hidden, true);
});

for (const language of ["en", "zh-CN"]) test("N1 read-only Armbian, device/relay choices and direct admin link in " + language, async () => {
  const d=dom();setLang(language);
  const saved=globalThis.setInterval;globalThis.setInterval=()=>0;
  const page=await import("../assets/install.js?n1-"+language);page.init({...boot,links:{[language]:{phicommN1:"https://openxplay.org/phicomm-n1"}}});globalThis.setInterval=saved;
  const s=snapshot();s.target={profile:"phicomm-n1",device:{mode:"ssh",connection:"network",host:"n1.test",serialno:"021122334455"}};
  s.layers[0]={...s.layers[0],readOnly:true,guide:"phicommN1",name:{en:"Armbian","zh-CN":"Armbian"},prettyName:"Debian GNU/Linux 13 (trixie)",kernelRelease:"6.12.112-ophub"};
  page.reset(s);await d.get("install-continue").click();
  assert.ok(d.root.textContent.includes("Armbian"));assert.ok(d.root.textContent.includes(t("Read only")));assert.ok(d.root.textContent.includes("6.12.112-ophub"));
  assert.ok(d.get("choice-device"));assert.ok(d.get("choice-relay"));assert.equal(d.get("choice-base"),undefined);
  const job={status:"succeeded",started:"2026-10-21T10:00:00Z",finished:"2026-10-21T10:01:00Z",steps:[],observed:true,installed:s.installed,left:"installed"};
  const nextSteps={serial:"021122334455",identity:"absent",adminUrl:"https://n1.test:6975/",forward:{active:false}};page.receive({...s,job,nextSteps});
  assert.ok(d.root.textContent.includes(t("Add your CarPlay identity")));
  assert.equal(d.root.textContent.includes("OpenStick"),false);
  assert.equal(d.get("admin-open").tag,"a");assert.equal(d.get("admin-open").href,"https://n1.test:6975/");
});

for (const language of ["en", "zh-CN"]) test("hardware review summary, confirmed identity, refusal reset, transfer and phases in " + language, async () => {
  const d=dom(); setLang(language);
  const savedInterval=globalThis.setInterval; globalThis.setInterval=()=>0;
  const page=await import("../assets/install.js?hardware-"+language); page.init(boot); globalThis.setInterval=savedInterval;
  let s=ready(snapshot());
  s.target.name={en:"UFI003 4G USB dongle","zh-CN":"UFI003 随身 Wi-Fi"};
  Object.assign(s.target.device,{mode:"adb",serialno:"43fb82bd",usb:{serial:"transport-serial",path:[1,1,1]}});
  s.identity={serial:"43fb82bd"};
  const needs=[{layer:"base",package:{checked:true,version:"openstick-v1"},ready:true,artifacts:[]},...s.choices[0].needs];
  s.choices.push({id:"full-reinstall",name:{en:"Full reinstall","zh-CN":"完整重装"},destructive:true,serial:"43fb82bd",layers:["base","device","relay"],needs,steps:[]});
  globalThis.fetch=async(url)=>{
    if(url==="/api/install/prepare")s={...s,fileResults:[]};
    return {ok:true,status:200,json:async()=>url.startsWith("/api/backups/")?{sets:[]}:s};
  };
  page.receive(s); await d.get("nav-1").click(); await d.get("choice-full-reinstall").click();
  await d.get("install-continue").click(); await d.get("install-continue").click(); await d.get("install-continue").click();
  const summary=d.nodes().find((n)=>n.className==="flash-summary"); assert.ok(summary);
  for(const text of ["Erases the device","Base system: about 6 minutes. The device restarts.","Device support: about 1 minute plus a device restart.","Relay: under a minute. The relay restarts.","Keep the device connected and powered until installation finishes."])assert.ok(summary.textContent.includes(t(text)),text);
  assert.ok(summary.textContent.includes(t("{layer}: {version}",{layer:t("Device support"),version:"2.0"})));
  assert.ok(summary.textContent.includes(t("{layer}: {version}",{layer:t("OpenXPlay relay"),version:"2.0"})));
  assert.equal(d.get("install-confirm"),undefined,"summary appears before confirmation");
  await d.get("install-start").click();
  const target=d.nodes().find((n)=>n.className==="confirmation-target");
  assert.ok(target.textContent.includes(s.target.name[language]+" · "+t("Serial {serial}",{serial:"43fb82bd"})));
  assert.ok(!target.textContent.includes("1.1.1")); assert.ok(!target.textContent.includes("transport-serial"));
  assert.ok(d.root.textContent.includes(t("Type the serial number shown on the device card to continue")));
  await d.get("install-cancel").click();
  await d.get("nav-1").click(); await d.get("choice-relay").click(); await d.get("install-continue").click();
  const detail="refused package.deb · 2048 B · SHA-256 abcdef012345 · 0.1.5~beta.1";
  s={...s,fileResults:[{name:"refused package.deb",size:2048,sha256:"abcdef012345",problem:{code:"catalog-unknown",detail}}]}; page.receive(s);
  assert.ok(d.root.textContent.includes(t("This package is not in the package list. Refused file and listed versions: {detail}.",{detail})));
  await d.button("Back").click(); await d.get("choice-device").click(); await d.get("install-continue").click();
  assert.ok(!d.root.textContent.includes("refused package.deb"),"another choice has no earlier refusal");
  await d.get("install-continue").click(); await d.get("install-continue").click();
  assert.ok(d.root.textContent.includes(t("Keeps the device's system and data.")));
  const job={status:"running",started:"2026-10-21T00:00:00Z",log:"",steps:[{id:"rootfs",layer:"base",action:"flash",status:"running"}],transfer:{phase:"sending",piece:4,pieces:5,done:50*1024*1024,total:200*1024*1024}};
  s={...s,job};page.receive(s);
  assert.ok(d.root.textContent.includes("50.0 MiB / 200.0 MiB"));
  let progress=d.nodes().find((n)=>n.tag==="progress");assert.equal(progress.value,3.25);assert.equal(progress.max,5);
  s={...s,job:{...job,transfer:{...job.transfer,phase:"writing",done:0}}};page.receive(s);
  assert.ok(d.root.textContent.includes(t("Writing {piece}/{pieces}",{piece:4,pieces:5})));
  assert.ok(!d.root.textContent.includes("0 B /"));progress=d.nodes().find((n)=>n.tag==="progress");assert.equal(progress.value,4);
  const steps=[{layer:"device",action:"install"},{layer:"relay",action:"install"},{layer:"device",action:"verify"},{layer:"device",action:"installed"}].map((v)=>({...v,status:"ok"}));
  s={...s,job:{...job,steps,status:"succeeded",finished:"2026-10-21T00:05:09Z",observed:true,installed:s.installed}};page.receive(s);
  const phases=d.nodes().filter((n)=>n.tag==="details"&&n.className.startsWith("job-phase")).map((n)=>n.children[0].textContent);
  assert.deepEqual(phases,[t("Device support"),t("OpenXPlay relay"),t("Check device support")]);
});

for (const language of ["en", "zh-CN"]) test("N1 confirmation and running warnings name the network in " + language, async () => {
  const d=dom();setLang(language);const savedInterval=globalThis.setInterval;globalThis.setInterval=()=>0;
  const page=await import("../assets/install.js?n1-review-"+language);page.init(boot);globalThis.setInterval=savedInterval;
  const s=ready(snapshot());s.target={profile:"phicomm-n1",name:{en:"Phicomm N1","zh-CN":"斐讯 N1"},device:{mode:"ssh",connection:"network",host:"n1.test",serialno:"021122334455"}};
  globalThis.fetch=async()=>({ok:true,status:200,json:async()=>s});
  page.receive(s);await d.get("nav-1").click();await d.get("install-continue").click();await d.get("install-continue").click();await d.get("install-continue").click();
  assert.ok(d.root.textContent.includes(t("Keep the box powered and connected to the network until installation finishes.")));
  await d.get("install-start").click();assert.ok(d.root.textContent.includes(t("Phicomm N1")+" · n1.test"));
  assert.ok(d.root.textContent.includes(t("Board serial (eth0 MAC): {serial}",{serial:"021122334455"})));
  assert.equal(d.get("install-confirm-warning").textContent,t("Keep the box powered and connected to the network until installation finishes."));
  page.receive({...s,job:{status:"running",started:"2026-10-21T00:00:00Z",steps:[{layer:"relay",action:"install",status:"running",resumable:true}],log:""}});
  assert.equal(d.get("install-running-warning").textContent,t("Keep the box powered and connected to the network until installation finishes."));
  assert.ok(!d.root.textContent.includes(t("Do not unplug the device or cut its power")));
});

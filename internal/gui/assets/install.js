// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { t, lang } from "./i18n.js";
import { api, el } from "./ui.js";
import { linksFor } from "./settingslogic.js";
import { deviceSummary } from "./devices.js";
import { ACTION_TEXT, LAYER_TEXT, STATUS_TEXT, LEFT_TEXT, COMPARISON_TEXT, WARNING_TEXT,
  problemText, canChoose, canContinue, canStart, canNavigate, canSkipBackup, selectedChoice, neededRows,
  isRunning, confirmationWarning, unplugText, guardBeforeUnload, displayVersion, elapsed, phase, mergeState,
  suggestedAction, serialFor, PHASE_TEXT, phaseStyle, phaseGroups, nextStepsState, sizeText, backupRestoration, choiceGroups, CHOICE_TEXT, parsePaths } from "./installlogic.js";

const view = { boot: {}, state: null, paths: {}, choice: "", step: 0, initialized: false,
  downgrade: false, busy: false, preparing: false, confirm: false, serial: "", error: null, rowErrors: {}, logOpen: false,
  backups: null, backupError: null, backupPath: "", artifactPaths: {}, multiplePaths: "", fullHashes: new Set(), reflashOpen: false,
  identityPaths: [], identityPath: "", identityMessage: "", nextError: null };
let root, onRunning, refreshing = false;

export function init(boot, runningChanged) {
  view.boot = boot;
  root = document.getElementById("install");
  onRunning = runningChanged;
  window.addEventListener("beforeunload", (event) => guardBeforeUnload(event, view.state));
  document.querySelectorAll("[data-install-step]").forEach((button) => button.addEventListener("click", () => {
    const step = Number(button.dataset.installStep);
    if (canNavigate(view.state, view.choice, step, view.busy)) return go(step);
  }));
  setInterval(() => {
    const label = document.getElementById("install-elapsed");
    if (label && view.state?.job) label.textContent = t("Elapsed: {time}", { time: elapsed(view.state.job) });
  }, 1000);
}

export function running() { return isRunning(view.state); }
export function snapshot() { return view.state; }
export function reset(state) { view.state=null;view.step=0;view.choice="";view.initialized=false;receive(state); }
export function selectRepair() { choose("repair"); return go(1); }

export function receive(state) {
  if (!state) return;
  state = mergeState(view.state, state);
  const previous = view.state?.job;
  const changedDevice = serialFor(view.state) !== serialFor(state);
  view.state = state;
  if (changedDevice) {
    view.choice = ""; view.serial = ""; view.backups = null; view.identityPaths = []; view.identityPath = "";
    view.identityMessage = ""; view.nextError = null; view.confirm = false;
    view.error = null; view.rowErrors = {};
  }
  if (!view.choice) view.choice = suggestedAction(state);
  const job = state.job;
  if (running()) { view.step = 4; view.confirm = false; }
  else if (job && (job.started !== previous?.started || previous?.status === "running")) view.step = 5;
  if (onRunning) onRunning(running());
  render();
  if (job && (job.started !== previous?.started || job.status !== previous?.status)) {
    focus(job.status === "running" ? "install-job-title" : "install-result");
  }
}

function focus(id) {
  const target = document.getElementById(id);
  if (target) { target.focus(); target.scrollIntoView({ block: "nearest" }); }
}

async function go(step) {
  view.step = step;
  view.initialized = true;
  view.confirm = false;
  view.error = null;
  if (step === 2) clearMessages();
  render();
  focus("install-step-title");
  if (step === 2) {
    // A finished job may have observed only its own packages' requirements. Recheck all
    // provider rows on entering Prepare so earlier session packages are reused correctly.
    await prepare();
  }
  if (step === 3 && !canSkipBackup(selectedChoice(view.state, view.choice))) await readBackups();
}

async function prepare(action = () => api("install/prepare", { choice: view.choice })) {
  if (view.preparing) return;
  view.preparing = true; view.error = null; render();
  try { receive(await action()); }
  catch (error) { view.error = error; }
  await refresh();
  view.preparing = false; render();
}

async function readBackups() {
  view.busy = true; view.backupError = null; render();
  try { view.backups = await api("backups/" + encodeURIComponent(serialFor(view.state, view.choice))); }
  catch (error) { view.backupError = error; }
  view.busy = false; render();
}

export async function refresh() {
  if (refreshing || running()) return;
  refreshing = true;
  try { receive(await api("install")); }
  catch (err) { view.error = err; render(); }
  refreshing = false;
}

async function backToLayers() {
  view.busy = true;
  view.confirm = false;
  view.error = null;
  render();
  await refresh();
  view.busy = false;
  go(1);
}

async function changePackage(layer, action) {
  view.busy = true;
  view.confirm = false;
  clearMessages();
  render();
  try { receive(await action()); await refresh(); }
  catch (err) { view.rowErrors[layer] = err; }
  view.busy = false;
  render();
}


async function start() {
  if (!canStart(view.state, view.choice, view.downgrade, view.busy, view.serial)) return;
  view.busy = true;
  view.confirm = false;
  view.error = null;
  render();
  try {
    receive(await api("install/start", { choice: view.choice, downgrade: view.downgrade, yesErase: view.serial }));
    // A very short job can finish before the start response reaches the page.
    receive(await api("install"));
  } catch (err) { view.error = err; }
  view.busy = false;
  render();
}

async function saveLog() {
  try {
    if (view.boot.web) {
      const text = await (await fetch("/api/install/log")).text();
      const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
      el("a", { href: url, download: "openxplay-install.log" }).click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } else { await api("install/log/save", {}); }
  } catch (err) { view.error = err; render(); }
}

function name(id) {
  const row = view.state?.layers?.find((r) => r.layer === id);
  return row?.name?.[lang()] || row?.name?.en || t(LAYER_TEXT[id]);
}

function baseKernel() { return view.state?.layers?.find((r) => r.layer === "base")?.kernelRelease || view.state?.installed?.kernelRelease || ""; }

function freshness(stale) {
  return stale ? el("div", { class: "warning stale", role: "status" }, [
    el("p", {}, t("Installed layers may have changed; these versions are the last observation.")),
    el("button", { type: "button", disabled: running() || view.busy, onclick: backToLayers }, t("Read again")),
  ]) : "";
}

function installed(facts, stale) {
  if (!facts) return el("p", { class: "note" }, t("Installed layers are not available."));
  const ids = view.state?.layers?.map((r) => r.layer) || Object.keys(facts.layers);
  return el("div", { class: "layer-summary" }, [freshness(stale),
    el("dl", { class: "installed" }, ids.flatMap((id) => [
      el("dt", {}, id === "base" ? t("Base system") : name(id)), el("dd", {}, [facts.layers[id] ? (id === "base" && view.state?.layers?.find((r)=>r.layer===id)?.readOnly ? name(id)+(facts.prettyName?" · "+facts.prettyName:"") : displayVersion(id, facts.layers[id])) : t("Not installed"),
        id === "base" && facts.layers[id] ? el("small", { class: "kernel-version" }, t("Kernel: {version}", { version: facts.kernelRelease || facts.layers.base })) : ""]),
    ])),
  ]);
}

function title(text) { return el("h2", { id: "install-step-title", tabindex: "-1" }, text); }
function back(step) { return el("button", { type: "button", disabled: view.busy, onclick: () => go(step) }, t("Back")); }
function next(step, enabled = true) {
  return el("button", { id: "install-continue", class: "primary", type: "button", disabled: view.busy || !enabled, onclick: () => go(step) }, t("Continue"));
}

function connectView() {
  return [title(t("Installed layers")), installed(view.state?.installed, view.state?.installedStale),
    nextStepsState(view.state).available ? nextStepsCard() : "",
    el("div", { class: "actions" }, [el("button", { type: "button", disabled: view.busy, onclick: refresh }, t("Read again")),
      next(1, canNavigate(view.state, view.choice, 1))])];
}

function guide() {
  if (view.state?.target?.profile === "phicomm-n1") return el("div", {class:"base-guide"}, [
    el("p",{},t("Install Armbian yourself before connecting. The Flasher installs device support and the relay.")),
    el("a",{href:linksFor(view.boot.links,lang()).phicommN1,"data-open":""},t("Phicomm N1 setup guide"))]);
  return el("div", { class: "base-guide" }, [
    el("p", { class: "note" }, t("Flashing the base needs fastboot; this version cannot do it yet.")),
    el("a", { href: linksFor(view.boot.links, lang()).manual, "data-open": "" }, t("Manual base installation guide")),
  ]);
}

function choose(id) {
  if (!canChoose(view.state, id, view.busy)) return;
  if (view.choice !== id) { view.downgrade = false; clearMessages(); }
  view.choice = id;
  view.serial = "";
  view.confirm = false;
  render();
}

function clearMessages() {
  view.error = null; view.rowErrors = {};
  if (view.state) view.state = { ...view.state, fileResults: [], fileCheck: null };
}

function choiceButton(id, text, enabled) {
  return el("button", { id: "choice-" + id, type: "button", class: view.choice === id ? "primary" : "",
    "aria-pressed": String(view.choice === id), disabled: view.busy || !enabled, onclick: () => choose(id) }, text);
}

function suggested(id) { return suggestedAction(view.state) === id ? el("span", { class: "tag suggested" }, t("Suggested")) : ""; }

function stepLabel(s) { return s.name?.[lang()] || s.name?.en || t(ACTION_TEXT[s.action] || s.action); }

function stepList(choice) {
  return el("details", { class: "choice-steps" }, [el("summary", {}, t("Show steps")),
    el("ol", {}, choice.steps.map((s) => el("li", {}, t("{layer}: {step}", { layer: name(s.layer), step: stepLabel(s) }))))]);
}

function choiceLabel(c) {
  if (c.id === "full") return t("Install device support and relay");
  if (c.name) return c.name[lang()] || c.name.en;
  return c.id === "device" ? t("Install / update device support") : t("Install / update relay");
}

function choiceCard(c, highlight = false) {
  const layer = view.state?.layers?.find((r) => r.layer === c.id);
  return el("div", { class: "full-choice" + (highlight ? " suggested-choice" : "") }, [
    el("div", { class: "actions" }, [choiceButton(c.id, choiceLabel(c), canChoose(view.state, c.id)), suggested(c.id),
      c.destructive ? el("span", { class: "tag destructive-tag" }, t("Erases the device")) : ""]),
    el("p", {}, t(CHOICE_TEXT[c.id])),
    c.layers.includes("base") && (!layer || layer.layer !== "base") && baseKernel() ? el("small", { class: "kernel-version" }, name("base") + " · " + t("Kernel: {version}", { version: baseKernel() })) : "",
    layer ? el("p", { class: "layer-version" }, [name(layer.layer) + (layer.layer === "base" && layer.installed ? "" : " · " + (layer.installed ? displayVersion(layer.layer, layer.installed) : t("Not installed"))),
      layer.layer === "base" && baseKernel() ? el("small", { class: "kernel-version" }, t("Kernel: {version}", { version: baseKernel() })) : ""]) : "",
    c.reason ? el("p", { class: "note" }, c.reason[lang()] || c.reason.en) : c.problem ? el("p", { class: "note" }, problemText(t, c.problem)) : "",
    c.problem?.code === "fastboot-unsupported" ? el("a", { href: linksFor(view.boot.links, lang()).manual, "data-open": "" }, t("Manual base installation guide")) : "",
  ]);
}

function chooseView() {
  const state = view.state;
  const parts = [title(t("Choose layers")), freshness(state?.installedStale)];
  const base = state?.layers?.find((row)=>row.readOnly);
  if(base)parts.push(el("article",{class:"read-only-base"},[el("h3",{},base.name?.[lang()]||base.name?.en),
    el("span",{class:"tag"},t("Read only")),el("p",{},base.prettyName||state.installed?.prettyName||"—"),
    el("p",{},t("Kernel: {version}",{version:base.kernelRelease||state.installed?.kernelRelease||"—"})),guide()]));
  const full = selectedChoice(state, "full");
  if (full?.problem?.code === "base-missing" && !state.choices.some((c) => c.destructive)) {
    return [...parts, el("p", { class: "warning", role: "status" }, problemText(t, full.problem)), guide()];
  }
  const groups = choiceGroups(state);
  if (groups.suggested) parts.push(choiceCard(groups.suggested, true));
  if (groups.updates.length) parts.push(el("section", { class: "update-layers" }, [el("h3", {}, t("Update layers")), ...groups.updates.map((c) => choiceCard(c))]));
  if (groups.reflash.length) {
    const details = el("details", { class: "reflash-choices" }, [el("summary", {}, t("Reflash (erases the device)")),
      ...groups.reflash.map((c) => choiceCard(c))]);
    details.open = view.reflashOpen;
    details.addEventListener("toggle", () => { view.reflashOpen = details.open; });
    parts.push(details);
  }
  const selected = selectedChoice(state, view.choice);
  if (selected && !selected.problem) parts.push(stepList(selected));
  parts.push(el("div", { class: "actions" }, [back(0), next(2, canChoose(state, view.choice))]));
  return parts;
}

function packageRow(need) {
  const layer = need.layer, pkg = need.package;
  const parts = [el("h3", {}, name(layer)),
    layer === "base" && baseKernel() ? el("small", { class: "kernel-version" }, t("Kernel: {version}", { version: baseKernel() })) : "",
    need.offline && layer !== "base" ? el("p", { class: "offline-package warning" }, t("Not checked against the package list (offline)")) : "",
    el("ul", { class: "artifact-rows" }, (need.artifacts || []).map((a) => artifactRow(layer, a)))];
  if (pkg && layer !== "base") parts.push(el("details", { class: "package-details" }, [el("summary", {}, t("Package details")),
    el("strong", {}, pkg.name), el("p", {}, t("{layer} · {version} · {size} bytes", { layer: name(layer), version: displayVersion(layer, pkg.version), size: pkg.size })),
    pkg.checked ? el("p", { class: "check ok" }, t("Checks passed")) : "",
    need.comparison ? el("p", { class: need.comparison === "downgrade" ? "check bad" : "check" }, t(COMPARISON_TEXT[need.comparison])) : "",
  ]));
  if (need.problem && !(need.problem.code === "downgrade" && view.downgrade)) parts.push(el("p", { class: "error-box" }, problemText(t, need.problem)));
  return el("li", { class: "package-row", "data-package-layer": layer }, parts);
}

function hashView(artifact) {
  if (!artifact.sha256) return el("p", { class: "note" }, t("Size and SHA-256 will be read from your file."));
  const full = view.fullHashes.has(artifact.sha256);
  return el("div", { class: "artifact-hash" }, [el("button", { type: "button", class: "hash-button", title: t("Show and copy full SHA-256"),
    "aria-label": t("Show and copy full SHA-256"), onclick: () => {
      view.fullHashes.add(artifact.sha256); navigator.clipboard?.writeText(artifact.sha256).catch(() => {}); render();
    } }, "SHA-256 " + artifact.sha256.slice(0, 12) + "…"),
    full ? el("code", { class: "full-hash" }, artifact.sha256) : ""]);
}

async function chooseFiles(layer = "", role = "") {
  view.rowErrors = {};
  const key = layer + "-" + role;
  return changePackage(key, () => api(view.boot.web ? "artifacts/import" : "artifacts/pick", {
    choice: view.choice, layer, role, multiple: !layer,
    ...(view.boot.web ? layer ? { path: (view.artifactPaths[key] || "").trim() } : { paths: parsePaths(view.multiplePaths) } : {}),
  }));
}

function artifactRow(layer, artifact) {
  const key = layer + "-" + artifact.role;
  const disabled = view.busy || view.preparing || running();
  const parts = [el("div", { class: "artifact-heading" }, [el("strong", {}, artifact.name),
    artifact.size ? el("span", { title: String(artifact.size) + " B" }, sizeText(artifact.size)) : ""]), hashView(artifact),
    el("div", { class: "artifact-links" }, (artifact.urls || []).map((url, i) => el("a", { href: url, "data-open": "" },
      /github\.com\/[^/]+\/[^/]+\/releases(?:\/tag\/[^/]+)?$/.test(url) ? t("Release page") : t("Download link {n}", { n: i+1 })))),
    el("p", { class: artifact.verified ? "check ok" : "note" }, artifact.verified ? t("Verified ✓") : t("Waiting for file")),
    artifact.path ? el("p", { class: "selected-path" }, artifact.path) : "",
  ];
  if (view.boot.web) parts.push(el("label", { for: "artifact-path-" + key }, t("File path on this computer")), el("input", {
    id: "artifact-path-" + key, type: "text", value: view.artifactPaths[key] || "", disabled,
    spellcheck: "false", autocomplete: "off", oninput: (e) => { view.artifactPaths[key] = e.target.value; },
    onkeydown: (e) => { if (e.key === "Enter" && view.artifactPaths[key]?.trim()) chooseFiles(layer, artifact.role); },
  }));
  parts.push(el("button", { id: "pick-" + key, type: "button", disabled, onclick: () => chooseFiles(layer, artifact.role) }, t("Choose file…")));
  if (view.rowErrors[key]) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.rowErrors[key])));
  return el("li", { class: "artifact-row", "data-artifact": key }, parts);
}

async function refreshCatalog() {
  view.busy = true; view.error = null; render();
  try { await api("catalog/fetch", { channel: "beta" }); } catch (_) { /* Catalog state explains the offline fallback. */ }
  await refresh(); view.busy = false; render();
  if (view.step === 2) await prepare();
}
function catalogView() {
  const c = view.state?.catalog;
  const unverified = c?.verified === false && !!c.index;
  const reason = c?.reason === "no signature" ? t("No signature") : c?.reason === "bad signature" ? t("Bad signature") : t("Signature check is off");
  const text = unverified ? t("Package list: not verified ({reason})", { reason }) +
    (c.status === "cached/offline" ? " · " + t("Saved copy (offline)") : "") :
    c?.status === "fresh" ? t("Package list: fresh ({channel}, #{n})", { channel: c.channel, n: c.index?.sequence || 0 }) :
    c?.status === "cached/offline" ? t("Package list: saved copy (offline), from {date}", { date: dateText(c.index?.generated || c.updated) }) :
      c?.status === "fetching" ? t("Loading the package list…") : t("Package list unavailable. Base files use the profile pins; other packages use their manifests.");
  return el("div", { class: "catalog-status" }, [el("p", { class: c?.status === "fresh" && !unverified ? "check ok" : "warning", role: "status" }, text),
    el("button", { type: "button", disabled: view.busy || view.preparing || c?.status === "fetching", onclick: refreshCatalog }, t("Refresh"))]);
}

function prepareView() {
  const selected = selectedChoice(view.state, view.choice);
  const check = view.state?.fileCheck;
  const checking = check?.status === "checking";
  const parts = [title(t("Prepare files")), freshness(view.state?.installedStale), catalogView(),
    el("p", { class: "file-instructions" }, t("Download each file from its link or any other source you trust. The app checks its SHA-256, so the file name does not matter.")),
    el("p", { class: "note" }, t("Selected files stay where they are. Keep them available until installation finishes.")),
  ];
  if (view.boot.web) parts.push(el("label", { for: "multiple-paths" }, t("Several file paths on this computer (one per line)")), el("textarea", {
    id: "multiple-paths", rows: 2, value: view.multiplePaths, disabled: view.busy || view.preparing, spellcheck: "false", autocomplete: "off",
    oninput: (e) => { view.multiplePaths = e.target.value; },
  }));
  parts.push(el("button", { id: "pick-several", type: "button", disabled: view.busy || view.preparing, onclick: () => chooseFiles() }, t("Choose several files…")));
  if (checking) parts.push(el("div", { class: "file-progress", role: "status" }, [
    el("p", {}, t("Checking {name}: {done} / {total}", { name: check.name, done: sizeText(check.done), total: sizeText(check.total) })),
    el("progress", { value: check.done, max: check.total || 1, "aria-label": t("Checking files") }),
    el("button", { id: "cancel-file-check", type: "button", onclick: async () => { receive(await api("artifacts/cancel", {})); } }, t("Cancel checking")),
  ]));
  for (const result of view.state?.fileResults || []) {
    if (result.problem) parts.push(el("p", { class: "error-box file-result", role: "alert" }, problemText(t, result.problem)));
  }
  if (view.rowErrors["-"]) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.rowErrors["-"])));
  parts.push(el("ul", { class: "needed-packages" }, neededRows(view.state, view.choice).map(packageRow)),
    selected?.downgrade ? el("div", { class: "warning" }, [el("p", {}, t("Warning: this installs an older version.")), el("label", {}, [
      el("input", { id: "install-downgrade", type: "checkbox", checked: view.downgrade, disabled: view.busy,
        onchange: (event) => { view.downgrade = event.target.checked; render(); } }), t("Allow downgrade"),
    ])]) : "",
    el("div", { class: "actions" }, [back(1), next(3, !checking && !view.preparing && canContinue(view.state, view.choice, view.downgrade))]));
  return parts;
}

function backupView() {
  const skip = canSkipBackup(selectedChoice(view.state, view.choice));
  return [title(t("Back up")), el("p", { class: skip ? "tag" : "warning" }, skip ? t("Skipped") : t("A verified backup is required before flashing these layers.")),
    skip ? el("p", {}, t("Not needed: these layers do not erase the device.")) : "",
    ...(!skip ? backupDetails() : []),
    el("div", { class: "actions" }, [back(2), next(4, (skip || !!view.backups && !view.backupError) && canContinue(view.state, view.choice, view.downgrade))])];
}

function dateText(date) { return date ? new Date(date).toLocaleString(lang()) : ""; }
function backupDetails() {
  const listing = view.backups;
  const serial = serialFor(view.state, view.choice);
  const restoration = backupRestoration(selectedChoice(view.state, view.choice), listing);
  const parts = [el("p", {}, t("These four partitions hold this dongle's own radio calibration and IMEI.")),
    el("p", {}, t("The app saves them on this computer before erasing the system, and only ever writes this dongle's own copy back.")),
    view.choice === "stock" ? el("p", { class: "warning" }, t("Original firmware must first restart through lk1st, replacing the bootloader and clearing boot so the four partitions can be read. The backup is saved before erasing the system.")) : "",
    listing?.folder ? el("code", { class: "backup-folder" }, listing.folder) : "",
    view.busy && !listing ? el("p", { role: "status" }, t("Reading backups…")) : "",
    listing?.sets?.length ? el("div", { class: "backup-sets" }, listing.sets.map((set) => el("details", { open: true }, [
      el("summary", {}, dateText(set.created) + " · " + set.set),
      el("ul", {}, ["fsc", "fsg", "modemst1", "modemst2"].map((name) => {
        const p = set.partitions[name];
        return el("li", {}, [el("strong", {}, name + ".bin"), " · " + sizeText(p.size) + " · ",
          el("code", { title: p.sha256 }, p.shortSHA256), " · ", p.source === "imported" ? t("Imported backup") : t("Captured from this device")]);
      })),
    ]))) : listing ? el("p", {}, view.choice === "stock" ? t("Will be created during flashing, after lk1st starts and before the system is erased.") : t("Will be created from the device during flashing, before anything is erased.")) : "",
    restoration.source === "previous-job" ? el("p", { class: "restore-source" }, t("Repair will write back the set saved before the interruption: {set}.", { set: restoration.set })) :
      restoration.source === "none" ? el("p", { class: "restore-source" }, t("This choice preserves the modem partitions; no backup is written back.")) :
      el("p", { class: "restore-source" }, t("This choice writes back the fresh backup taken from this dongle during flashing.")),
    el("p", { class: "note" }, view.state?.chainNotice ? t("Repair uses the saved backup set without reading the modem partitions again.") : t("A fresh dump is always taken during flashing; changed data is saved as a new backup set.")),
    el("p", { class: "note" }, t("Keep this backup folder; it belongs to this dongle.")),
  ];
  if (listing?.sets?.length) parts.push(el("button", { type: "button", disabled: view.busy, onclick: async () => {
    try { await api("backups/" + encodeURIComponent(serial) + "/show-folder", {}); }
    catch (error) { view.backupError = error; render(); }
  } }, view.boot.platform === "windows" ? t("Show in Explorer") : t("Show in Finder")));
  if (view.boot.web) parts.push(el("label", { for: "backup-path" }, t("Backup folder on this computer")), el("input", {
    id: "backup-path", type: "text", value: view.backupPath, disabled: view.busy, spellcheck: "false", autocomplete: "off",
    oninput: (e) => { view.backupPath = e.target.value; },
  }));
  parts.push(el("button", { type: "button", disabled: view.busy, onclick: async () => {
    view.busy = true; view.backupError = null; render();
    try {
      await api(view.boot.web ? "backups/import" : "backups/pick", { serial, ...(view.boot.web ? { dir: view.backupPath.trim() } : {}) });
    } catch (error) { view.backupError = error; }
    view.busy = false;
    if (!view.backupError) await readBackups(); else render();
  } }, t("Use an existing backup folder…")));
  if (view.backupError) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.backupError)),
    el("button", { type: "button", disabled: view.busy, onclick: readBackups }, t("Try again")));
  return parts;
}

function confirmation(selected) {
  const has = (layer) => selected.layers.includes(layer);
  const device = view.state.target.device;
  return el("div", { class: "confirm" + (selected.destructive ? " destructive-confirm" : ""), role: "alertdialog", "aria-labelledby": "install-confirm-title", "aria-describedby": "install-confirm-warning" }, [
    el("strong", { id: "install-confirm-title" }, t("Start this installation?")),
    el("p", { id: "install-confirm-warning", class: "warning flash-warning", role: "alert" }, device.connection === "network" ? t("Keep the box powered and connected to the network until installation finishes.") : t(confirmationWarning(selected))),
    el("p", { class: "confirmation-target" }, t("Target: {device}", { device: deviceSummary(t, view.state.target.name?.[lang()] || view.state.target.name?.en || (device.mode === "ssh" ? t("Phicomm N1") : t("UFI003 USB dongle")), device, serialFor(view.state, view.choice)) })),
    device.mode === "ssh" ? el("p", {}, t("Board serial (eth0 MAC): {serial}", { serial: serialFor(view.state, view.choice) })) : "",
    el("ul", {}, selected.needs.filter((n) => n.package).map((n) => el("li", {}, [t("{layer}: {version}", { layer: n.layer === "base" ? t("Base system") : name(n.layer), version: displayVersion(n.layer, n.package.version) }), n.layer === "base" && baseKernel() ? el("small", { class: "kernel-version" }, t("Kernel: {version}", { version: baseKernel() })) : ""]))),
    has("device") ? el("p", {}, t("Bluetooth restarts and the device reboots once for the device layer.")) : "",
    has("relay") ? el("p", {}, t("The relay restarts.")) : "",
    el("p", {}, t("CarPlay stops for a while. Do not install while driving.")),
    selected.destructive ? el("label", { for: "install-serial" }, t("Type the serial number shown on the device card to continue")) : "",
    selected.destructive ? el("p", { class: "serial-card" }, serialFor(view.state, view.choice)) : "",
    selected.destructive ? el("input", { id: "install-serial", type: "text", value: view.serial, spellcheck: "false", autocomplete: "off", autocapitalize: "off",
      oninput: (e) => { view.serial = e.target.value; document.getElementById("install-confirm").disabled = !canStart(view.state, view.choice, view.downgrade, view.busy, view.serial); } }) : "",
    el("div", { class: "actions" }, [el("button", { id: "install-confirm", class: "primary", type: "button", disabled: !canStart(view.state, view.choice, view.downgrade, view.busy, view.serial), onclick: start }, t("Install now")),
      el("button", { id: "install-cancel", type: "button", onclick: () => { view.confirm = false; render(); focus("install-start"); } }, t("Cancel"))]),
  ]);
}

function flashView() {
  if (running()) return [jobView(view.state.job)];
  const selected = selectedChoice(view.state, view.choice);
  return [title(t("Flash")), selected ? flashSummary(selected) : "", selected ? stepList(selected) : "",
    view.confirm && selected ? confirmation(selected) : el("div", { class: "actions" }, [back(3),
      el("button", { id: "install-start", type: "button", class: "primary", disabled: !canContinue(view.state, view.choice, view.downgrade, view.busy),
        onclick: () => { view.confirm = true; render(); focus("install-cancel"); } }, t("Start"))])];
}

function flashSummary(selected) {
  const has = (layer) => selected.layers.includes(layer);
  return el("section", { class: "flash-summary", "aria-label": t("Installation summary") }, [
    el("h3", {}, choiceLabel(selected)), el("p", {}, t(CHOICE_TEXT[selected.id])),
    el("p", { class: selected.destructive ? "warning" : "note" }, selected.destructive ? t("Erases the device") : t("Keeps the device's system and data.")),
    el("ul", {}, selected.needs.filter((n) => n.package).map((n) => el("li", {}, t("{layer}: {version}", { layer: n.layer === "base" ? t("Base system") : name(n.layer), version: displayVersion(n.layer, n.package.version) })))),
    has("base") ? el("p", {}, t("Base system: about 6 minutes. The device restarts.")) : "",
    has("device") ? el("p", {}, t("Device support: about 1 minute plus a device restart.")) : "",
    has("relay") ? el("p", {}, t("Relay: under a minute. The relay restarts.")) : "",
    el("p", { class: "note" }, view.state.target.device.connection === "network" ? t("Keep the box powered and connected to the network until installation finishes.") : t("Keep the device connected and powered until installation finishes.")),
  ]);
}

function jobView(job) {
  const active = job.steps.find((s) => s.status === "running");
  const parts = [el("h2", { id: "install-job-title", tabindex: "-1" }, t("Installation progress")),
    job.status === "running" ? el("p", { role: "status" }, active ? t("Installing {layer}: {step}", { layer: name(active.layer), step: stepLabel(active) }) : t("Starting installation…")) : "",
    el("p", { id: "install-elapsed" }, t("Elapsed: {time}", { time: elapsed(job) }))];
  if (active && job.status === "running") parts.push(el("div", { class: "phase-warning " + phaseStyle(active), role: "alert" }, [
    el("strong", {}, t(PHASE_TEXT[phaseGroups(job.steps).find((g) => g.steps.includes(active)).phase] || "Installation progress")),
    el("p", {}, view.state.target?.device?.connection === "network" ? t("Keep the box powered and connected to the network until installation finishes.") : active.critical ? t("Do not unplug the device or cut its power") : active.resumable ? t("If this is interrupted, it can be repeated") : ""),
  ]));
  if (job.status === "running" && ["adb-bootloader", "reboot-bootloader"].includes(active?.action)) parts.push(el("p", { role: "status" }, t("The dongle is restarting into the bootloader…")));
  if (job.status === "running" && active?.action === "wait-fastboot") parts.push(el("p", { role: "status" }, t("Waiting for the bootloader to reconnect…")));
  if (job.status === "running" && active?.action === "wait-linux") parts.push(el("p", { role: "status" }, t("Waiting for Linux to start (up to 3 minutes)…")));
  if (job.transfer && job.status === "running") {
    const p = job.transfer;
    // Fastboot exposes bytes while sending. Writing has no byte callbacks, so retain the
    // chunk's sent position instead of presenting a byte counter stuck at zero.
    const sent = p.phase === "sending" ? Math.min(1, p.done / (p.total || 1)) : 1;
    parts.push(el("p", { class: "transfer-progress", role: "status" }, p.phase === "sending" ?
      t("Sending {piece}/{pieces}", p) + " · " + sizeText(p.done) + " / " + sizeText(p.total) : t("Writing {piece}/{pieces}", p)),
      el("progress", { value: p.piece - 1 + sent, max: p.pieces, "aria-label": t("Installation progress") }));
    if (p.estimateSeconds > 0) parts.push(el("p", { class: "note" }, t("Estimated time for this transfer: {seconds} s", { seconds: p.estimateSeconds })));
  }
  if (job.findings?.backupComparison?.startsWith("larger")) parts.push(el("p", { class: "note" }, t("The modem partitions are larger in the new partition table; the data is unchanged and was saved as a new backup set at the new size.")));
  else if (job.findings?.backupComparison?.startsWith("different")) parts.push(el("p", { class: "note" }, t("The modem's EFS data changes while Linux runs. This is normal; a new backup set was saved.")));
  if (job.status === "succeeded") {
    parts.push(el("div", { id: "install-result", tabindex: "-1", class: "result ok", role: "status" }, [el("strong", {}, t("Installation succeeded.")),
      el("p", { class: "unplug-status" }, nextStepsState(view.state).relay ? t("Keep it connected for the next step") : t(unplugText(job))), installed(job.installed, !job.observed || view.state.installedStale),
      findingsView(job),
    ]));
    parts.push(nextStepsCard());
  } else if (job.status === "failed") {
    const step = job.steps[job.failed];
    parts.push(el("div", { id: "install-result", tabindex: "-1", class: "error-box", role: "alert" }, [el("strong", {}, t("Installation failed.")),
      el("p", { class: "unplug-status" }, t(unplugText(job))),
      step ? el("p", {}, t("Failed step: {layer} — {step}", { layer: name(step.layer), step: stepLabel(step) })) : "",
      el("p", {}, problemText(t, job.problem)), el("p", {}, view.state?.chainNotice ? view.state.chainNotice[lang()] || view.state.chainNotice.en : job.plan && job.left === "partial" ? t("The system installation is incomplete. Keep the device connected and choose Repair.") : t(LEFT_TEXT[job.left] || LEFT_TEXT.partial)),
      installed(job.installed, !job.observed || view.state.installedStale),
      job.plan && view.state.resume && !view.state.resume.finished ? el("button", { type: "button", disabled: !canChoose(view.state, "repair"), onclick: selectRepair }, t("Repair")) : "",
      job.retryable ? el("button", { type: "button", disabled: view.busy, onclick: async () => {
        view.busy = true; view.error = null; render();
        try { receive(await api("install/retry", { started: job.started })); }
        catch (error) { view.error = error; }
        view.busy = false; render();
      } }, t("Retry this step")) : "",
    ]));
  }
  parts.push(el("div", { class: "job-phases" }, phaseGroups(job.steps).map((group) => el("details", { open: group.steps.some((s) => ["running", "failed"].includes(s.status)), class: "job-phase " + (group.phase === "critical" ? "critical" : "") }, [
    el("summary", {}, t(PHASE_TEXT[group.phase] || "Installation progress")),
    el("ol", { class: "job-steps" }, group.steps.map((s) => el("li", { class: s.status + " " + phaseStyle(s), "aria-current": s.status === "running" ? "step" : false }, [
      stepLabel(s), el("span", {}, t(STATUS_TEXT[s.status])),
    ]))),
  ]))));
  const details = el("details", { id: "install-log" }, [el("summary", { id: "install-log-summary" }, t("Session log")),
    job.logCapped ? el("p", { class: "note" }, t("The log reached its limit; only the most recent 1 MiB is kept.")) : "",
    el("pre", { id: "install-log-text", tabindex: "0", "aria-label": t("Session log") }, job.log)]);
  details.open = view.logOpen;
  details.addEventListener("toggle", () => { view.logOpen = details.open; });
  parts.push(details, el("div", { class: "actions" }, [el("button", { id: "install-save-log", type: "button", onclick: saveLog }, t("Save log…")),
    job.status !== "running" ? el("button", { id: "install-back-layers", type: "button", class: "primary", disabled: view.busy, onclick: backToLayers }, t("Back to layers")) : ""]));
  return el("div", { class: "install-job" }, parts);
}

function findingsView(job) {
  const facts = job.installed;
  if (!facts && !job.findings) return "";
  return el("details", { class: "base-findings" }, [el("summary", {}, t("Base system findings")),
    el("dl", { class: "installed" }, [
      el("dt", {}, t("Kernel")), el("dd", {}, facts?.kernelRelease || "—"),
      el("dt", {}, t("Debian")), el("dd", {}, facts?.debianVersion || "—"),
      el("dt", {}, t("Free space")), el("dd", {}, job.findings?.["df -h /"] || Object.entries(facts?.freeKiB || {}).map(([path, kib]) => path + ": " + sizeText(kib*1024)).join(" · ") || "—"),
      el("dt", {}, t("Failed units")), el("dd", {}, el("pre", {}, job.findings?.["systemctl --failed --no-pager"] || "—")),
    ]),
  ]);
}

async function importIdentity() {
  if (view.busy) return;
  view.busy = true; view.nextError = null; view.identityMessage = ""; render();
  try {
    const picked = view.boot.web ? parsePaths(view.identityPath) : (await api("identity/pick", {})).paths || [];
    if (picked.length) {
      view.identityPaths = [...new Set([...view.identityPaths, ...picked])];
      view.identityPath = "";
      receive(await api("identity/import", { paths: view.identityPaths }));
      view.identityPaths = []; view.identityMessage = t("Identity added");
    }
  } catch (error) { view.nextError = error; if (error.code !== "identity-missing") view.identityPaths = []; }
  view.busy = false; render();
}

async function openAdmin() {
  view.busy = true; view.nextError = null; render();
  // Reserve a browser tab from the click so popup blockers do not discard the later URL.
  const tab = view.boot.web ? window.open("about:blank", "_blank") : null;
  try {
    const forward = await api("admin/forward/start", {});
    view.state = { ...view.state, nextSteps: { ...view.state.nextSteps, forward } };
    if (view.boot.web) { if (tab) { tab.opener = null; tab.location = forward.address; } }
    else await api("open", { url: forward.address });
  } catch (error) { tab?.close(); view.nextError = error; }
  view.busy = false; render();
}

function nextStepsCard() {
  const next = nextStepsState(view.state);
  const disabled = view.busy || !next.connected;
  const parts = [el("h3", {}, t("Next steps"))];
  if (!next.relay) return el("section", { class: "next-steps", "aria-label": t("Next steps") }, [...parts,
    el("p", {}, t("Install the remaining layers to start the relay.")),
    el("button", { type: "button", disabled: view.busy, onclick: backToLayers }, t("Back to layers")),
    el("a", { href: view.state?.target?.profile === "phicomm-n1" ? linksFor(view.boot.links,lang()).phicommN1 : lang() === "zh-CN" ? "https://openxplay.org/ufi003-relay#step-network" : "https://openxplay.org/en/ufi003-relay#step-network", "data-open": "" }, t("Continue with network setup")),
  ]);
  if (next.identity === "present") parts.push(el("div", { class: "identity-success result ok", role: "status" }, [
    el("strong", {}, t("Identity added")), el("p", {}, t("Files: {files}", { files: "identity.pk8 · certificate.p7b" })),
  ]));
  parts.push(el("h3", {}, t("Add your CarPlay identity")), el("p", {}, t("Choose the private key and certificate together or one after the other, in any order (DER or PEM; any file names), or a ZIP containing identity.pk8 and certificate.p7b.")));
  if (view.boot.web) parts.push(el("label", { for: "identity-path" }, t("Paths to identity files or a ZIP on this computer (one per line)")), el("textarea", {
    id: "identity-path", rows: 2, value: view.identityPath, disabled, spellcheck: "false", autocomplete: "off",
    oninput: (e) => { view.identityPath = e.target.value; },
  }));
  if (view.identityPaths.length) parts.push(el("p", { class: "note" }, view.identityPaths.map((p) => p.split(/[\\/]/).at(-1)).join(" · ")));
  parts.push(el("button", { id: "identity-pick", type: "button", disabled, onclick: importIdentity }, t("Choose identity files or ZIP…")));
  if (next.identity === "unknown") parts.push(el("button", { type: "button", disabled, onclick: async () => {
    view.busy = true; view.nextError = null; render();
    try { const state = await api("identity"); view.state = { ...view.state, nextSteps: state }; }
    catch (error) { view.nextError = error; }
    view.busy = false; render();
  } }, t("Check identity status")));
  if (view.identityMessage && next.identity !== "present") parts.push(el("p", { class: "check ok" }, view.identityMessage));
  if(view.state?.target?.device?.mode === "ssh") {
    const host=view.state.target.device.host;
    const address=view.state.nextSteps?.adminUrl || "https://"+(host.includes(":")?"["+host+"]":host)+":6975/";
    parts.push(el("h3",{},t("Open the relay admin page")),el("a",{id:"admin-open",href:address,"data-open":""},t("Open the relay admin page")),
      el("p",{},t("Expect a certificate warning in your browser. The first-time password is on the login page.")));
  } else {
  parts.push(el("h3", {}, t("Open the relay admin page over USB")),
    el("p", {}, t("Expect a certificate warning in your browser. The first-time password is on the login page.")),
    el("button", { id: "admin-open", type: "button", disabled, onclick: openAdmin }, t("Open the relay admin page over USB")));
  }
  if (next.forward) parts.push(el("a", { href: next.forward.address, "data-open": "", class: "admin-address" }, next.forward.address),
    el("button", { type: "button", disabled: view.busy, onclick: async () => {
      try { const forward = await api("admin/forward/stop", {}); view.state = { ...view.state, nextSteps: { ...view.state.nextSteps, forward } }; }
      catch (error) { view.nextError = error; }
      render();
    } }, t("Close USB admin connection")));
  if (view.nextError) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.nextError)));
  parts.push(el("a", { href: view.state?.target?.profile === "phicomm-n1" ? linksFor(view.boot.links,lang()).phicommN1 : lang() === "zh-CN" ? "https://openxplay.org/ufi003-relay#step-network" : "https://openxplay.org/en/ufi003-relay#step-network", "data-open": "" }, t("Continue with network setup")));
  return el("section", { class: "next-steps", "aria-label": t("Next steps") }, parts);
}

export function render() {
  if (!root) return;
  const focused = root.contains(document.activeElement) ? document.activeElement.id : "";
  const input = document.activeElement;
  const selection = input?.tagName === "INPUT" || input?.tagName === "TEXTAREA" ? [input.selectionStart, input.selectionEnd] : null;
  const log = document.getElementById("install-log-text");
  const logScroll = log?.scrollTop || 0;
  const atEnd = !log || log.scrollHeight - log.scrollTop - log.clientHeight < 20;
  const step = phase(view.state, view.step);
  const banner = document.getElementById("install-running-warning");
  const signatureNotice = document.getElementById("signature-off-notice");
  signatureNotice.hidden = (view.state?.catalog?.signatureCheck ?? view.boot.verifyPackageListSignature) !== false;
  signatureNotice.textContent = t("Signature check is off: the package list is not verified");
  banner.hidden = !running();
  banner.textContent = view.state?.target?.device?.connection === "network" ? t("Keep the box powered and connected to the network until installation finishes.") : t(WARNING_TEXT.running);
  banner.classList.toggle("critical", !!view.state?.job?.steps?.some((s) => s.status === "running" && s.critical));
  document.getElementById("install-connect").hidden = step !== 0;
  const renderStep = [connectView, chooseView, prepareView, backupView, flashView, () => view.state?.job ? [jobView(view.state.job)] : []][step];
  const parts = renderStep();
  if (view.state?.chainNotice) parts.unshift(el("p", { class: "warning critical", role: "status" }, view.state.chainNotice[lang()] || view.state.chainNotice.en));
  if (view.state?.problem) parts.unshift(el("p", { class: "error-box", role: "status" }, problemText(t, view.state.problem)));
  if (view.error) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.error)));
  root.replaceChildren(...parts);
  for (const [id, value] of [["multiple-paths", view.multiplePaths], ["identity-path", view.identityPath]]) {
    const input = document.getElementById(id); if (input) input.value = value;
  }
  if (focused) { const again = document.getElementById(focused); if (again && !again.disabled) again.focus(); }
  if (selection) document.getElementById(focused)?.setSelectionRange(...selection);
  const freshLog = document.getElementById("install-log-text");
  if (freshLog) freshLog.scrollTop = atEnd ? freshLog.scrollHeight : logScroll;
  document.querySelectorAll(".steps li").forEach((li, index) => {
    li.classList.toggle("current", index === step);
    li.classList.toggle("skipped", index === 3 && step > 2 && canSkipBackup(selectedChoice(view.state, view.choice)));
    if (index === step) li.setAttribute("aria-current", "step"); else li.removeAttribute("aria-current");
  });
  document.querySelectorAll("[data-install-step]").forEach((button) => {
    button.disabled = !canNavigate(view.state, view.choice, Number(button.dataset.installStep), view.busy);
  });
}

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { t, lang } from "./i18n.js";
import { api, el } from "./ui.js";
import { linksFor } from "./settingslogic.js";
import { ACTION_TEXT, LAYER_TEXT, STATUS_TEXT, LEFT_TEXT, COMPARISON_TEXT, WARNING_TEXT,
  problemText, canChoose, canContinue, canStart, canNavigate, canSkipBackup, selectedChoice, neededRows,
  isRunning, confirmationWarning, unplugText, guardBeforeUnload, displayVersion, elapsed, phase, mergeState,
  suggestedAction, serialFor, PHASE_TEXT, phaseStyle, phaseGroups, nextStepsState, identitySelection, sizeText, backupRestoration } from "./installlogic.js";

const view = { boot: {}, state: null, paths: {}, choice: "", step: 0, initialized: false,
  downgrade: false, busy: false, preparing: false, confirm: false, serial: "", error: null, rowErrors: {}, logOpen: false,
  backups: null, backupError: null, backupPath: "", artifactPaths: {}, totalNotice: "", notices: new Set(),
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
  render();
  focus("install-step-title");
  if (step === 2) {
    // A finished job may have observed only its own packages' requirements. Recheck all
    // provider rows on entering Prepare so earlier session packages are reused correctly.
    const total = neededRows(view.state, view.choice).flatMap((n) => n.artifacts || []).filter((a) => !a.verified).reduce((n, a) => n+a.size, 0);
    const notice = view.choice + ":" + total;
    view.totalNotice = total && !view.notices.has(notice) ? sizeText(total) : "";
    view.notices.add(notice);
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
  view.rowErrors[layer] = null;
  render();
  try { receive(await action()); await refresh(); }
  catch (err) { view.rowErrors[layer] = err; }
  view.busy = false;
  render();
}

function add(layer) {
  return changePackage(layer, () => view.boot.web
    ? api("install/packages", { layer, path: (view.paths[layer] || "").trim() }) : api("install/pick", { layer }));
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
      el("dt", {}, name(id)), el("dd", {}, facts.layers[id] ? displayVersion(id, facts.layers[id]) : t("Not installed")),
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
    el("div", { class: "actions" }, [el("button", { type: "button", disabled: view.busy, onclick: refresh }, t("Read again")),
      next(1, canNavigate(view.state, view.choice, 1))])];
}

function guide() {
  return el("div", { class: "base-guide" }, [
    el("p", { class: "note" }, t("Flashing the base needs fastboot; this version cannot do it yet.")),
    el("a", { href: linksFor(view.boot.links, lang()).manual, "data-open": "" }, t("Manual base installation guide")),
  ]);
}

function choose(id) {
  if (!canChoose(view.state, id, view.busy)) return;
  if (view.choice !== id) view.downgrade = false;
  view.choice = id;
  view.serial = "";
  view.confirm = false;
  render();
}

function choiceButton(id, text, enabled) {
  return el("button", { id: "choice-" + id, type: "button", class: view.choice === id ? "primary" : "",
    "aria-pressed": String(view.choice === id), disabled: view.busy || !enabled, onclick: () => choose(id) }, text);
}

function suggested(id) { return view.state?.suggested === id ? el("span", { class: "tag suggested" }, t("Suggested")) : ""; }

function stepLabel(s) { return s.name?.[lang()] || s.name?.en || t(ACTION_TEXT[s.action] || s.action); }

function stepList(choice) {
  return el("details", { class: "choice-steps" }, [el("summary", {}, t("Show steps")),
    el("ol", {}, choice.steps.map((s) => el("li", {}, name(s.layer) + ": " + stepLabel(s))))]);
}

function chooseView() {
  const state = view.state;
  const parts = [title(t("Choose layers")), freshness(state?.installedStale)];
  const full = selectedChoice(state, "full");
  if (full?.problem?.code === "base-missing" && !state.choices.some((c) => c.name)) {
    return [...parts, el("p", { class: "warning", role: "status" }, problemText(t, full.problem)), guide()];
  }
  if (full) parts.push(el("div", { class: "full-choice" + (state.suggested === "full" ? " suggested-choice" : "") }, [
    el("div", { class: "actions" }, [choiceButton("full", t("Full install"), canChoose(state, "full")), suggested("full")]),
    el("p", { class: "note" }, t("Installs {layers} in order.", { layers: full.layers.map(name).join(" → ") })),
    full.problem ? el("p", { class: "note" }, problemText(t, full.problem)) : "",
  ]));
  parts.push(el("ul", { class: "layer-rows" }, (state?.layers || []).map((row) => el("li", {
    class: "layer-row" + (state.suggested === row.choice ? " suggested-choice" : ""),
  }, [el("div", {}, [el("strong", {}, name(row.layer)),
    el("p", { class: "layer-version" }, row.installed ? displayVersion(row.layer, row.installed) : t("Not installed"))]),
    row.action === "guide" ? guide() : el("div", {}, [
      el("div", { class: "actions" }, [choiceButton(row.choice, row.actionName?.[lang()] || row.actionName?.en || (row.action === "install" ? t("Install") : t("Update / reinstall")), row.enabled), suggested(row.choice)]),
      selectedChoice(state, row.choice)?.destructive ? el("span", { class: "tag destructive-tag" }, t("Erases the device")) : "",
      row.reason ? el("p", { class: "note" }, problemText(t, row.reason)) : "",
      row.reason?.code === "fastboot-unsupported" ? el("a", { href: linksFor(view.boot.links, lang()).manual, "data-open": "" }, t("Manual base installation guide")) : "",
    ]),
  ]))));
  for (const c of state?.choices || []) {
    if (!c.name || state.layers.some((row) => row.choice === c.id)) continue;
    parts.push(el("div", { class: "full-choice" }, [
      choiceButton(c.id, c.name[lang()] || c.name.en, canChoose(state, c.id)),
      c.destructive ? el("span", { class: "tag destructive-tag" }, t("Erases the device")) : "", suggested(c.id),
      c.reason ? el("p", { class: "note" }, c.reason[lang()] || c.reason.en) :
        c.problem ? el("p", { class: "note" }, problemText(t, c.problem)) : "",
      c.problem?.code === "fastboot-unsupported" ? el("a", { href: linksFor(view.boot.links, lang()).manual, "data-open": "" }, t("Manual base installation guide")) : "",
    ]));
  }
  const selected = selectedChoice(state, view.choice);
  if (selected && !selected.problem) parts.push(stepList(selected));
  parts.push(el("div", { class: "actions" }, [back(0), next(2, canChoose(state, view.choice))]));
  return parts;
}

// The page consumes provider rows, never the session's whole package list. Future catalog
// controls can fill these same needs; picking a local file stays the fallback for each row.
function packageRow(need) {
  const layer = need.layer, pkg = need.package, disabled = view.busy || running();
  const parts = [el("h3", {}, name(layer)),
    el("ul", { class: "artifact-rows" }, (need.artifacts || []).filter((a) => a.role !== "local").map((a) => artifactRow(layer, a)))];
  if (pkg) {
    const packageParts = [el("strong", {}, pkg.name), el("p", {}, t("{layer} · {version} · {size} bytes", { layer: name(layer), version: pkg.version, size: pkg.size })),
      pkg.sha256 ? el("code", { title: pkg.sha256 }, "SHA-256 " + pkg.sha256.slice(0, 12) + "…") : "",
      pkg.checked ? el("p", { class: "check ok" }, t("Checks passed")) : "",
      need.comparison ? el("p", { class: need.comparison === "downgrade" ? "check bad" : "check" }, t(COMPARISON_TEXT[need.comparison])) : "",
      view.state.installed?.layers[layer] ? el("p", { class: "note" }, t("Installed: {version}", { version: displayVersion(layer, view.state.installed.layers[layer]) })) : ""];
    if (need.artifacts?.some((a) => a.role !== "local")) parts.push(el("details", { class: "package-details" }, [el("summary", {}, t("Package details")), ...packageParts]));
    else parts.push(...packageParts);
  } else parts.push(el("p", { class: "needed" }, t("Needed")));
  if (need.problem && !(need.problem.code === "downgrade" && view.downgrade)) parts.push(el("p", { class: "error-box" }, problemText(t, need.problem)));
  if (view.rowErrors[layer]) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.rowErrors[layer])));
  const fallback = [el("summary", {}, t("Use a local package…")), el("p", { class: "note" }, t("Accepted files: {types}", { types: (need.fileTypes || []).join(" / ") }))];
  if (view.boot.web && layer !== "base") {
    const id = "install-path-" + layer;
    fallback.push(el("label", { for: id }, t("Package path on this computer")), el("input", {
      id, type: "text", value: view.paths[layer] || "", disabled, spellcheck: "false", autocomplete: "off", autocapitalize: "off",
      oninput: (event) => { view.paths[layer] = event.target.value; },
      onkeydown: (event) => { if (event.key === "Enter" && view.paths[layer]?.trim()) { event.preventDefault(); add(layer); } },
    }));
  }
  fallback.push(el("div", { class: "actions" }, [
    el("button", { id: "pick-" + layer, type: "button", disabled, onclick: () => add(layer) }, t("Choose file…")),
    pkg ? el("button", { id: "remove-" + layer, type: "button", disabled, "aria-label": t("Remove {name}", { name: pkg.name }),
      onclick: () => changePackage(layer, () => api("install/packages/" + pkg.id, {}, "DELETE")) }, t("Remove")) : "",
  ]));
  if (layer !== "base") parts.push(el("details", { class: "local-package" }, fallback));
  return el("li", { class: "package-row", "data-package-layer": layer }, parts);
}

function artifactRow(layer, artifact) {
  const key = layer + "-" + artifact.role;
  const disabled = view.busy || view.preparing || running();
  const percent = artifact.size ? Math.min(100, Math.floor(artifact.done/artifact.size*100)) : 0;
  const parts = [el("div", { class: "artifact-heading" }, [el("strong", {}, artifact.name), el("span", {}, sizeText(artifact.size))])];
  if (artifact.error) parts.push(el("p", { class: "error-box", role: "status" }, problemText(t, artifact.error)),
    el("button", { type: "button", disabled, onclick: () => prepare(() => api("artifacts/retry", { layer, role: artifact.role })) }, t("Retry")));
  else if (artifact.verified) parts.push(el("p", { class: "check ok" }, artifact.status === "cached" ? t("Cached ✓") : t("Verified ✓")));
  else if (artifact.status === "downloading" || artifact.done > 0) parts.push(
    el("p", { role: "status" }, t("Downloading: {done} / {total} · {percent}% · {speed}/s", {
      done: sizeText(artifact.done), total: sizeText(artifact.size), percent, speed: sizeText(artifact.bytesPerSecond || 0) })),
    el("progress", { value: artifact.done, max: artifact.size, "aria-label": artifact.name }));
  else parts.push(el("p", { class: "note" }, t("Pending")));
  const fallback = [el("summary", {}, t("Use a file I downloaded…")), el("p", { class: "note" }, t("Matched by SHA-256; the file name does not matter."))];
  if (view.boot.web) fallback.push(el("label", { for: "artifact-path-" + key }, t("Package path on this computer")), el("input", {
    id: "artifact-path-" + key, type: "text", value: view.artifactPaths[key] || "", disabled,
    spellcheck: "false", autocomplete: "off", oninput: (e) => { view.artifactPaths[key] = e.target.value; },
  }));
  fallback.push(el("button", { type: "button", disabled, onclick: async () => {
    await changePackage(key, () => api(view.boot.web ? "artifacts/import" : "artifacts/pick", {
      layer, role: artifact.role, ...(view.boot.web ? { path: (view.artifactPaths[key] || "").trim() } : {}),
    }));
    if (!view.rowErrors[key]) await prepare();
  } }, t("Choose file…")));
  parts.push(el("details", {}, fallback));
  if (view.rowErrors[key]) parts.push(el("p", { class: "error-box" }, problemText(t, view.rowErrors[key])));
  return el("li", { class: "artifact-row", "data-artifact": key }, parts);
}

async function refreshCatalog() {
  view.busy = true; view.error = null; render();
  try { await api("catalog/fetch", { channel: "beta" }); } catch (error) { view.error = error; }
  await refresh(); view.busy = false; render();
  if (view.state?.catalog?.index && view.step === 2) await prepare();
}
function catalogView() {
  const c = view.state?.catalog;
  if (c?.status === "fresh") return el("p", { class: "catalog-status check ok" }, t("Package list: up to date ({channel}, #{n})", { channel: c.channel, n: c.index?.sequence || 0 }));
  if (c?.status === "cached/offline") return el("p", { class: "catalog-status warning" }, t("Package list: offline; using the saved list from {date}", { date: dateText(c.index?.generated || c.updated) }));
  return el("div", { class: "catalog-status" }, [el("p", { class: c?.status === "failed" ? "error-box" : "note" },
    c?.status === "failed" ? t("Package list failed: {reason}", { reason: c.reason || "" }) : t("Loading the package list…")),
    el("button", { type: "button", disabled: view.busy || view.preparing || c?.status === "fetching", onclick: refreshCatalog }, t("Try again"))]);
}

function prepareView() {
  const selected = selectedChoice(view.state, view.choice);
  return [title(t("Prepare packages")), freshness(view.state?.installedStale), catalogView(),
    view.totalNotice ? el("p", { class: "download-total", role: "status" }, t("Total download: {size}. Downloads start automatically.", { size: view.totalNotice })) : "",
    el("ul", { class: "needed-packages" }, neededRows(view.state, view.choice).map(packageRow)),
    selected?.downgrade ? el("div", { class: "warning" }, [el("p", {}, t("Warning: this installs an older version.")), el("label", {}, [
      el("input", { id: "install-downgrade", type: "checkbox", checked: view.downgrade, disabled: view.busy,
        onchange: (event) => { view.downgrade = event.target.checked; render(); } }), t("Allow downgrade"),
    ])]) : "",
    el("div", { class: "actions" }, [back(1), next(3, !view.preparing && canContinue(view.state, view.choice, view.downgrade))])];
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
    el("p", { class: "note" }, t("A fresh dump is always taken during flashing; changed data is saved as a new backup set.")),
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
    el("p", { id: "install-confirm-warning", class: "warning flash-warning", role: "alert" }, t(confirmationWarning(selected))),
    el("p", {}, t("Target: {device}", { device: view.state.target.profile + " · " + (device.usb.serial || device.usb.path?.join(".") || "") })),
    el("ul", {}, selected.needs.filter((n) => n.package).map((n) => el("li", {}, name(n.layer) + ": " + n.package.version))),
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
  return [title(t("Flash")), selected ? stepList(selected) : "",
    view.confirm && selected ? confirmation(selected) : el("div", { class: "actions" }, [back(3),
      el("button", { id: "install-start", type: "button", class: "primary", disabled: !canContinue(view.state, view.choice, view.downgrade, view.busy),
        onclick: () => { view.confirm = true; render(); focus("install-cancel"); } }, t("Start"))])];
}

function jobView(job) {
  const active = job.steps.find((s) => s.status === "running");
  const parts = [el("h2", { id: "install-job-title", tabindex: "-1" }, t("Installation progress")),
    job.status === "running" ? el("p", { role: "status" }, active ? t("Installing {layer}: {step}", { layer: name(active.layer), step: stepLabel(active) }) : t("Starting installation…")) : "",
    el("p", { id: "install-elapsed" }, t("Elapsed: {time}", { time: elapsed(job) }))];
  if (active && job.status === "running") parts.push(el("div", { class: "phase-warning " + phaseStyle(active), role: "alert" }, [
    el("strong", {}, t(PHASE_TEXT[phaseGroups([active])[0].phase] || "Installation progress")),
    el("p", {}, active.critical ? t("Do not unplug the device or cut its power") : active.resumable ? t("If this is interrupted, it can be repeated") : ""),
  ]));
  if (job.status === "running" && ["adb-bootloader", "reboot-bootloader"].includes(active?.action)) parts.push(el("p", { role: "status" }, t("The dongle is restarting into the bootloader…")));
  if (job.status === "running" && active?.action === "wait-fastboot") parts.push(el("p", { role: "status" }, t("Waiting for the bootloader to reconnect…")));
  if (job.status === "running" && active?.action === "wait-linux") parts.push(el("p", { role: "status" }, t("Waiting for Linux to start (up to 3 minutes)…")));
  if (job.transfer && job.status === "running") {
    const p = job.transfer;
    parts.push(el("p", { class: "transfer-progress", role: "status" }, (p.phase === "sending" ? t("Sending {piece}/{pieces}", p) : t("Writing {piece}/{pieces}", p)) + " · " + sizeText(p.done) + " / " + sizeText(p.total)),
      el("progress", { value: p.done, max: p.total, "aria-label": t("Installation progress") }));
    if (p.estimateSeconds > 0) parts.push(el("p", { class: "note" }, t("Estimated time for this transfer: {seconds} s", { seconds: p.estimateSeconds })));
  }
  if (job.findings?.backupComparison?.startsWith("different")) parts.push(el("p", { class: "note" }, t("The modem's EFS data changes while Linux runs. This is normal; a new backup set was saved.")));
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
      el("p", {}, problemText(t, job.problem)), el("p", {}, job.plan && job.left === "partial" ? t("The system installation is incomplete. Keep the device connected and choose Repair.") : t(LEFT_TEXT[job.left] || LEFT_TEXT.partial)),
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
    const picked = view.boot.web ? (view.identityPath.trim() ? [view.identityPath.trim()] : []) : (await api("identity/pick", {})).paths || [];
    for (const path of picked) {
      if (/\.zip$/i.test(path)) view.identityPaths = [path];
      else {
        if (!["identity.pk8", "certificate.p7b"].includes(path.split(/[\\/]/).at(-1))) throw { code: "identity-name" };
        view.identityPaths = view.identityPaths.filter((old) => !/\.zip$/i.test(old) && old.split(/[\\/]/).at(-1) !== path.split(/[\\/]/).at(-1));
        view.identityPaths.push(path);
      }
    }
    view.identityPath = "";
    const selection = identitySelection(view.identityPaths);
    if (!selection.ready) view.nextError = { code: "identity-missing", detail: selection.missing };
    else {
      receive(await api("identity/import", { paths: view.identityPaths }));
      view.identityPaths = []; view.identityMessage = t("Identity added");
    }
  } catch (error) { view.nextError = error; }
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
    el("a", { href: lang() === "zh-CN" ? "https://openxplay.org/ufi003-relay#step-network" : "https://openxplay.org/en/ufi003-relay#step-network", "data-open": "" }, t("Continue with network setup")),
  ]);
  if (next.identity === "present") parts.push(el("p", { class: "check ok", role: "status" }, t("Identity added")));
  else {
    parts.push(el("h3", {}, t("Add your CarPlay identity")), el("p", {}, t("Choose identity.pk8 and certificate.p7b together or one after the other, in any order, or a ZIP containing both.")));
    if (view.boot.web) parts.push(el("label", { for: "identity-path" }, t("Path to an identity file or ZIP on this computer")), el("input", {
      id: "identity-path", type: "text", value: view.identityPath, disabled, spellcheck: "false", autocomplete: "off",
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
  }
  if (view.identityMessage && next.identity !== "present") parts.push(el("p", { class: "check ok" }, view.identityMessage));
  parts.push(el("h3", {}, t("Open the relay admin page over USB")),
    el("p", {}, t("Expect a certificate warning in your browser. The first-time password is on the login page.")),
    el("button", { id: "admin-open", type: "button", disabled, onclick: openAdmin }, t("Open the relay admin page over USB")));
  if (next.forward) parts.push(el("a", { href: next.forward.address, "data-open": "", class: "admin-address" }, next.forward.address),
    el("button", { type: "button", disabled: view.busy, onclick: async () => {
      try { const forward = await api("admin/forward/stop", {}); view.state = { ...view.state, nextSteps: { ...view.state.nextSteps, forward } }; }
      catch (error) { view.nextError = error; }
      render();
    } }, t("Close USB admin connection")));
  if (view.nextError) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.nextError)));
  parts.push(el("a", { href: lang() === "zh-CN" ? "https://openxplay.org/ufi003-relay#step-network" : "https://openxplay.org/en/ufi003-relay#step-network", "data-open": "" }, t("Continue with network setup")));
  return el("section", { class: "next-steps", "aria-label": t("Next steps") }, parts);
}

export function render() {
  if (!root) return;
  const focused = root.contains(document.activeElement) ? document.activeElement.id : "";
  const input = document.activeElement;
  const selection = input?.tagName === "INPUT" || focused.startsWith("install-path-") ? [input.selectionStart, input.selectionEnd] : null;
  const log = document.getElementById("install-log-text");
  const logScroll = log?.scrollTop || 0;
  const atEnd = !log || log.scrollHeight - log.scrollTop - log.clientHeight < 20;
  const step = phase(view.state, view.step);
  const banner = document.getElementById("install-running-warning");
  banner.hidden = !running();
  banner.textContent = t(WARNING_TEXT.running);
  banner.classList.toggle("critical", !!view.state?.job?.steps?.some((s) => s.status === "running" && s.critical));
  document.getElementById("install-connect").hidden = step !== 0;
  const renderStep = [connectView, chooseView, prepareView, backupView, flashView, () => view.state?.job ? [jobView(view.state.job)] : []][step];
  const parts = renderStep();
  if (view.state?.problem) parts.unshift(el("p", { class: "error-box", role: "status" }, problemText(t, view.state.problem)));
  if (view.error) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.error)));
  root.replaceChildren(...parts);
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

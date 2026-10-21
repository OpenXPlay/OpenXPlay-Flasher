// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { t, lang } from "./i18n.js";
import { api, el } from "./ui.js";
import { linksFor } from "./settingslogic.js";
import { ACTION_TEXT, LAYER_TEXT, STATUS_TEXT, LEFT_TEXT, COMPARISON_TEXT, WARNING_TEXT,
  problemText, canChoose, canContinue, canStart, canNavigate, canSkipBackup, selectedChoice, neededRows,
  isRunning, confirmationWarning, unplugText, guardBeforeUnload, displayVersion, elapsed, phase, mergeState } from "./installlogic.js";

const view = { boot: {}, state: null, paths: {}, choice: "", step: 0, initialized: false,
  downgrade: false, busy: false, confirm: false, error: null, rowErrors: {}, logOpen: false };
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

export function receive(state) {
  if (!state) return;
  state = mergeState(view.state, state);
  const previous = view.state?.job;
  view.state = state;
  if (!view.choice) view.choice = state.suggested || "";
  if (!view.initialized && state.target && state.installed) {
    view.step = 1;
    view.initialized = true;
  }
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
    view.busy = true;
    render();
    await refresh();
    view.busy = false;
    render();
  }
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
  if (!canStart(view.state, view.choice, view.downgrade, view.busy)) return;
  view.busy = true;
  view.confirm = false;
  view.error = null;
  render();
  try {
    receive(await api("install/start", { choice: view.choice, downgrade: view.downgrade }));
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
  return el("details", { open: true, class: "choice-steps" }, [el("summary", {}, t("Show steps")),
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
  ]));
  parts.push(el("ul", { class: "layer-rows" }, (state?.layers || []).map((row) => el("li", {
    class: "layer-row" + (state.suggested === row.choice ? " suggested-choice" : ""),
  }, [el("div", {}, [el("strong", {}, name(row.layer)),
    el("p", { class: "layer-version" }, row.installed ? displayVersion(row.layer, row.installed) : t("Not installed"))]),
    row.action === "guide" ? guide() : el("div", {}, [
      el("div", { class: "actions" }, [choiceButton(row.choice, row.actionName?.[lang()] || row.actionName?.en || (row.action === "install" ? t("Install") : t("Update / reinstall")), row.enabled), suggested(row.choice)]),
      row.reason ? el("p", { class: "note" }, problemText(t, row.reason)) : "",
    ]),
  ]))));
  for (const c of state?.choices || []) {
    if (!c.name) continue;
    parts.push(el("div", { class: "full-choice" }, [
      choiceButton(c.id, c.name[lang()] || c.name.en, canChoose(state, c.id)),
      c.reason ? el("p", { class: "note" }, c.reason[lang()] || c.reason.en) :
        c.problem ? el("p", { class: "note" }, problemText(t, c.problem)) : "",
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
  const parts = [el("h3", {}, name(layer)), el("p", { class: "note" }, t("Accepted files: {types}", { types: need.fileTypes.join(" / ") }))];
  if (pkg) {
    parts.push(el("strong", {}, pkg.name), el("p", {}, t("{layer} · {version} · {size} bytes", { layer: name(layer), version: pkg.version, size: pkg.size })),
      el("code", { title: pkg.sha256 }, "SHA-256 " + pkg.sha256.slice(0, 12) + "…"),
      el("p", { class: "check ok" }, t("Checks passed")),
      el("p", { class: need.comparison === "downgrade" ? "check bad" : "check" }, t(COMPARISON_TEXT[need.comparison])),
      view.state.installed?.layers[layer] ? el("p", { class: "note" }, t("Installed: {version}", { version: displayVersion(layer, view.state.installed.layers[layer]) })) : "");
  } else parts.push(el("p", { class: "needed" }, t("Needed")));
  if (need.problem && !(need.problem.code === "downgrade" && view.downgrade)) parts.push(el("p", { class: "error-box" }, problemText(t, need.problem)));
  if (view.rowErrors[layer]) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.rowErrors[layer])));
  if (view.boot.web) {
    const id = "install-path-" + layer;
    parts.push(el("label", { for: id }, t("Package path on this computer")), el("input", {
      id, type: "text", value: view.paths[layer] || "", disabled, spellcheck: "false", autocomplete: "off", autocapitalize: "off",
      oninput: (event) => { view.paths[layer] = event.target.value; },
      onkeydown: (event) => { if (event.key === "Enter" && view.paths[layer]?.trim()) { event.preventDefault(); add(layer); } },
    }));
  }
  parts.push(el("div", { class: "actions" }, [
    el("button", { id: "pick-" + layer, type: "button", disabled, onclick: () => add(layer) }, t("Choose file…")),
    pkg ? el("button", { id: "remove-" + layer, type: "button", disabled, "aria-label": t("Remove {name}", { name: pkg.name }),
      onclick: () => changePackage(layer, () => api("install/packages/" + pkg.id, {}, "DELETE")) }, t("Remove")) : "",
  ]));
  return el("li", { class: "package-row", "data-package-layer": layer }, parts);
}

function prepareView() {
  const selected = selectedChoice(view.state, view.choice);
  return [title(t("Prepare packages")), freshness(view.state?.installedStale),
    el("ul", { class: "needed-packages" }, neededRows(view.state, view.choice).map(packageRow)),
    selected?.downgrade ? el("div", { class: "warning" }, [el("p", {}, t("Warning: this installs an older version.")), el("label", {}, [
      el("input", { id: "install-downgrade", type: "checkbox", checked: view.downgrade, disabled: view.busy,
        onchange: (event) => { view.downgrade = event.target.checked; render(); } }), t("Allow downgrade"),
    ])]) : "",
    el("div", { class: "actions" }, [back(1), next(3, canContinue(view.state, view.choice, view.downgrade))])];
}

function backupView() {
  const skip = canSkipBackup(selectedChoice(view.state, view.choice));
  return [title(t("Back up")), el("p", { class: skip ? "tag" : "warning" }, skip ? t("Skipped") : t("A verified backup is required before flashing these layers.")),
    skip ? el("p", {}, t("Not needed: these layers do not erase the device.")) : "",
    el("div", { class: "actions" }, [back(2), next(4, skip && canContinue(view.state, view.choice, view.downgrade))])];
}

function confirmation(selected) {
  const has = (layer) => selected.layers.includes(layer);
  const device = view.state.target.device;
  return el("div", { class: "confirm", role: "alertdialog", "aria-labelledby": "install-confirm-title", "aria-describedby": "install-confirm-warning" }, [
    el("strong", { id: "install-confirm-title" }, t("Start this installation?")),
    el("p", { id: "install-confirm-warning", class: "warning flash-warning", role: "alert" }, t(confirmationWarning(selected))),
    el("p", {}, t("Target: {device}", { device: view.state.target.profile + " · " + (device.usb.serial || device.usb.path?.join(".") || "") })),
    el("ul", {}, selected.needs.filter((n) => n.package).map((n) => el("li", {}, name(n.layer) + ": " + n.package.version))),
    has("device") ? el("p", {}, t("Bluetooth restarts and the device reboots once for the device layer.")) : "",
    has("relay") ? el("p", {}, t("The relay restarts.")) : "",
    el("p", {}, t("CarPlay stops for a while. Do not install while driving.")),
    el("div", { class: "actions" }, [el("button", { id: "install-confirm", class: "primary", type: "button", disabled: !canStart(view.state, view.choice, view.downgrade, view.busy), onclick: start }, t("Install now")),
      el("button", { id: "install-cancel", type: "button", onclick: () => { view.confirm = false; render(); focus("install-start"); } }, t("Cancel"))]),
  ]);
}

function flashView() {
  if (running()) return [jobView(view.state.job)];
  const selected = selectedChoice(view.state, view.choice);
  return [title(t("Flash")), selected ? stepList(selected) : "",
    view.confirm && selected ? confirmation(selected) : el("div", { class: "actions" }, [back(2),
      el("button", { id: "install-start", type: "button", class: "primary", disabled: !canStart(view.state, view.choice, view.downgrade, view.busy),
        onclick: () => { view.confirm = true; render(); focus("install-cancel"); } }, t("Start"))])];
}

function jobView(job) {
  const active = job.steps.find((s) => s.status === "running");
  const parts = [el("h2", { id: "install-job-title", tabindex: "-1" }, t("Installation progress")),
    job.status === "running" ? el("p", { role: "status" }, active ? t("Installing {layer}: {step}", { layer: name(active.layer), step: t(ACTION_TEXT[active.action]) }) : t("Starting installation…")) : "",
    el("p", { id: "install-elapsed" }, t("Elapsed: {time}", { time: elapsed(job) }))];
  if (job.status === "succeeded") {
    parts.push(el("div", { id: "install-result", tabindex: "-1", class: "result ok", role: "status" }, [el("strong", {}, t("Installation succeeded.")),
      el("p", { class: "unplug-status" }, t(unplugText(job))), installed(job.installed, !job.observed || view.state.installedStale),
      el("p", {}, t("Next: open the relay admin page on port 6975. USB forwarding will be added later."))]));
  } else if (job.status === "failed") {
    const step = job.steps[job.failed];
    parts.push(el("div", { id: "install-result", tabindex: "-1", class: "error-box", role: "alert" }, [el("strong", {}, t("Installation failed.")),
      el("p", { class: "unplug-status" }, t(unplugText(job))),
      step ? el("p", {}, t("Failed step: {layer} — {step}", { layer: name(step.layer), step: stepLabel(step) })) : "",
      el("p", {}, problemText(t, job.problem)), el("p", {}, t(LEFT_TEXT[job.left] || LEFT_TEXT.partial)),
      installed(job.installed, !job.observed || view.state.installedStale),
    ]));
  }
  parts.push(el("ol", { class: "job-steps" }, job.steps.map((s) => el("li", { class: s.status, "aria-current": s.status === "running" ? "step" : false }, [
    name(s.layer) + ": " + stepLabel(s), el("span", {}, t(STATUS_TEXT[s.status])),
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

export function render() {
  if (!root) return;
  const focused = root.contains(document.activeElement) ? document.activeElement.id : "";
  const input = document.activeElement;
  const selection = focused.startsWith("install-path-") ? [input.selectionStart, input.selectionEnd] : null;
  const log = document.getElementById("install-log-text");
  const logScroll = log?.scrollTop || 0;
  const atEnd = !log || log.scrollHeight - log.scrollTop - log.clientHeight < 20;
  const step = phase(view.state, view.step);
  const banner = document.getElementById("install-running-warning");
  banner.hidden = !running();
  banner.textContent = t(WARNING_TEXT.running);
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

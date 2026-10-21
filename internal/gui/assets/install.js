// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { t } from "./i18n.js";
import { api, el } from "./ui.js";
import { ACTION_TEXT, CHOICE_TEXT, LAYER_TEXT, STATUS_TEXT, LEFT_TEXT, problemText, canStart, displayVersion, elapsed, phase, mergeState } from "./installlogic.js";

const view = { boot: {}, state: null, path: "", choice: "full", downgrade: false, busy: false, confirm: false, error: null, logOpen: false, plansOpen: {} };
let root, onRunning, refreshing = false;

export function init(boot, runningChanged) {
  view.boot = boot;
  root = document.getElementById("install");
  onRunning = runningChanged;
  setInterval(() => {
    const label = document.getElementById("install-elapsed");
    if (label && view.state && view.state.job) label.textContent = t("Elapsed: {time}", { time: elapsed(view.state.job) });
  }, 1000);
}

export function running() { return !!(view.state && view.state.job && view.state.job.status === "running"); }

export function receive(state) {
  if (!state) return;
  state = mergeState(view.state, state);
  const previous = view.state && view.state.job && view.state.job.status;
  view.state = state;
  if (running()) view.confirm = false;
  if (onRunning) onRunning(running());
  render();
  const current = state.job && state.job.status;
  if (current && current !== previous) {
    const target = document.getElementById(current === "running" ? "install-job-title" : "install-result");
    if (target) target.focus();
  }
}

export async function refresh() {
  if (refreshing || running()) return;
  refreshing = true;
  try { receive(await api("install")); }
  catch (err) { view.error = err; render(); }
  refreshing = false;
}

async function changePackages(action) {
  view.busy = true;
  view.confirm = false;
  view.error = null;
  render();
  try { receive(await action()); await refresh(); }
  catch (err) { view.error = err; await refresh(); }
  view.busy = false;
  render();
}

function add() {
  return changePackages(() => view.boot.web
    ? api("install/packages", { path: view.path.trim() }) : api("install/pick", {}));
}

async function start() {
  view.busy = true;
  view.confirm = false;
  view.error = null;
  render();
  try {
    await api("install/start", { choice: view.choice, downgrade: view.downgrade });
    // Read current state: a very short job can finish before the start response arrives.
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

function installed(facts) {
  if (!facts) return el("p", { class: "note" }, t("Installed layers are not available."));
  return el("dl", { class: "installed" }, ["base", "device", "relay"].flatMap((id) => [
    el("dt", {}, t(LAYER_TEXT[id])),
    el("dd", {}, facts.layers[id] ? displayVersion(id, facts.layers[id]) : t("Not installed")),
  ]));
}

function packages() {
  const state = view.state;
  const disabled = running() || view.busy;
  const parts = [el("h3", {}, t("Local packages")), el("p", { class: "note" }, t("Choose a device .tar.gz or a relay .deb / .manifest.json pair. Adding another version replaces that layer's package."))];
  if (state && state.packages.length) {
    parts.push(el("ul", { class: "package-list" }, state.packages.map((p) => el("li", {}, [
      el("div", {}, [el("strong", {}, p.name), el("p", {}, t("{layer} · {version} · {size} bytes", { layer: t(LAYER_TEXT[p.layer]), version: p.version, size: p.size })),
        el("code", { title: p.sha256 }, "SHA-256 " + p.sha256.slice(0, 12) + "…"), " · ", el("span", { class: "check ok" }, t("Checks passed"))]),
      el("button", { id: "remove-" + p.id, type: "button", disabled, "aria-label": t("Remove {name}", { name: p.name }), onclick: () => changePackages(() => api("install/packages/" + p.id, {}, "DELETE")) }, t("Remove")),
    ]))));
  } else { parts.push(el("p", { class: "note" }, t("No packages loaded."))); }
  if (view.boot.web) {
    parts.push(el("label", { for: "install-path" }, t("Package path on this computer")), el("input", {
      id: "install-path", type: "text", value: view.path, disabled, spellcheck: "false", autocomplete: "off", autocapitalize: "off",
      oninput: (event) => { view.path = event.target.value; },
      onkeydown: (event) => { if (event.key === "Enter" && view.path.trim()) { event.preventDefault(); add(); } },
    }));
  }
  parts.push(el("div", { class: "actions" }, [
    el("button", { id: "install-add", type: "button", disabled, onclick: add }, t("Add…")),
    el("button", { id: "install-refresh", type: "button", disabled, onclick: refresh }, t("Refresh installed layers")),
  ]));
  return el("div", { class: "install-packages" }, parts);
}

function choices() {
  const disabled = running() || view.busy;
  const parts = [el("legend", {}, t("Choose layers"))];
  for (const plan of (view.state && view.state.choices) || []) {
    const steps = el("details", {}, [el("summary", { id: "steps-" + plan.choice }, t("Show steps")),
      el("ol", {}, plan.steps.map((s) => el("li", {}, t(LAYER_TEXT[s.layer]) + ": " + t(ACTION_TEXT[s.action]))))]);
    steps.open = view.plansOpen[plan.choice] !== undefined ? view.plansOpen[plan.choice] : plan.choice === view.choice;
    steps.addEventListener("toggle", () => { view.plansOpen[plan.choice] = steps.open; });
    parts.push(el("div", { class: "install-choice" }, [
      el("label", {}, [el("input", { id: "choice-" + plan.choice, type: "radio", name: "install-choice", value: plan.choice, checked: plan.choice === view.choice, disabled: disabled || !!plan.problem,
        onchange: () => { view.choice = plan.choice; view.plansOpen[plan.choice] = true; view.confirm = false; view.downgrade = false; render(); } }), t(CHOICE_TEXT[plan.choice])]),
      plan.problem ? el("p", { class: "note" }, problemText(t, plan.problem)) : steps,
    ]));
  }
  const selected = view.state && view.state.choices.find((p) => p.choice === view.choice);
  if (selected && selected.downgrade) {
    parts.push(el("div", { class: "confirm" }, [el("p", {}, t("Warning: this installs an older relay version.")), el("label", {}, [
      el("input", { id: "install-downgrade", type: "checkbox", checked: view.downgrade, disabled,
        onchange: (event) => { view.downgrade = event.target.checked; view.confirm = false; render(); } }), t("Allow relay downgrade"),
    ])]));
  }
  if (view.confirm && selected) {
    const has = (layer) => selected.steps.some((s) => s.layer === layer && s.action === "install");
    parts.push(el("div", { class: "confirm", role: "alertdialog", "aria-labelledby": "install-confirm-title", "aria-describedby": "install-confirm-text" }, [
      el("strong", { id: "install-confirm-title" }, t("Start this installation?")),
      el("div", { id: "install-confirm-text" }, [
        has("device") ? el("p", {}, t("Bluetooth restarts and the device reboots once for the device layer.")) : "",
        has("relay") ? el("p", {}, t("The relay restarts.")) : "",
        el("p", {}, t("CarPlay stops for a while. Do not install while driving. Keep the USB cable connected.")),
      ]),
      el("div", { class: "actions" }, [el("button", { id: "install-confirm", class: "primary", type: "button", disabled: !canStart(view.state, view.choice, view.downgrade, view.busy), onclick: start }, t("Install now")),
        el("button", { id: "install-cancel", type: "button", onclick: () => { view.confirm = false; render(); document.getElementById("install-start").focus(); } }, t("Cancel"))]),
    ]));
  }
  parts.push(el("button", { id: "install-start", type: "button", class: "primary", disabled: !canStart(view.state, view.choice, view.downgrade, view.busy),
    onclick: () => { view.confirm = true; render(); document.getElementById("install-confirm").focus(); } }, t("Start")));
  return el("fieldset", { class: "install-choices" }, parts);
}

function jobView(job) {
  const active = job.steps.find((s) => s.status === "running");
  const parts = [el("h3", { id: "install-job-title", tabindex: "-1" }, t("Installation progress")),
    job.status === "running" ? el("p", { role: "status", "aria-live": "polite" }, active ? t("Installing {layer}: {step}", { layer: t(LAYER_TEXT[active.layer]), step: t(ACTION_TEXT[active.action]) }) : t("Starting installation…")) : "",
    el("p", { id: "install-elapsed" }, t("Elapsed: {time}", { time: elapsed(job) })),
    el("ol", { class: "job-steps" }, job.steps.map((s) => el("li", { class: s.status, "aria-current": s.status === "running" ? "step" : false }, [
      t(LAYER_TEXT[s.layer]) + ": " + t(ACTION_TEXT[s.action]), el("span", {}, t(STATUS_TEXT[s.status])),
    ])))];
  if (job.status === "succeeded") {
    parts.push(el("div", { id: "install-result", tabindex: "-1", class: "result ok", role: "status" }, [el("strong", {}, t("Installation succeeded.")), installed(job.installed),
      el("p", {}, t("Next: open the relay admin page on port 6975. USB forwarding will be added later."))]));
  } else if (job.status === "failed") {
    const step = job.steps[job.failed];
    parts.push(el("div", { id: "install-result", tabindex: "-1", class: "error-box", role: "alert" }, [
      el("strong", {}, t("Installation failed.")),
      step ? el("p", {}, t("Failed step: {layer} — {step}", { layer: t(LAYER_TEXT[step.layer]), step: t(ACTION_TEXT[step.action]) })) : "",
      el("p", {}, problemText(t, job.problem)), el("p", {}, t(LEFT_TEXT[job.left] || LEFT_TEXT.partial)),
      !job.observed ? el("p", {}, t("The device could not be read after the failure; versions below are the last observation.")) : "",
      installed(job.installed),
    ]));
  }
  const details = el("details", { id: "install-log" }, [el("summary", { id: "install-log-summary" }, t("Session log")),
    job.logCapped ? el("p", { class: "note" }, t("The log reached its limit; only the most recent 1 MiB is kept.")) : "",
    el("pre", { id: "install-log-text", tabindex: "0", "aria-label": t("Session log") }, job.log)]);
  details.open = view.logOpen;
  details.addEventListener("toggle", () => { view.logOpen = details.open; });
  parts.push(details, el("button", { id: "install-save-log", type: "button", onclick: saveLog }, t("Save log…")));
  return el("div", { class: "install-job" }, parts);
}

export function render() {
  if (!root) return;
  const focused = root.contains(document.activeElement) ? document.activeElement.id : "";
  const input = document.getElementById("install-path");
  const selection = focused === "install-path" && input ? [input.selectionStart, input.selectionEnd] : null;
  const log = document.getElementById("install-log-text");
  const logScroll = log ? log.scrollTop : 0;
  const atEnd = !log || log.scrollHeight - log.scrollTop - log.clientHeight < 20;
  const parts = [el("h2", {}, t("Install local layers"))];
  const state = view.state;
  if (state && state.problem) parts.push(el("p", { class: "error-box", role: "status" }, problemText(t, state.problem)));
  if (state && state.installed) {
    parts.push(el("h3", {}, t("Installed layers")), installed(state.installed),
      el("p", { class: "note" }, t("Debian {version} · {arch} · root: {root}", { version: state.installed.debianVersion, arch: state.installed.dpkgArchitecture, root: state.installed.root ? t("Yes") : t("No") })),
      el("p", { class: "note" }, t("Free space: {space}", { space: Object.entries(state.installed.freeKiB).map(([dir, n]) => dir + " " + n + " KiB").join(" · ") })));
  }
  parts.push(packages(), el("p", { class: "note" }, t("Back up is not needed for the device and relay layers.")), choices());
  if (view.error) parts.push(el("p", { class: "error-box", role: "alert" }, problemText(t, view.error)));
  if (state && state.job) parts.push(jobView(state.job));
  root.replaceChildren(...parts);
  if (focused) { const again = document.getElementById(focused); if (again && !again.disabled) again.focus(); }
  const freshInput = document.getElementById("install-path");
  if (selection && freshInput) freshInput.setSelectionRange(...selection);
  const freshLog = document.getElementById("install-log-text");
  if (freshLog) freshLog.scrollTop = atEnd ? freshLog.scrollHeight : logScroll;
  document.querySelectorAll(".steps li").forEach((li, index) => {
    const current = index === phase(state, view.confirm);
    li.classList.toggle("current", current);
    if (current) li.setAttribute("aria-current", "step"); else li.removeAttribute("aria-current");
  });
}

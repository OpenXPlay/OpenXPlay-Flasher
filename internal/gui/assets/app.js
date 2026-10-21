// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// The page talks to Go only through /api (JSON over the app's own handler) and polls for state, so
// it runs the same in the app window, in a browser (openxplay-flasher web) and in tests.

import { t, setLang, lang, translateStatic } from "./i18n.js";
import { deviceView, modelView, modeLabel, usbId, usbPort } from "./devices.js";
import { api, el } from "./ui.js";
import { problemText } from "./installlogic.js";
import * as configView from "./config.js";
import * as installView from "./install.js";
import * as settingsView from "./settings.js";
import { effectiveLanguage, linksFor, viewFromHash, viewAllowed } from "./settingslogic.js";

const boot = window.bootPrefs || { lang: "en", systemLang: "en", saved: "", links: {}, web: true };
const state = { info: null, infoError: false, profiles: [], devices: null, rev: 0, offline: false, othersOpen: false,
  view: "install", savedLang: boot.saved || "", languageBusy: false, languageError: false, designation: "", designationBusy: false };

function status(kind, title, text) {
  return el("div", { class: "status " + kind }, [
    el("span", { class: "dot" }),
    el("div", {}, [el("strong", {}, t(title)), text ? el("p", {}, t(text)) : ""]),
  ]);
}

function deviceCard(c) {
  const model = modelView(c, state.profiles, lang());
  const rows = [
    el("dt", {}, t("Model")),
    el("dd", {}, model.sure ? model.names[0] : t("{models} (to be confirmed)", { models: model.names.join(" / ") })),
    el("dt", {}, t("Mode")), el("dd", {}, t(modeLabel(c.mode))),
    el("dt", {}, t("USB ID")), el("dd", {}, usbId(c.usb)),
    el("dt", {}, t("Port")), el("dd", {}, usbPort(c.usb)),
  ];
  if (c.product) rows.push(el("dt", {}, t("Product")), el("dd", {}, c.product));
  if (c.serialno) rows.push(el("dt", {}, t("Board serial")), el("dd", {}, c.serialno));
  if (model.layers.length) {
    rows.push(el("dt", {}, t("Layers")), el("dd", {}, el("ol", {}, model.layers.map((l) =>
      el("li", {}, [l.name, l.destructive ? el("span", { class: "tag destructive-tag" }, t("erases the device")) : ""])))));
  }
  const serial = c.serialno || c.usb.serial;
  const install = installView.snapshot();
  const unfinished = install?.resume && !install.resume.finished && install.resume.serial === serial;
  return el("article", { class: "connected-device" }, [
    c.mode === "fastboot" ? el("strong", {}, t("Bootloader (fastboot) · {product} · {serial}", { product: c.product || "—", serial: serial || "—" })) : "",
    el("dl", { class: "device-card" }, rows),
    unfinished ? el("div", { class: "warning" }, [el("p", {}, t("An earlier installation did not finish")),
      el("button", { type: "button", onclick: () => installView.selectRepair() }, t("Repair"))]) : "",
  ]);
}

// renderDevices rebuilds the card. The disclosure keeps its open state and the focused control
// keeps focus, since a refresh can come at any moment while the user is on the card.
function renderDevices() {
  const root = document.getElementById("devices");
  const focused = root.contains(document.activeElement) ? document.activeElement.dataset.focus : "";
  const view = deviceView(state.devices);
  const parts = [];
  if (state.offline) parts.push(el("div", { class: "error-box" }, t("Lost contact with the flasher. Retrying…")));
  switch (view.status) {
    case "scanning":
      parts.push(status("wait", "Looking for USB devices…"));
      break;
    case "error":
      parts.push(status("error", "Cannot read USB devices"), el("div", { class: "error-box" }, view.error));
      break;
    case "none":
      parts.push(status("wait", "No supported device found",
        "Plug the device into a USB port directly on this computer, not through a hub without its own power."));
      break;
    case "one":
      parts.push(status("ok", "Device found",
        view.supported[0].mode === "fastboot" ? t("The bootloader product and serial are checked before flashing.") :
          view.supported[0].mode === "stock" ? t("Original firmware confirmed. Continue to choose the installation.") : t("The model and installed layers are checked over adb before installation.")),
        deviceCard(view.supported[0]));
      break;
    case "many":
      parts.push(status("warn", "Several supported devices found", "Connect only one device at a time; unplug the others."),
        ...view.supported.map(deviceCard));
      break;
  }
  const ignoredADB = view.others.filter((c) => c.mode === "adb");
  if (ignoredADB.length) {
    const summary = ignoredADB.length === 1
      ? t("1 other Android device is connected and ignored.")
      : t("{n} other Android devices are connected and ignored.", { n: ignoredADB.length });
    const details = el("details", { class: "others" }, [
      el("summary", { "data-focus": "others" }, summary),
      el("p", {}, t("Unknown adb devices are ignored until you explicitly designate one as a UFI003 with its original firmware.")),
      el("ul", {}, ignoredADB.map((c) => el("li", {}, [usbId(c.usb) + " · " + usbPort(c.usb),
        c.mode === "adb" ? el("button", { type: "button", disabled: state.designationBusy,
          onclick: () => { state.designation = usbPort(c.usb); state.designationError = ""; renderDevices(); } }, t("This is a UFI003 with its original firmware")) : "",
        state.designation === usbPort(c.usb) ? el("div", { class: "designation-confirm" }, [
          el("p", {}, t("The app will read this device's model over adb to confirm that it is a UFI003 with its original firmware.")),
          el("button", { type: "button", disabled: state.designationBusy, onclick: async () => {
            state.designationBusy = true; renderDevices();
            try {
              installView.receive(await api("devices/designate-stock", { location: usbPort(c.usb) }));
              state.designationError = ""; state.designation = "";
            } catch (error) { state.designationError = problemText(t, error); }
            state.designationBusy = false; renderDevices();
          } }, t("Confirm model")),
          el("button", { type: "button", disabled: state.designationBusy, onclick: () => { state.designation = ""; renderDevices(); } }, t("Cancel")),
        ]) : "",
      ]))),
      state.designationError ? el("p", { class: "error-box" }, state.designationError) : "",
    ]);
    details.open = state.othersOpen;
    details.addEventListener("toggle", () => { state.othersOpen = details.open; });
    parts.push(details);
  }
  root.replaceChildren(...parts);
  if (focused) {
    const again = root.querySelector('[data-focus="' + focused + '"]');
    if (again) again.focus();
  }
}

function renderFooter() {
  const links = linksFor(boot.links, lang());
  for (const link of document.querySelectorAll("[data-help]")) link.href = links[link.dataset.help];
  if (!state.info) return;
  document.getElementById("version").textContent = t("Version {v}", { v: state.info.version });
  document.getElementById("license").textContent = t("License: {l}", { l: state.info.license });
}

function renderSettings() {
  settingsView.render({ info: state.info, infoError: state.infoError, links: boot.links,
    savedLang: state.savedLang, languageBusy: state.languageBusy, languageError: state.languageError,
    onLanguageChange: chooseLanguage });
}

async function loadInfo() {
  try {
    state.info = await api("info");
    state.infoError = false;
  } catch (_) {
    state.infoError = true;
  }
  renderFooter();
  renderSettings();
}

async function chooseLanguage(choice) {
  if (state.languageBusy) return;
  state.languageBusy = true;
  state.languageError = false;
  render();
  try {
    const saved = await api("settings", { lang: choice });
    state.savedLang = saved.lang;
    setLang(effectiveLanguage(saved.lang, boot.systemLang));
  } catch (_) {
    state.languageError = true;
  }
  state.languageBusy = false;
  render();
}

function render() {
  document.documentElement.lang = lang();
  document.title = t("OpenXPlay Flasher");
  translateStatic(document);
  for (const b of document.querySelectorAll("[data-lang]")) {
    b.setAttribute("aria-pressed", String(b.dataset.lang === state.savedLang));
    b.disabled = state.languageBusy;
  }
  renderDevices();
  renderFooter();
  configView.render();
  installView.render();
  renderSettings();
}

function showView(name) {
  if (!viewAllowed(name, installView.running())) {
    history.replaceState(null, "", "#" + state.view);
    return;
  }
  state.view = name;
  history.replaceState(null, "", "#" + name);
  for (const tab of document.querySelectorAll("[data-view]")) {
    tab.setAttribute("aria-selected", String(tab.dataset.view === name));
  }
  document.getElementById("view-install").hidden = name !== "install";
  document.getElementById("view-config").hidden = name !== "config";
  document.getElementById("view-settings").hidden = name !== "settings";
  if (name === "config") configView.refresh();
  if (name === "install") installView.refresh();
  if (name === "settings") loadInfo();
}

window.addEventListener("hashchange", () => showView(viewFromHash(window.location.hash)));

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// follow long-polls /api/state: each answer comes when the revision passes the last one seen, or
// after the server's timeout with the same state.
async function follow() {
  for (;;) {
    try {
      const next = await api("state?after=" + state.rev);
      const changed = next.rev !== state.rev || state.offline;
      const devicesChanged = JSON.stringify(next.devices) !== JSON.stringify(state.devices);
      state.rev = next.rev;
      if (next.rev > 0) state.devices = next.devices;
      state.offline = false;
      if (changed) {
        if (next.install) installView.receive(next.install);
        renderDevices();
        if (devicesChanged && state.view === "config") configView.refresh();
        if (devicesChanged && state.view === "install") installView.refresh();
      }
    } catch (err) {
      if (!state.offline) {
        state.offline = true;
        renderDevices();
      }
      await sleep(1000);
    }
  }
}

function openLink(url) {
  api("open", { url }).catch((err) => console.error(err));
}

// WKWebView follows macOS's Keyboard navigation setting, off by default, under which Tab skips
// buttons and links. The page moves focus itself so Tab reaches every control on every platform.
function focusables() {
  return [...document.querySelectorAll("button, a[href], summary, input, select, textarea, [tabindex]")]
    .filter((e) => !e.disabled && e.tabIndex >= 0 && e.offsetParent !== null);
}

document.addEventListener("keydown", (event) => {
  if (event.key !== "Tab" || event.altKey || event.ctrlKey || event.metaKey) return;
  const list = focusables();
  if (!list.length) return;
  const i = list.indexOf(document.activeElement);
  const next = event.shiftKey ? (i <= 0 ? list.length - 1 : i - 1) : (i + 1) % list.length;
  event.preventDefault();
  list[next].focus();
});

document.addEventListener("click", (event) => {
  const tab = event.target.closest("[data-view]");
  if (tab) {
    showView(tab.dataset.view);
    return;
  }
  const langButton = event.target.closest("[data-lang]");
  if (langButton) {
    chooseLanguage(langButton.dataset.lang);
    return;
  }
  const link = event.target.closest("a[data-open]");
  if (link) {
    event.preventDefault();
    openLink(link.href);
  }
});

setLang(boot.lang);
configView.init(boot);
settingsView.init();
installView.init(boot, (running) => {
  document.querySelectorAll("[data-view]").forEach((tab) => { tab.disabled = !viewAllowed(tab.dataset.view, running); });
});
render();
if (window.location.hash) showView(viewFromHash(window.location.hash));
if (state.view !== "settings") loadInfo();
api("profiles").then((profiles) => { state.profiles = profiles || []; renderDevices(); }).catch((err) => console.error(err));
follow();

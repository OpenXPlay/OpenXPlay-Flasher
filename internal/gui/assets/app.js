// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// The page talks to Go only through /api (JSON over the app's own handler) and polls for state, so
// it runs the same in the app window, in a browser (openxplay-flasher web) and in tests.

import { t, setLang, lang, translateStatic } from "./i18n.js";
import { deviceView, modelView, modeLabel, usbId, usbPort } from "./devices.js";

const boot = window.bootPrefs || { lang: "en", web: true };
const state = { info: null, profiles: [], devices: null, rev: 0, offline: false, othersOpen: false };

async function api(path, body) {
  const res = await fetch("/api/" + path, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

function el(tag, attrs, children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (key === "class") node.className = value;
    else node.setAttribute(key, value);
  }
  for (const child of [].concat(children || [])) {
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

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
  if (model.layers.length) {
    rows.push(el("dt", {}, t("Layers")), el("dd", {}, el("ol", {}, model.layers.map((l) =>
      el("li", {}, [l.name, l.destructive ? el("span", { class: "tag" }, t("erases the device")) : ""])))));
  }
  return el("dl", { class: "device-card" }, rows);
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
        "Confirming the model and reading its installed layers over adb comes in the next version."),
        deviceCard(view.supported[0]));
      break;
    case "many":
      parts.push(status("warn", "Several supported devices found", "Connect only one device at a time; unplug the others."),
        ...view.supported.map(deviceCard));
      break;
  }
  if (view.others.length) {
    const summary = view.others.length === 1
      ? t("1 other Android device is connected and ignored.")
      : t("{n} other Android devices are connected and ignored.", { n: view.others.length });
    const details = el("details", { class: "others" }, [
      el("summary", { "data-focus": "others" }, summary),
      el("p", {}, t("A supported device running its original firmware may be among them; it is recognised once its USB IDs are added to its profile.")),
      el("ul", {}, view.others.map((c) => el("li", {}, usbId(c.usb) + " · " + t(modeLabel(c.mode)) + " · " + usbPort(c.usb)))),
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
  if (!state.info) return;
  document.getElementById("version").textContent = t("Version {v}", { v: state.info.version });
  document.getElementById("license").textContent = t("License: {l}", { l: state.info.license });
}

function render() {
  document.documentElement.lang = lang();
  document.title = t("OpenXPlay Flasher");
  translateStatic(document);
  for (const b of document.querySelectorAll("[data-lang]")) {
    b.setAttribute("aria-pressed", String(b.dataset.lang === lang()));
  }
  renderDevices();
  renderFooter();
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// follow long-polls /api/state: each answer comes when the revision passes the last one seen, or
// after the server's timeout with the same state.
async function follow() {
  for (;;) {
    try {
      const next = await api("state?after=" + state.rev);
      const changed = next.rev !== state.rev || state.offline;
      state.rev = next.rev;
      if (next.rev > 0) state.devices = next.devices;
      state.offline = false;
      if (changed) renderDevices();
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
  if (boot.web) {
    window.open(url, "_blank", "noopener");
    return;
  }
  api("open", { url }).catch((err) => console.error(err));
}

document.addEventListener("click", (event) => {
  const langButton = event.target.closest("[data-lang]");
  if (langButton) {
    setLang(langButton.dataset.lang);
    render();
    api("settings", { lang: lang() }).catch((err) => console.error(err));
    return;
  }
  const link = event.target.closest("a[data-open]");
  if (link) {
    event.preventDefault();
    openLink(link.href);
  }
});

setLang(boot.lang);
render();
api("info").then((info) => { state.info = info; renderFooter(); }).catch((err) => console.error(err));
api("profiles").then((profiles) => { state.profiles = profiles || []; renderDevices(); }).catch((err) => console.error(err));
follow();

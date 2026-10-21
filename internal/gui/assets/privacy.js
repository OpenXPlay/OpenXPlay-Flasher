// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// This view holds only typed input and the redacted backend view, never saved credentials.
import { t, lang } from "./i18n.js";
import { api, el } from "./ui.js";
import { errorText } from "./configlogic.js";

let entry = null;
let state = null;
let draft = null;
let busy = false;
let result = null;
let errors = {};
let backups = null;
let confirm = "";
let redraw = () => {};
let generation = 0;

export function resetPrivacy() {
  generation++;
  entry = state = draft = result = backups = null;
  errors = {};
  confirm = "";
  busy = false;
}

function accept(value) {
  state = value;
  draft = { enabled: value.enabled, provider: value.provider, name: value.name, token: "", agreeLetsEncryptTerms: value.agreeLetsEncryptTerms };
  errors = value.errors || {};
}

async function operation(action, body = {}) {
  const ticket = generation;
  const id = entry.id;
  busy = true;
  result = null;
  redraw();
  try {
    const value = await api("config/privacy/" + action, { id, ...body });
    if (ticket !== generation) return;
    if (action === "backups") backups = value;
    else if (value) {
      accept(value);
      if (action !== "read") result = { kind: "ok", text: action === "remove"
        ? t("Removed. The relay returns to its self-signed certificate within 10 seconds.")
        : action === "restore" ? t("Restored. The relay picks it up within 10 seconds.")
          : t("Saved. The relay picks it up within 10 seconds.") };
      if (action === "restore" || action === "remove") {
        const refreshed = await api("config/privacy/backups", { id });
        if (ticket !== generation) return;
        backups = refreshed;
      }
    }
  } catch (err) {
    if (ticket !== generation) return;
    errors = err.fields || {};
    result = { kind: "error", text: err.code === "private-invalid" ? t("Check the marked fields.") : errorText(t, err) };
  } finally {
    if (ticket === generation) {
      busy = false;
      confirm = "";
      redraw();
    }
  }
}

// Submit only edits made since the last read, so concurrent admin-page changes survive.
function changedFields() {
  const patch = {};
  for (const key of ["enabled", "provider", "name", "agreeLetsEncryptTerms"]) {
    if (draft[key] !== state[key]) patch[key] = draft[key];
  }
  if (draft.token) patch.token = draft.token;
  return patch;
}

async function importFile(web) {
  if (!web) return operation("import", { native: true });
  const ticket = generation;
  const input = el("input", { type: "file", accept: ".json,application/json" });
  input.addEventListener("change", async () => {
    const file = input.files && input.files[0];
    if (!file || ticket !== generation) return;
    if (file.size > 16384) {
      errors = { file: "too-large" };
      result = { kind: "error", text: t("Check the marked fields.") };
      redraw();
      return;
    }
    let content = await file.text();
    if (ticket !== generation) { content = ""; return; }
    input.value = "";
    await operation("import", { content });
    content = "";
  });
  input.click();
}

function fieldError(key) {
  if (!errors[key]) return null;
  const messages = {
    missing: t("This field is missing."), invalid: t("This field is invalid."),
    "unknown-key": t("The file contains an unknown key."), "bad-version": t("The file version must be 1."),
    "too-large": t("The privacy file is larger than 16 KiB."), "not-json": t("The file must contain one JSON object."),
  };
  return el("p", { class: "check bad", role: "alert" }, messages[errors[key]] || t("This field is invalid."));
}

function checkbox(key, label) {
  return el("label", { class: "restart" }, [el("input", {
    type: "checkbox", id: "privacy-" + key, checked: draft[key], disabled: busy,
    onchange: (event) => { draft[key] = event.target.checked; },
  }), label]);
}

export function privacyView(file, web, render) {
  redraw = render;
  if (!entry || entry.path !== file.path) {
    resetPrivacy();
    entry = file;
    // Defer until the new view is attached; the operation redraws its loading state.
    const ticket = generation;
    queueMicrotask(() => { if (ticket === generation) operation("read"); });
  }
  const parts = [el("h3", {}, t("Trusted HTTPS")),
    el("p", { class: "note" }, t("Private data stays on the device. The flasher never shows or exports it.")),
    el("p", { class: "note" }, t("Open the head-unit page without a certificate warning, using a free domain name."))];
  if (draft) {
    parts.push(checkbox("enabled", t("Use a free domain name")), fieldError("enabled"));
    const provider = el("select", { id: "privacy-provider", disabled: busy, onchange: (event) => { draft.provider = event.target.value; } },
      [["", "—"], ["duckdns", "DuckDNS"], ["dynv6", "dynv6"]].map(([value, text]) => el("option", { value, selected: value === draft.provider }, text)));
    parts.push(el("label", { for: "privacy-provider" }, t("DNS provider")), provider, fieldError("provider"));
    for (const [key, label, placeholder] of [
      ["name", t("Domain name"), t("For example myrelay.duckdns.org")],
      ["token", t("Token"), state.tokenSet ? t("Token saved") : t("Copy it from your provider account. It stays on this relay.")],
    ]) {
      parts.push(el("label", { for: "privacy-" + key }, label), el("input", {
        id: "privacy-" + key, type: key === "token" ? "password" : "text", value: draft[key], disabled: busy,
        autocomplete: "off", autocapitalize: "off", spellcheck: "false", placeholder,
        oninput: (event) => { draft[key] = event.target.value; },
      }), fieldError(key));
    }
    parts.push(checkbox("agreeLetsEncryptTerms", el("span", {}, [
      el("a", { href: "https://letsencrypt.org/repository/", "data-open": true }, t("I agree to the Let's Encrypt Subscriber Agreement")),
    ])), fieldError("agreeLetsEncryptTerms"), fieldError("file"), fieldError("version"), fieldError("acme"));
    if (state.enabled && (!state.provider || !state.name || !state.tokenSet || !state.agreeLetsEncryptTerms || Object.keys(state.errors || {}).length)) {
      parts.push(el("p", { class: "note" }, t("Fill in the provider, domain name and token, and agree to the terms.")));
    }
    parts.push(el("div", { class: "actions" }, [
      el("button", { type: "button", class: "primary", disabled: busy, onclick: () => operation("write", changedFields()) }, t("Save")),
      el("button", { type: "button", disabled: busy, onclick: () => importFile(web) }, t("Import configuration file…")),
      el("button", { type: "button", disabled: busy || !state.exists, onclick: () => { confirm = "remove"; redraw(); } }, t("Remove")),
      el("button", { type: "button", disabled: busy, onclick: () => operation("backups") }, t("Earlier versions")),
      el("button", { type: "button", disabled: busy, onclick: () => operation("read") }, t("Reload")),
    ]));
  }
  if (!draft && !busy) parts.push(el("button", { type: "button", onclick: () => operation("read") }, t("Reload")));
  if (confirm) {
    parts.push(el("div", { class: "confirm", role: "alertdialog" }, [
      el("p", {}, confirm === "remove" ? t("Remove the trusted HTTPS configuration from the device? Its earlier versions are deleted too, and this cannot be undone.") : t("Put this earlier version back on the device? The current version is kept as a backup.")),
      el("div", { class: "actions" }, [
        el("button", { type: "button", disabled: busy, onclick: () => confirm === "remove" ? operation("remove") : operation("restore", { backup: confirm }) }, confirm === "remove" ? t("Remove") : t("Restore")),
        el("button", { type: "button", disabled: busy, onclick: () => { confirm = ""; redraw(); } }, t("Cancel")),
      ]),
    ]));
  }
  if (busy) parts.push(el("p", { class: "status wait", role: "status" }, t("Working…")));
  if (result) parts.push(el("p", { class: result.kind === "ok" ? "result ok" : "error-box", role: "status" }, result.text));
  if (backups) {
    const fmt = new Intl.DateTimeFormat(lang(), { dateStyle: "medium", timeStyle: "medium" });
    parts.push(el("h3", {}, t("Earlier versions on the device")), backups.length ? el("ul", { class: "backups" }, backups.map((b) => el("li", {}, [
      el("span", {}, fmt.format(new Date(b.time))),
      el("button", { type: "button", disabled: busy, onclick: () => { confirm = b.id; redraw(); } }, t("Restore")),
    ]))) : el("p", { class: "note" }, t("No earlier versions yet.")));
  }
  return el("div", { class: "editor privacy" }, parts);
}

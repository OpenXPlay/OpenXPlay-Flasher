// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { t, lang } from "./i18n.js";
import { el } from "./ui.js";
import { aboutRows, aboutText, linksFor } from "./settingslogic.js";

let root = null;
let current = null;
let copyResult = "";
let confirmSignatureOff = false;

export function init() {
  root = document.getElementById("settings");
  confirmSignatureOff = false;
}

// The clipboard API is not available in every app webview. The selection fallback also works in
// those windows and in browsers that deny clipboard permission.
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return;
  } catch (_) {
    const focus = document.activeElement;
    const field = el("textarea", { class: "clipboard-copy", readonly: true, "aria-hidden": "true", tabindex: "-1" });
    field.value = text;
    document.body.append(field);
    field.focus();
    field.select();
    try {
      if (!document.execCommand("copy")) throw new Error("copy failed");
    } finally {
      field.remove();
      if (focus) focus.focus();
    }
  }
}

async function copyAbout() {
  try {
    await copyText(aboutText(current.info, t));
    copyResult = "ok";
  } catch (_) {
    copyResult = "error";
  }
  render(current);
}

export function render(prefs) {
  if (!root) return;
  current = prefs;
  const focused = root.contains(document.activeElement) ? document.activeElement.id : "";
  const info = prefs.info;
  const links = linksFor(prefs.links, lang());
  const rows = aboutRows(info, t);
  const about = el("section", { class: "card" }, [
    el("h2", {}, t("About")),
    info ? el("dl", { class: "about-info" }, rows.flatMap((row) => [
      el("dt", {}, row.label),
      el("dd", {}, row.href ? el("a", { href: row.href, "data-open": true }, row.value) : row.value),
    ])) : !prefs.infoError ? el("p", { class: "note" }, t("Loading app information…")) : "",
    prefs.infoError ? el("p", { class: "error-box" }, t("Could not read app information. Open Settings again to retry.")) : "",
    el("div", { class: "actions" }, [
      el("button", { type: "button", id: "copy-about", disabled: !info, onclick: copyAbout }, t("Copy")),
      el("span", { role: "status", class: copyResult === "error" ? "check bad" : "check ok" },
        copyResult === "ok" ? t("Copied.") : copyResult === "error" ? t("Could not copy. Select the information and copy it manually.") : ""),
    ]),
  ]);
  const language = el("section", { class: "card" }, [
    el("h2", {}, t("Language")),
    el("label", { for: "settings-language", class: "note" }, t("App language")),
    el("select", { id: "settings-language", disabled: prefs.languageBusy,
      onchange: (event) => prefs.onLanguageChange(event.target.value) }, [
      el("option", { value: "", selected: prefs.savedLang === "" }, t("Follow the system")),
      el("option", { value: "zh-CN", selected: prefs.savedLang === "zh-CN" }, t("中文")),
      el("option", { value: "en", selected: prefs.savedLang === "en" }, t("English")),
    ]),
    prefs.languageError ? el("p", { class: "error-box", role: "alert" }, t("Could not save the language. Please try again.")) : "",
  ]);
  const signature = el("section", { class: "card" }, [
    el("h2", {}, t("Package list")),
    el("label", { class: "signature-switch", for: "settings-signature" }, [
      el("input", { type: "checkbox", role: "switch", id: "settings-signature", checked: prefs.signatureCheck !== false,
        disabled: prefs.signatureBusy, "aria-describedby": "settings-signature-help", onchange: (event) => {
          if (!event.target.checked && current.signatureCheck !== false) {
            confirmSignatureOff = true; render(current);
            document.getElementById("signature-keep")?.focus();
          } else {
            current.onSignatureChange(event.target.checked);
          }
        } }),
      t("Verify the package list signature"),
    ]),
    el("p", { id: "settings-signature-help", class: "note" }, t("When off, the package list is used even if it is unsigned or its signature does not match; each chosen file is still checked against the SHA-256 in the list.")),
    confirmSignatureOff ? el("div", { class: "confirm", role: "alertdialog", "aria-labelledby": "signature-confirm-title" }, [
      el("p", { id: "signature-confirm-title", class: "warning" }, t("The app can then no longer tell whether the package list comes from OpenXPlay. Turn off the signature check?")),
      el("div", { class: "actions" }, [
        el("button", { type: "button", id: "signature-keep", onclick: () => { confirmSignatureOff = false; render(current); } }, t("Cancel")),
        el("button", { type: "button", id: "signature-turn-off", onclick: () => {
          confirmSignatureOff = false; current.onSignatureChange(false);
        } }, t("Turn off")),
      ]),
    ]) : "",
    prefs.signatureError ? el("p", { class: "error-box", role: "alert" }, t("Could not save the signature check setting. Please try again.")) : "",
  ]);
  const help = el("section", { class: "card" }, [
    el("h2", {}, t("Help and downloads")),
    el("div", { class: "actions" }, [
      el("a", { href: links.tutorial, "data-open": true }, t("Tutorial")),
      el("a", { href: links.download, "data-open": true }, t("Download page")),
      el("a", { href: links.source, "data-open": true }, t("Source code")),
      el("a", { href: links.issues, "data-open": true }, t("Report a problem")),
    ]),
    el("dl", { class: "about-info data-paths" }, [
      el("dt", {}, t("Settings file")), el("dd", {}, el("code", {}, (info && info.settingsFile) || t("Unknown"))),
      el("dt", {}, t("adb key folder")), el("dd", {}, el("code", {}, (info && info.adb && info.adb.keyDir) || t("Unknown"))),
    ]),
  ]);
  root.replaceChildren(about, language, signature, help);
  if (focused) document.getElementById(focused)?.focus();
}

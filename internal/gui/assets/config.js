// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// The configuration view: read a configuration file from the connected device, edit it, and
// write it back over USB. The device keeps a backup of every version it replaces, and the relay is
// restarted (and the file rolled back) by the Go side; this module only drives the API.

import { t, lang } from "./i18n.js";
import { api, el } from "./ui.js";
import { privacyView, resetPrivacy } from "./privacy.js";
import { checkContent, errorText, isPrivateImport } from "./configlogic.js";

const view = {
  boot: {},
  category: "user",
  rawBlocked: false,
  openTarget: "",
  openEntry: null,
  target: null, // GET /api/config, or null
  unavailable: "", // its error code
  loading: false,
  path: "", // the open file
  file: null, // POST /api/config/read
  text: "", // editor content
  busy: "", // "", "reading", "saving", "restoring"
  confirm: "", // "", "save", "restore:<id>", "discard:<path>"
  restart: false, // other files: restart the relay after saving
  result: null, // { kind: "ok"|"error", text, log }
  backups: null, // list, or null when hidden
  otherPath: "",
};

let root = null;

export function init(boot) {
  view.boot = boot;
  root = document.getElementById("config");
}

// refresh asks which device and files the view works on; called when the view opens and when the
// attached devices change.
export async function refresh(entered = false) {
  if (entered) resetPrivacy();
  try {
    const target = await api("config");
    if (view.target && JSON.stringify(view.target) !== JSON.stringify(target)) resetPrivacy();
    if (view.openTarget && view.openTarget !== JSON.stringify(target)) {
      resetPrivacy();
      view.rawBlocked = true;
      view.confirm = "";
      view.backups = null;
    }
    if (view.openTarget === JSON.stringify(target)) view.rawBlocked = false;
    view.target = target;
    view.unavailable = "";
  } catch (err) {
    resetPrivacy();
    view.rawBlocked = !!view.file;
    view.confirm = "";
    view.backups = null;
    view.target = null;
    view.unavailable = err.code || "device";
  }
  render();
}

function entryFor(path) {
  if (view.file && view.path === path) return view.openEntry;
  return view.target && view.target.files.find((f) => f.path === path);
}

function fileName(f) {
  return (f.name && (f.name[lang()] || f.name.en)) || f.path;
}

function dirty() {
  return view.file !== null && view.text !== view.file.content;
}

async function open(path) {
  if (dirty() && view.confirm !== "discard:" + path) {
    view.confirm = "discard:" + path;
    render();
    return;
  }
  const openEntry = view.target && view.target.files.find((f) => f.path === path);
  Object.assign(view, { openEntry, path, file: null, text: "", busy: "reading", confirm: "", result: null, backups: null, restart: false });
  render();
  try {
    view.file = await api("config/read", { path });
    view.text = view.file.content;
    view.openTarget = JSON.stringify(view.target);
    view.rawBlocked = false;
  } catch (err) {
    view.path = "";
    view.result = { kind: "error", text: errorText(t, err) };
  }
  view.busy = "";
  render();
}

function writeOutcome(res) {
  if (res.rolledBack) {
    return { kind: "error", text: t("The relay did not stay up with the new file, so the previous version was put back."), log: res.log };
  }
  if (res.restarted && !res.healthy) {
    return { kind: "error", text: t("The relay did not stay up. Check the file and the relay log."), log: res.log };
  }
  if (res.restarted) return { kind: "ok", text: t("Saved. The relay restarted and is running.") };
  return { kind: "ok", text: t("Saved.") };
}

async function save() {
  if (view.rawBlocked || !view.target) return;
  view.busy = "saving";
  view.confirm = "";
  render();
  try {
    const res = await api("config/write", { path: view.path, content: view.text, restart: view.restart });
    view.result = writeOutcome(res);
    if (!res.rolledBack) view.file = Object.assign({}, view.file, { content: view.text, exists: true });
  } catch (err) {
    view.result = { kind: "error", text: errorText(t, err) };
  }
  view.busy = "";
  view.backups = null;
  render();
}

async function showBackups() {
  try {
    view.backups = await api("config/backups", { path: view.path });
  } catch (err) {
    view.result = { kind: "error", text: errorText(t, err) };
  }
  render();
}

async function restore(id) {
  if (view.rawBlocked || !view.target) return;
  view.busy = "restoring";
  view.confirm = "";
  render();
  try {
    const res = await api("config/restore", { path: view.path, id, restart: view.restart });
    view.result = writeOutcome(res);
    view.file = await api("config/read", { path: view.path });
    view.text = view.file.content;
    view.backups = await api("config/backups", { path: view.path });
  } catch (err) {
    view.result = { kind: "error", text: errorText(t, err) };
  }
  view.busy = "";
  render();
}

async function importFile() {
  if (view.boot.web) {
    const input = el("input", { type: "file" });
    input.addEventListener("change", () => {
      const file = input.files && input.files[0];
      if (!file) return;
      file.text().then((text) => {
        if (isPrivateImport(text)) view.result = { kind: "error", text: errorText(t, { code: "privacy" }) };
        else { view.text = text; view.result = null; }
        render();
      });
    });
    input.click();
    return;
  }
  try {
    const res = await api("config/import", {});
    if (res.name) {
      view.text = res.content;
      view.result = { kind: "ok", text: t("Loaded {name}. Save to write it to the device.", { name: res.name }) };
    }
  } catch (err) {
    view.result = { kind: "error", text: errorText(t, err) };
  }
  render();
}

async function exportFile() {
  const name = view.path.split("/").pop() || "config.txt";
  if (view.boot.web) {
    const a = el("a", { href: URL.createObjectURL(new Blob([view.text], { type: "text/plain" })), download: name });
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    return;
  }
  try {
    const res = await api("config/export", { path: view.path, content: view.text });
    if (res.saved) view.result = { kind: "ok", text: t("Saved a copy to {path}.", { path: res.saved }) };
  } catch (err) {
    view.result = { kind: "error", text: errorText(t, err) };
  }
  render();
}

function fileList() {
  const items = view.target.files.filter((f) => f.category === view.category).map((f) =>
    el("li", {}, el("button", {
      type: "button",
      class: "file" + (f.path === view.path ? " current" : ""),
      "aria-current": f.path === view.path ? "true" : false,
      onclick: () => open(f.path),
    }, [el("strong", {}, fileName(f)), el("code", {}, f.path)])));
  const other = el("form", {
    class: "other",
    onsubmit: (event) => {
      event.preventDefault();
      if (view.otherPath.trim()) open(view.otherPath.trim());
    },
  }, [
    el("label", { for: "config-other" }, t("Other file")),
    el("div", { class: "row" }, [
      el("input", {
        id: "config-other", type: "text", spellcheck: "false", autocapitalize: "off", autocomplete: "off",
        placeholder: "/etc/…", value: view.otherPath,
        oninput: (event) => { view.otherPath = event.target.value; },
      }),
      el("button", { type: "submit" }, t("Open")),
    ]),
  ]);
  return el("aside", { class: "files" }, [el("ul", {}, items), other]);
}

function confirmBar(text, action, label) {
  return el("div", { class: "confirm", role: "alertdialog" }, [
    el("p", {}, text),
    el("div", { class: "actions" }, [
      el("button", { type: "button", class: "primary", onclick: action }, label),
      el("button", { type: "button", onclick: () => { view.confirm = ""; render(); } }, t("Cancel")),
    ]),
  ]);
}

function editor() {
  const entry = entryFor(view.path);
  const format = entry ? entry.format : "text";
  const check = checkContent(format, view.text);
  const willRestart = entry ? entry.restartService : view.restart;
  const parts = [];

  const meta = [el("code", {}, view.path)];
  if (view.file && view.file.exists) meta.push(" · " + view.file.mode + " · " + view.file.owner + " · " + t("{n} bytes", { n: view.file.size }));
  parts.push(el("div", { class: "meta" }, meta));
  if (view.file && !view.file.exists) parts.push(el("p", { class: "note" }, t("The file does not exist yet; saving creates it.")));

  const area = el("textarea", {
    id: "config-text", spellcheck: "false", autocapitalize: "off", autocomplete: "off", wrap: "off",
    "aria-label": t("File content"), disabled: view.busy !== "",
    oninput: (event) => {
      view.text = event.target.value;
      view.result = null;
      updateState();
    },
  });
  area.value = view.text;
  parts.push(area);
  parts.push(el("p", { id: "config-check", class: "check " + (check.ok ? "ok" : "bad") }, check.ok ? (format === "json" ? t("Valid JSON") : "") : t("Not valid JSON: {msg}", { msg: check.message })));

  if (entry) {
    if (entry.restartService) parts.push(el("p", { class: "note" }, t("Saving restarts the relay; CarPlay stops for about half a minute.")));
  } else {
    parts.push(el("label", { class: "restart" }, [
      el("input", { type: "checkbox", checked: view.restart, onchange: (event) => { view.restart = event.target.checked; render(); } }),
      t("Restart the relay after saving"),
    ]));
  }

  if (view.confirm === "save") {
    parts.push(confirmBar(willRestart
      ? t("Write this file to the device? The device keeps the current version as a backup, restarts the relay, and puts the backup back if the relay does not stay up.")
      : t("Write this file to the device? The device keeps the current version as a backup."),
    save, t("Save to device")));
  } else if (view.confirm.indexOf("restore:") === 0) {
    const id = view.confirm.slice(8);
    parts.push(confirmBar(t("Put this earlier version back on the device? The current version is kept as a backup."), () => restore(id), t("Restore")));
  }

  const busyText = { reading: t("Reading from the device…"), saving: willRestart ? t("Saving and restarting the relay…") : t("Saving…"), restoring: t("Restoring…") }[view.busy];
  parts.push(el("div", { class: "actions" }, [
    el("button", {
      id: "config-save", type: "button", class: "primary",
      disabled: view.busy !== "" || view.rawBlocked || !view.target || !check.ok || !dirty(),
      onclick: () => { view.confirm = "save"; render(); },
    }, t("Save to device")),
    el("button", { type: "button", disabled: view.busy !== "", onclick: () => open(view.path) }, t("Reload")),
    el("button", { type: "button", disabled: view.busy !== "", onclick: importFile }, t("Import…")),
    el("button", { type: "button", disabled: view.busy !== "", onclick: exportFile }, t("Export…")),
    el("button", { type: "button", disabled: view.busy !== "" || view.rawBlocked || !(view.file && view.file.exists), onclick: showBackups }, t("Earlier versions")),
  ]));
  if (busyText) parts.push(el("p", { class: "status wait" }, [el("span", { class: "dot" }), busyText]));
  if (view.backups) {
    const fmt = new Intl.DateTimeFormat(lang(), { dateStyle: "medium", timeStyle: "medium" });
    parts.push(el("div", { class: "backups" }, [
      el("h3", {}, t("Earlier versions on the device")),
      view.backups.length === 0 ? el("p", { class: "note" }, t("No earlier versions yet.")) :
        el("ul", {}, view.backups.map((b) => el("li", {}, [
          el("span", {}, fmt.format(new Date(b.time))),
          el("button", { type: "button", disabled: view.busy !== "" || view.rawBlocked, onclick: () => { view.confirm = "restore:" + b.id; render(); } }, t("Restore")),
        ]))),
    ]));
  }
  return el("div", { class: "editor" }, parts);
}

// updateState refreshes the parts that depend on the text without rebuilding the editor, so typing
// keeps the caret and the scroll position.
function updateState() {
  const entry = entryFor(view.path);
  const check = checkContent(entry ? entry.format : "text", view.text);
  const save = document.getElementById("config-save");
  if (save) save.disabled = view.busy !== "" || view.rawBlocked || !view.target || !check.ok || !dirty();
  const line = document.getElementById("config-check");
  if (line) {
    line.className = "check " + (check.ok ? "ok" : "bad");
    line.textContent = check.ok ? ((entry && entry.format === "json") ? t("Valid JSON") : "") : t("Not valid JSON: {msg}", { msg: check.message });
  }
  const result = document.getElementById("config-result");
  if (result && view.result === null) result.remove();
}

export function render() {
  if (!root) return;
  const focusedId = document.activeElement && root.contains(document.activeElement) ? document.activeElement.id : "";
  const area = focusedId ? document.getElementById(focusedId) : null;
  const caret = area && typeof area.selectionStart === "number" ? [area.selectionStart, area.selectionEnd, area.scrollTop] : null;

  const parts = [el("h2", {}, t("Device configuration")),
    el("div", { class: "actions config-categories", role: "group", "aria-label": t("Configuration") },
      [["system", t("System")], ["user", t("User")], ["privacy", t("Privacy")]].map(([category, label]) => el("button", {
        type: "button", "aria-pressed": view.category === category ? "true" : "false", disabled: view.busy !== "",
        onclick: () => {
          const change = () => {
            resetPrivacy();
            Object.assign(view, { category, path: "", file: null, text: "", confirm: "", backups: null, result: null, rawBlocked: false, openTarget: "" });
            render();
          };
          if (dirty()) {
            view.confirm = "category:" + category;
            render();
          } else change();
        },
      }, label)))];
  if (view.category === "system") parts.push(el("p", { class: "note system-warning" }, t("Wrong values here can keep the relay from starting.")));
  if (view.confirm.startsWith("category:")) {
    const category = view.confirm.slice(9);
    parts.push(confirmBar(t("Discard your changes to this file?"), () => {
      resetPrivacy();
      Object.assign(view, { category, path: "", file: null, text: "", confirm: "", backups: null, result: null, rawBlocked: false, openTarget: "" });
      render();
    }, t("Discard")));
  }
  if (!view.target) {
    parts.push(el("div", { class: "status wait" }, [el("span", { class: "dot" }), el("p", {}, errorText(t, { code: view.unavailable }))]));
    if (view.file && view.category !== "privacy") parts.push(editor());
  } else {
    const discard = view.confirm.indexOf("discard:") === 0 ? view.confirm.slice(8) : "";
    if (view.category === "privacy") {
      const file = view.target.files.find((f) => f.category === "privacy");
      if (file) parts.push(privacyView(file, view.boot.web, render));
    } else {
      const body = [fileList()];
      if (view.path) body.push(editor());
      else body.push(el("p", { class: "note pick" }, t("Choose a file to read it from the device.")));
      parts.push(el("div", { class: "config-body" }, body));
      if (discard) {
        parts.splice(1, 0, confirmBar(t("Discard your changes to this file?"), () => { view.file = null; open(discard); }, t("Discard")));
      }
    }
  }
  if (view.rawBlocked && view.file && view.category !== "privacy") parts.push(el("p", { class: "note" }, t("The device changed or is unavailable. Your edits are kept. Reload before saving.")));
  if (view.result && view.category !== "privacy") parts.push(el("div", { id: "config-result", class: view.result.kind === "ok" ? "result ok" : "error-box", role: "status" }, [view.result.text, view.result.log ? el("pre", {}, view.result.log) : ""]));
  root.replaceChildren(...parts);

  if (focusedId) {
    const again = document.getElementById(focusedId);
    if (again) again.focus();
  }
  const fresh = focusedId ? document.getElementById(focusedId) : null;
  if (fresh && caret) {
    fresh.setSelectionRange(caret[0], caret[1]);
    fresh.scrollTop = caret[2];
  }
}

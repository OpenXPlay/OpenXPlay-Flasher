// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Pure logic for the configuration view, kept free of the DOM so it runs under node:test.

// checkContent mirrors the Go side's format check, so Save is offered only for content the device
// side will accept. The Go side checks again before anything is written.
export function checkContent(format, text) {
  if (text.indexOf("\u0000") >= 0) return { ok: false, message: "NUL" };
  if (format !== "json") return { ok: true, message: "" };
  try {
    const value = JSON.parse(text);
    if (value === null || typeof value !== "object" || Array.isArray(value)) {
      return { ok: false, message: "{ … }" };
    }
    return { ok: true, message: "" };
  } catch (err) {
    return { ok: false, message: err.message };
  }
}

const ERROR_TEXT = {
  "no-adb": "This build cannot reach devices over USB.",
  "no-device": "Connect a supported device that runs Linux; its configuration files appear here.",
  "several-devices": "Several supported devices are connected. Connect only one.",
  "not-adb": "The device is not running Linux with adb, for example it is in fastboot or EDL mode.",
  "unconfirmed": "The device model is not confirmed yet.",
  "no-config": "This device model has no configuration files.",
  "protected": "This file is protected and cannot be opened.",
  "not-file": "That path is not a regular file.",
  "too-large": "The file is larger than 1 MiB.",
  "binary": "This is not a text file.",
  "bad-path": "Enter an absolute path, for example /etc/openxplay/host.json.",
  "adb-no-device": "The device does not answer over adb.",
  "adb-busy": "Another program is using the device's adb connection.",
};

// errorText turns an API error into a sentence in the UI language; t is the translator. Errors
// without a known code keep the server's own message.
export function errorText(t, err) {
  const known = ERROR_TEXT[err && err.code];
  if (known) return t(known);
  if (err && err.code === "invalid") return t("The content is not valid: {msg}", { msg: err.message });
  return t("The device reported an error: {msg}", { msg: (err && err.message) || "" });
}

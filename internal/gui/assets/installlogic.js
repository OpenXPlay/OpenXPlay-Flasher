// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Pure labels and state rules for the install view, shared with its node tests.

export const ACTION_TEXT = {
  "requirements": "Confirm model and requirements",
  "clock": "Set the device clock",
  "prepare": "Prepare the package folder",
  "push": "Upload the package",
  "checksum": "Check the uploaded SHA-256",
  "unpack": "Unpack and check the bundle",
  "preflight": "Run preflight checks",
  "install": "Install the layer",
  "reboot": "Reboot the device",
  "wait": "Wait for adb after reboot",
  "verify": "Verify the layer",
  "installed": "Read installed versions",
};

export const CHOICE_TEXT = {
  "full": "Full install",
  "device": "Only device support",
  "relay": "Only the relay",
};

export const LAYER_TEXT = {
  "base": "Base system",
  "device": "Device support",
  "relay": "OpenXPlay relay",
};

export const STATUS_TEXT = {
  "pending": "Pending",
  "running": "Running",
  "ok": "Complete",
  "failed": "Failed",
};

export const ERROR_TEXT = {
  "no-adb": "This build cannot reach devices over USB.",
  "no-device": "Connect one supported Linux device to install layers.",
  "several-devices": "Several supported devices are connected. Connect only one.",
  "not-adb": "The device is not running Linux with adb, for example it is in fastboot or EDL mode.",
  "unconfirmed": "The device model is not confirmed yet.",
  "base-missing": "Install the base system first; this version cannot flash it yet.",
  "below-missing": "A lower layer is missing: {detail}.",
  "wrong-device": "This package is for another device: {detail}.",
  "requirement": "A version or hardware requirement is not met: {detail}.",
  "required-package": "A required Debian package is missing: {detail}.",
  "not-root": "The adb shell must run as root.",
  "free-space": "There is not enough free space in {detail}.",
  "no-package": "Load a package for this choice first.",
  "duplicate-layer": "Load only one package for each layer.",
  "downgrade": "Allow the older relay version explicitly before starting.",
  "busy": "A device operation is already running. Wait for it to finish.",
  "package": "The package could not be checked: {detail}",
  "success-line": "The command did not report the expected success line: {detail}.",
  "device-checksum": "The uploaded package failed its SHA-256 check.",
  "rolled-back": "The relay update failed and restored the previous relay.",
  "reboot-not-started": "The device did not start rebooting within 120 seconds.",
  "reboot-timeout": "The device did not return over adb within 120 seconds.",
  "installed-version": "The installed version does not match the package: {detail}.",
  "adb-no-device": "The device does not answer over adb.",
  "adb-busy": "Another program is using the device's adb connection.",
  "usb": "Cannot read USB devices",
  "bad-choice": "Choose an available install plan.",
  "no-dialogs": "Enter the package path on this computer.",
};

export const LEFT_TEXT = {
  "unchanged": "No layer install was started; package files may remain in the cache.",
  "partial": "Some layers may have changed. Check the installed versions and log before retrying.",
  "previous-relay": "The previous relay is running; other completed layers remain installed.",
};

export function problemText(t, problem) {
  const code = problem && problem.code;
  return t(ERROR_TEXT[code] || "The device reported an error: {msg}", {
    detail: (problem && (problem.detail || problem.message)) || "",
    msg: (problem && (problem.detail || problem.message)) || "",
  });
}

export function canStart(state, choice, downgrade, busy = false) {
  const plan = state && (state.choices || []).find((p) => p.choice === choice);
  return !!plan && !plan.problem && plan.steps.length > 0 && !busy &&
    !(state.job && state.job.status === "running") && (!plan.downgrade || downgrade);
}

export function displayVersion(layer, version) {
  return layer === "relay" ? version.replace("~", "-") : version;
}

// A response already in flight can arrive after a newer progress event or final result.
export function mergeState(current, next) {
  const previous = current && current.job;
  const incoming = next && next.job;
  if (previous && (!incoming || incoming.started < previous.started ||
      incoming.started === previous.started && previous.status !== "running" && incoming.status === "running")) {
    return { ...next, job: previous, installed: current.installed };
  }
  return next;
}

export function elapsed(job, now = Date.now()) {
  if (!job) return "0:00";
  const end = job.status === "running" ? now : Date.parse(job.finished);
  const seconds = Math.max(0, Math.floor((end - Date.parse(job.started)) / 1000)) || 0;
  return Math.floor(seconds / 60) + ":" + String(seconds % 60).padStart(2, "0");
}

export function phase(state, confirming) {
  if (state && state.job && state.job.status === "running") return 4;
  if (state && state.job && state.job.status === "succeeded") return 5;
  if (!state || !state.target) return 0;
  return confirming ? 2 : 1;
}

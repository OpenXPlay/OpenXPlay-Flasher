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
  "below-missing": "Install {detail} first.",
  "wrong-device": "This package is for another device: {detail}.",
  "requirement": "A version or hardware requirement is not met: {detail}.",
  "required-package": "A required Debian package is missing: {detail}.",
  "not-root": "The adb shell must run as root.",
  "free-space": "There is not enough free space in {detail}.",
  "no-package": "Load a package for this choice first.",
  "duplicate-layer": "Load only one package for each layer.",
  "downgrade": "Allow the older version explicitly before starting.",
  "wrong-layer": "Choose a package for {detail}.",
  "keep-awake": "The computer could not be kept awake. Check its power settings and try again: {detail}",
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
    detail: problem && LAYER_TEXT[problem.detail] ? t(LAYER_TEXT[problem.detail]) : (problem && (problem.detail || problem.message)) || "",
    msg: (problem && (problem.detail || problem.message)) || "",
  });
}

export const COMPARISON_TEXT = {
  "new": "New install",
  "upgrade": "Upgrade",
  "same": "Same version: reinstall",
  "downgrade": "Older version: downgrade",
};

export const WARNING_TEXT = {
  "confirmation": "Do not unplug the device or cut its power until the Flasher says it is done. Interrupting the flash can leave the device unable to start (bricked).",
  "destructive": "This erases the device's system and data. Do not unplug the device or cut its power until the Flasher says it is done. Interrupting the flash can leave the device unable to start (bricked).",
  "running": "Flashing. Do not unplug the device, and keep this computer powered and awake.",
};

export const UNPLUG_TEXT = {
  "installed": "It is now safe to unplug the device.",
  "unchanged": "It is safe to unplug the device: no layer install was started.",
  "previous-relay": "It is safe to unplug the device: the previous relay is running.",
  "partial": "Safe unplugging cannot be confirmed. Keep the device connected and powered; read the layers again and check the log.",
};

export function selectedChoice(state, id) {
  return state && (state.choices || []).find((c) => c.id === id);
}

export function isRunning(state) { return !!(state && state.job && state.job.status === "running"); }

// Choose depends only on the device and the declared scope, never package availability.
export function canChoose(state, id, busy = false) {
  const choice = selectedChoice(state, id);
  return !!choice && !state.problem && !choice.problem && choice.layers.length > 0 && !busy && !isRunning(state);
}

// A provider row describes a checked payload regardless of whether it came from a file or catalog.
export function neededRows(state, id) { return selectedChoice(state, id)?.needs || []; }

export function canContinue(state, id, downgrade = false, busy = false) {
  const needs = neededRows(state, id);
  return canChoose(state, id, busy) && needs.length > 0 && needs.every((n) =>
    n.package?.checked && (n.ready || downgrade && n.problem?.code === "downgrade"));
}

export function canStart(state, id, downgrade, busy = false) {
  return canContinue(state, id, downgrade, busy);
}

export function canNavigate(state, id, step, busy = false) {
  if (busy || isRunning(state)) return false;
  if (step === 0) return true;
  if (step === 1) return !!state?.target && !!state.installed;
  return step === 2 && canChoose(state, id);
}

export function canSkipBackup(choice) { return !!choice && !choice.destructive; }

export function confirmationWarning(choice) {
  return choice?.destructive ? WARNING_TEXT.destructive : WARNING_TEXT.confirmation;
}

export function unplugText(job) {
  return UNPLUG_TEXT[job?.status === "succeeded" ? "installed" : job?.left] || UNPLUG_TEXT.partial;
}

export function guardBeforeUnload(event, state) {
  if (!isRunning(state)) return false;
  event.preventDefault();
  event.returnValue = "";
  return true;
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
    return current;
  }
  return next;
}

export function elapsed(job, now = Date.now()) {
  if (!job) return "0:00";
  const end = job.status === "running" ? now : Date.parse(job.finished);
  const seconds = Math.max(0, Math.floor((end - Date.parse(job.started)) / 1000)) || 0;
  return Math.floor(seconds / 60) + ":" + String(seconds % 60).padStart(2, "0");
}

export function phase(state, step = 0) {
  return isRunning(state) ? 4 : step;
}

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
  "chain-incomplete": "The boot chain was not finished. The dongle is no longer available in the same lk1st fastboot session. Stop here; do not restart the dongle.",
  "gpt-invalid": "The pinned partition table is invalid: {detail}.",
  "gpt-capacity": "The new partition table has no fixed capacity for {detail}. Flashing stopped before the next boot-chain write.",
  "base-version": "This base version is not pinned by the device profile: {detail}.",
  "fastboot-unsupported": "Flashing the base system is not available in the Windows version yet; use a Mac, or the manual guide.",
  "base-unconfirmed": "Confirm this board's serial before installing the base system.",
  "wrong-mode": "This plan does not apply to the connected device's current mode.",
  "wrong-product": "The bootloader product does not match: {detail}.",
  "serial-mismatch": "The board serial does not match: {detail}.",
  "stock-mismatch": "This is not the expected UFI003 original firmware. Found: {detail}. Nothing was changed.",
  "stock-location": "The stock adb serial cannot be located. Specify its USB location: {detail}.",
  "backup-restore-set": "Backup set {detail} is missing or invalid. Import a backup before continuing.",
  "backup-empty": "The modem backup contains empty calibration data: {detail}.",
  "backup-author": "The backup matches the image author or its author fingerprint is unavailable: {detail}.",
  "backup-checksum": "The backup failed its SHA-256 check: {detail}.",
  "backup-size": "The modem dump has an unexpected size: {detail}.",
  "backup-invalid": "The backup metadata is invalid: {detail}.",
  "backup-dump": "The modem partition could not be backed up: {detail}.",
  "fastboot-timeout": "The board did not return in fastboot within 90 seconds.",
  "linux-timeout": "The board did not return over adb within 180 seconds.",
  "linux-mismatch": "The Linux system does not match the base profile: {detail}.",
  "protected-write": "A protected modem partition write was refused: {detail}.",
  "unpinned-file": "An unpinned image was refused: {detail}.",
  "bad-serial": "The board serial is missing or invalid.",
  "reconnected": "The USB connection changed. Reconnect this board and choose Repair.",
  "journal-invalid": "The saved job journal is invalid. Preserve it and check the device before continuing.",
  "catalog-unavailable": "The signed catalog is unavailable. Fetch it again or import verified files.",
  "catalog-package": "The checked package does not match the catalog layer: {detail}.",
  "base": "Base installation stopped: {detail}.",
  "base-files": "Prepare all pinned base archives before starting.",
  "base-unavailable": "This device profile has no base installer.",
  "bad-plan": "The install plan failed its safety checks.",
  "typed-serial": "Type this board's serial to confirm erasing its system and data.",
  "storage-unavailable": "The backup, cache or job directory is unavailable.",
  "no-adb": "This build cannot reach devices over USB.",
  "no-device": "Connect one supported Linux device to install layers.",
  "several-devices": "Several supported devices are connected. Connect only one.",
  "not-adb": "The device is not running Linux with adb, for example it is in fastboot or EDL mode.",
  "unconfirmed": "The device model is not confirmed yet.",
  "base-missing": "Install the base system first.",
  "ssh-login": "Connect to the N1 with your SSH login first.",
  "ssh-model": "This host does not match the N1 system requirements: {detail}.",
  "ssh-host-changed": "The SSH host key changed. Connection refused. Verify the box before using Forget this host.",
  "ssh-key-passphrase": "Enter the private key passphrase and connect again.",
  "ssh-refused": "SSH connection refused: no SSH server is listening on that port. Check the port and enable SSH on the box.",
  "ssh-timeout": "SSH timed out. Check the address and that the box is powered on. If a proxy or VPN app intercepts local connections, set this address to direct.",
  "ssh-no-greeting": "No SSH greeting was received. Check the address and that the box is powered on. If a proxy or VPN app intercepts local connections, set this address to direct.",
  "ssh-authentication": "SSH authentication failed. Check the user name, password or private key.",
  "ssh-connection": "The SSH connection failed. Check the address, port and SSH service on the box.",
  "ssh-known-hosts": "Could not read the app's stored SSH host keys. Check access to the app's data folder and try again.",
  "ssh-sudo-password-required": "sudo needs the login password. Enter it and connect again.",
  "ssh-sudo-incorrect-password": "sudo rejected the login password. Check it and try again.",
  "ssh-sudo-not-sudoer": "This user is not allowed to use sudo. Log in with a sudo user or root.",
  "below-missing": "Install {detail} first.",
  "wrong-device": "This package is for another device: {detail}.",
  "requirement": "A version or hardware requirement is not met: {detail}.",
  "required-package": "A required Debian package is missing: {detail}.",
  "not-root": "The shell must run as root.",
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
  "reboot-timeout": "The device did not reconnect within 120 seconds.",
  "installed-version": "The installed version does not match the package: {detail}.",
  "adb-no-device": "The device does not answer over adb.",
  "adb-busy": "Another program is using the device's adb connection.",
  "usb": "Cannot read USB devices",
  "bad-choice": "Choose an available install plan.",
  "no-dialogs": "Enter the package path on this computer.",
  "backup-folder": "The backup folder has not been created yet.",
  "file-read": "Could not read this file: {detail}. Choose a regular file within the size limit.",
  "file-changed": "This file changed after it was selected: {detail}. Choose it again before continuing.",
  "file-cancelled": "File checking cancelled: {detail}.",
  "unmatched-file": "This file is not one of the files listed here: {detail}.",
  "catalog-unknown": "This package is not in the package list. Refused file and listed versions: {detail}.",
  "catalog-no-versions": "The package list has no versions for {detail}.",
  "identity-unrecognized": "This file is not a recognized PKCS #8 private key or PKCS #7 / CMS certificate (DER or PEM).",
  "identity-key-format": "EC PRIVATE KEY is not PKCS #8. Choose a P-256 PKCS #8 private key (PRIVATE KEY in PEM).",
  "identity-key-type": "The PKCS #8 private key must be an EC key; this key uses another algorithm.",
  "identity-key-curve": "The EC private key must use the P-256 curve.",
  "identity-encrypted": "The ZIP entry {detail} is encrypted. Choose an unencrypted identity file.",
  "identity-missing": "Missing {detail}. Select both identity.pk8 and certificate.p7b, or a ZIP containing them.",
  "identity-duplicate": "There is more than one {detail}. Select only one copy of each file.",
  "identity-read": "Could not read {detail}, or it exceeds 4 MiB. Choose a regular file.",
  "identity-zip": "The ZIP could not be opened. Choose the two identity files or a valid ZIP.",
  "identity-key": "The private key is not valid PKCS #8 data (DER or PEM).",
  "identity-certificate": "The certificate is not valid PKCS #7 / CMS SignedData (DER or PEM).",
  "identity-write": "The identity could not be added. Keep the device connected and try again with both files.",
  "identity-status": "Could not confirm the identity files on the device. Keep it connected and try again.",
  "relay-missing": "Install the OpenXPlay relay before continuing.",
  "forward-unavailable": "Could not open the USB admin connection. Keep the device connected and try again.",
  "open-folder": "Could not open the backup folder. Use the folder address shown here.",
  "picker": "Could not open the file picker. Please try again.",
  "retry-unavailable": "This step cannot be retried directly. Read the device again and choose a new plan or Repair.",
};

export const LEFT_TEXT = {
  "unchanged": "No layer install was started; your selected files remain in their original locations.",
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
  "destructive": "This erases the device's system and data: settings, the CarPlay identity, iPhone pairings and Wi-Fi settings. Do not unplug the device or cut its power until the Flasher says it is done. Interrupting the flash can leave the device unable to start (bricked).",
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
    n.package?.checked && (n.ready || downgrade && n.problem?.code === "downgrade") && artifactsVerified(n));
}

export function artifactsVerified(need) {
  const artifacts = need.artifacts || [];
  return artifacts.every((a) => a.verified && !a.error);
}

export function serialFor(state, id) {
  return selectedChoice(state, id)?.serial || state?.identity?.serial || state?.target?.device?.serialno || state?.target?.device?.usb?.serial || "";
}

export function canStart(state, id, downgrade, busy = false, typedSerial = "") {
  return canContinue(state, id, downgrade, busy) && (!selectedChoice(state, id)?.destructive ||
    !!serialFor(state, id) && typedSerial === serialFor(state, id));
}

export function canNavigate(state, id, step, busy = false) {
  if (busy || isRunning(state)) return false;
  if (step === 0) return true;
  if (step === 1) return !!state?.target && !!state.installed;
  return step === 2 && canChoose(state, id);
}

export function canSkipBackup(choice) { return !!choice && !choice.destructive; }

export function backupRestoration(choice, listing) {
  if (choice?.basePlan === "system" || !choice?.basePlan && ["base", "full-reinstall"].includes(choice?.id)) return { source: "none" };
  if (choice?.id === "repair" && listing?.repairSource === "previous-job") return { source: "previous-job", set: listing.repairSet };
  return { source: "fresh-capture" };
}

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
  if (layer === "base" && version) return /^openstick-/i.test(version) ? "OpenStick " + version.slice(10) : "OpenStick v1";
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

export function suggestedAction(state) {
  if (!state) return "";
  const has = (id) => (state.choices || []).some((c) => c.id === id);
  const mode = state.identity?.mode || state.target?.device?.mode;
  if ((mode === "stock" || state.suggested === "stock") && has("stock")) return "stock";
  if ((mode === "fastboot" || state.resume && !state.resume.finished) && has("repair")) return "repair";
  if (state.installed?.layers?.base && !state.installed.layers.device && has("full")) return "full";
  if (state.installed?.layers?.relay && state.installed.layers.device && has("relay")) return "relay";
  return state.suggested || "";
}

export function choiceGroups(state) {
  const suggestion = suggestedAction(state);
  const choices = state?.choices || [];
  const stock = suggestion === "stock";
  return {
    suggested: choices.find((c) => c.id === suggestion),
    updates: stock ? [] : choices.filter((c) => ["device", "relay", "full"].includes(c.id) && c.id !== suggestion),
    reflash: stock ? [] : choices.filter((c) => ["base", "full-reinstall", "repair"].includes(c.id) && c.id !== suggestion),
  };
}

export const CHOICE_TEXT = {
  "stock": "Installs OpenStick v1, device support and the relay, replacing the original firmware.",
  "full": "Installs device support and the relay in order; keeps the base system.",
  "device": "Updates device support and restarts the device once.",
  "relay": "Updates the relay and restarts it.",
  "base": "Reinstalls OpenStick v1 and erases settings, identities and pairings.",
  "full-reinstall": "Reinstalls OpenStick v1, device support and the relay; erases settings, identities and pairings.",
  "repair": "Repairs an interrupted installation or a device in fastboot; erases its system and data.",
};

export function parsePaths(value) {
  return value.split(/\r?\n/).map((p) => p.trim().replace(/^["'](.*)["']$/, "$1")).filter(Boolean);
}

export const PHASE_TEXT = {
  "prepare": "Prepare the device",
  "backup": "Back up",
  "critical": "Critical",
  "system": "Write the system",
  "boot": "First boot",
  "device": "Device support",
  "check-device": "Check device support",
  "relay": "OpenXPlay relay",
};
export function stepPhase(step) {
  if (step.critical) return "critical";
  if (step.layer !== "base") return step.layer;
  if (step.action === "backup") return "backup";
  if (["boot-linux", "verify-linux"].includes(step.id) || step.action === "wait-linux") return "boot";
  if (["rootfs", "boot", "system-erase-boot", "wait-system"].includes(step.id)) return "system";
  return "prepare";
}
export function phaseStyle(step) { return step?.critical ? "critical" : step?.resumable ? "resumable" : "neutral"; }
export function phaseGroups(steps) {
  return (steps || []).reduce((groups, step) => {
    const phase = step.layer === "device" && ["verify", "installed"].includes(step.action) && groups.some((g) => g.phase === "relay")
      ? "check-device" : stepPhase(step);
    if (groups.at(-1)?.phase !== phase) groups.push({ phase, steps: [] });
    groups.at(-1).steps.push(step);
    return groups;
  }, []);
}
export function nextStepsState(state) {
  const job = state?.job;
  const facts = state?.installed || job?.installed;
  const relay = !!facts?.layers?.relay;
  const device = state?.target?.device;
  const serial = state?.identity?.serial || device?.serialno || "";
  const connected = !!state?.target?.profile && ["adb", "ssh"].includes(device?.mode) && !!serial &&
    (!device.serialno || device.serialno === serial) && (!state.identity?.mode || state.identity.mode === device.mode) &&
    !state.problem && !state.installedStale;
  const next = state?.nextSteps || {};
  return { relay, connected, available: !!state?.installed?.layers?.relay && connected && !isRunning(state),
    identity: serial && next.serial === serial ? next.identity || "unknown" : "unknown",
    forward: serial && next.forward?.active && next.forward.serial === serial ? next.forward : null };
}

export function sizeText(bytes) {
  if (bytes >= 1024*1024) return (bytes/(1024*1024)).toFixed(1) + " MiB";
  if (bytes >= 1024) return (bytes/1024).toFixed(1) + " KiB";
  return String(bytes) + " B";
}

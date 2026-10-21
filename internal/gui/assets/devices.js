// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Pure view logic for the device card, kept free of the DOM so it runs under node:test.

const MODE_LABELS = {
  adb: "Linux running, adb on",
  fastboot: "Bootloader (fastboot)",
  edl: "Emergency download (EDL 9008)",
  stock: "Original firmware (designated)",
};

// Unsupported devices get the bare transport: their system is unknown.
const OTHER_MODE_LABELS = {
  adb: "adb device",
  fastboot: "fastboot device",
  edl: "Qualcomm EDL device",
};

export function modeLabel(mode) {
  return MODE_LABELS[mode] || mode;
}

export function otherModeLabel(mode) {
  return OTHER_MODE_LABELS[mode] || mode;
}

export function usbId(usb) {
  const hex = (n) => n.toString(16).padStart(4, "0");
  return hex(usb.vendorId) + ":" + hex(usb.productId);
}

export function usbPort(usb) {
  const path = usb.path || [];
  return path.length ? usb.bus + "-" + path.join(".") : String(usb.bus);
}

// text picks a profile string in the UI language, falling back to English.
export function text(t, lang) {
  return (t && (t[lang] || t.en)) || "";
}

// deviceView sorts a snapshot into what the card shows. Supported devices are candidates a profile
// matches; other adb and fastboot devices (phones, most likely) are listed apart and never chosen.
// status: "scanning" before the first snapshot, then "error", "none", "one" or "many".
export function deviceView(snapshot) {
  if (!snapshot) return { status: "scanning", supported: [], others: [], error: "" };
  const all = snapshot.candidates || [];
  const supported = all.filter((c) => (c.profiles || []).length > 0);
  const others = all.filter((c) => !(c.profiles || []).length);
  let status = "none";
  if (snapshot.error) status = "error";
  else if (supported.length === 1) status = "one";
  else if (supported.length > 1) status = "many";
  return { status, supported, others, error: snapshot.error || "" };
}

// modelView names the model of a candidate from its matching profiles. Models of one family share
// USB IDs, so more than one match leaves the model to be confirmed; the layers are shown only once
// the model is known.
export function modelView(candidate, profiles, lang) {
  const matched = (candidate.profiles || [])
    .map((id) => (profiles || []).find((p) => p.id === id))
    .filter(Boolean);
  const names = matched.map((p) => text(p.name, lang) || p.id);
  const sure = matched.length === 1;
  const layers = sure
    ? matched[0].layers.map((l) => ({ id: l.id, name: text(l.name, lang), destructive: !!l.destructive }))
    : [];
  return { names, sure, layers };
}

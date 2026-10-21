// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Pure presentation helpers for Settings and navigation, also used by the browser tests.

export function effectiveLanguage(saved, system) {
  if (saved === "en" || saved === "zh-CN") return saved;
  return system === "zh-CN" ? "zh-CN" : "en";
}

export function linksFor(links, language) {
  return links[language] || links.en || {};
}

export function viewFromHash(hash) {
  const name = hash.replace(/^#/, "");
  return ["install", "config", "settings"].includes(name) ? name : "install";
}

export function viewAllowed(name, running) {
  return ["install", "config", "settings"].includes(name) && (!running || name !== "config");
}

function adbRoute(adb, t) {
  switch (adb.mode) {
    case "auto": return t("Automatic: adb server at {address}, otherwise native USB", { address: adb.serverAddr });
    case "server": return t("adb server at {address}", { address: adb.serverAddr });
    case "usb": return t("Native USB");
    default: return t("Unknown");
  }
}

// The on-screen rows and copied report share their labels, values and optional build fields.
export function aboutRows(info, t) {
  if (!info) return [];
  const rows = [
    { label: t("Product"), value: t(info.name) },
    { label: t("Version"), value: info.version || t("Unknown") },
    { label: t("Commit"), value: info.commit || t("Unknown") },
  ];
  if (info.buildDate) rows.push({ label: t("Build date"), value: info.buildDate });
  rows.push(
    { label: t("License"), value: info.license, href: info.sourceUrl },
    { label: t("Platform"), value: info.platform + "/" + info.architecture },
  );
  if (info.osVersion) rows.push({ label: t("OS version"), value: info.osVersion });
  const adb = info.adb || {};
  rows.push({ label: t("Verify the package list signature"), value: info.verifyPackageListSignature === false ? t("Off") : t("On") });
  rows.push({ label: t("adb connection"), value: adbRoute(adb, t) });
  if (adb.lastTransport === "server" || adb.lastTransport === "usb") {
    rows.push({ label: t("Last adb connection"), value: adbRoute({ ...adb, mode: adb.lastTransport }, t) });
  }
  return rows;
}

export function aboutText(info, t) {
  return aboutRows(info, t).map(({ label, value, href }) =>
    t("{label}: {value}", { label, value: value + (href ? " (" + href + ")" : "") })).join("\n");
}

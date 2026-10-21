// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Fixture-only browser QA server. It serves the actual GUI assets and fake JSON responses;
// there is no Go service, hardware transport, external fetch, or credential data in this process.
// Run: node internal/gui/tests/browser-fixtures.mjs [port]
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const assets = fileURLToPath(new URL("../assets/", import.meta.url));
const serial = "43fb82bd";
const hash = "b9e786f197b6" + "a".repeat(52);
const text = (en, zh) => ({ en, "zh-CN": zh });
const names = { base: text("Base system", "基础系统"), device: text("Device support", "设备支持"), relay: text("OpenXPlay relay", "OpenXPlay 中继") };
const candidate = (mode = "adb") => ({ mode, profiles: ["ufi003"], product: mode === "fastboot" ? "lk1st" : "", serialno: serial,
  usb: { vendorId: 0x18d1, productId: mode === "fastboot" ? 0xd00d : 0xd001, bus: 1, address: 4, path: [3, 2], serial } });
const unknown = { mode: "adb", profiles: [], usb: { vendorId: 0x05c6, productId: 0x9091, bus: 1, address: 8, path: [4], serial } };
const facts = { root: true, kernelRelease: "5.15.0-handsomekernel+", debianVersion: "11", deviceTreeModel: "Handsome Openstick",
  layers: { base: "5.15.0-handsomekernel+", device: "0.1.0", relay: "0.1.5~beta.1" }, freeKiB: { "/var/cache": 2359296 }, packages: {} };
const baseSteps = [
  { id: "confirm", action: "confirm-linux", name: text("Confirm the device serial", "确认设备序列号"), resumable: true },
  { id: "adb-bootloader", action: "adb-bootloader", name: text("Reboot into fastboot", "重启到 fastboot"), resumable: true },
  { id: "backup", action: "backup", name: text("Back up this dongle's modem partitions", "备份本设备的调制解调器分区"), resumable: true },
  { id: "chain-partition", action: "flash", name: text("Write the partition table", "写入分区表"), critical: true },
  { id: "restore-fsg", action: "restore", name: text("Restore this dongle's fsg", "恢复本设备的 fsg"), critical: true },
  { id: "rootfs", action: "flash", name: text("Install the root filesystem", "安装根文件系统"), resumable: true },
  { id: "verify-linux", action: "wait-linux", name: text("Confirm Linux and record findings", "确认 Linux 并记录检查结果"), resumable: true },
].map((s) => ({ layer: "base", ...s }));
const artifact = (role, name, size, status = "verified") => ({ role, name, size, sha256: hash, source: "catalog", total: size,
  done: status === "downloading" ? Math.floor(size*.42) : status === "verified" ? size : 0,
  bytesPerSecond: 5242880, status, verified: status === "verified", ...(status === "failed" ? { error: { code: "download", detail: "HTTP 503 · fixture mirror unavailable" } } : {}) });

export function fixture(scene) {
  let mode = scene === "connect-fastboot" ? "fastboot" : scene === "connect-stock" ? "stock" : "adb";
  const state = { target: { profile: "ufi003", device: candidate(mode) }, identity: { mode, serial, product: mode === "fastboot" ? "lk1st" : "", location: "1-3.2" },
    installed: structuredClone(facts), installedStale: false, packages: [], layers: [], choices: [], suggested: mode === "stock" ? "stock" : mode === "fastboot" ? "repair" : "relay",
    nextSteps: { serial, identity: "absent", forward: { active: false } },
    catalog: { status: "fresh", channel: "beta", index: { sequence: 12, generated: "2026-10-21T08:00:00Z" } },
  };
  const need = (layer) => {
    const list = layer === "base" ? [artifact("base-generic", "base-generic.zip", 17825792), artifact("rootfs-zip", "rootfs.zip", 407896064), artifact("boot", "boot-ufi003.img", 12582912)] :
      [artifact(layer === "relay" ? "deb" : "bundle", layer === "relay" ? "openxplay-relay_0.1.5~beta.1_arm64.deb" : "openxplay-ufi003-0.1.0.tar.gz", layer === "relay" ? 4194304 : 786432)];
    if (scene === "prepare-downloading" && layer === "base") list[1] = artifact("rootfs-zip", "rootfs.zip", 407896064, "downloading");
    if (scene === "prepare-failed" && layer === "base") list[1] = artifact("rootfs-zip", "rootfs.zip", 407896064, "failed");
    return { layer, fileTypes: layer === "base" ? [".zip", ".img"] : layer === "relay" ? [".deb", ".manifest.json"] : [".tar.gz"], artifacts: list,
      ready: list.every((a) => a.verified), status: "checked", comparison: "same", package: { id: layer, name: layer === "base" ? "OpenStick Debian 11" : "openxplay-"+layer,
        version: layer === "base" ? "debian-11" : layer === "device" ? "0.1.0" : "0.1.5~beta.1", sha256: hash, size: list.reduce((n, a) => n+a.size, 0), checked: list.every((a) => a.verified) } };
  };
  const choice = (id, layers, destructive = false, name) => ({ id, name, serial, layers, destructive, backupRequired: destructive, ready: true,
    needs: layers.map(need), steps: [...(destructive ? baseSteps : []), ...layers.filter((l) => l !== "base").map((layer) => ({ layer, action: "install", resumable: true }))] });
  state.choices = [choice("full", ["device", "relay"]), choice("device", ["device"]), choice("relay", ["relay"]),
    choice("stock", ["base", "device", "relay"], true, text("Install OpenXPlay from the original firmware", "从原厂固件安装 OpenXPlay")),
    choice("base", ["base"], true, text("Reinstall the base system", "重新安装基础系统")),
    choice("full-reinstall", ["base", "device", "relay"], true, text("Full reinstall", "完整重装")),
    choice("repair", ["base"], true, text("Repair", "修复"))];
  if (mode !== "stock") { state.choices[3].problem = { code: "wrong-mode" }; state.choices[3].reason = text("This plan requires the original firmware.", "此方案需要设备运行原厂固件。"); }
  state.layers = ["base", "device", "relay"].map((layer) => ({ layer, name: names[layer], installed: state.installed.layers[layer], action: "update", enabled: true,
    choice: layer === "base" && mode === "stock" ? "stock" : layer, ...(layer === "base" ? { actionName: state.choices.find((c) => c.id === (mode === "stock" ? "stock" : "base")).name } : {}) }));
  const devices = { candidates: [candidate(mode)] };
  if (mode === "stock") { state.target.device.usb = { ...unknown.usb }; devices.candidates = [{ ...unknown, mode: "stock", profiles: ["ufi003"], serialno: serial }]; state.installed.layers = {}; }
  if (scene === "connect-designation") { devices.candidates = [unknown]; delete state.target; delete state.identity; delete state.installed; state.choices=[]; state.layers=[]; }
  if (scene === "connect-fastboot") { state.resume = { serial, finished: false, capturedSet: "20261021T080000Z" }; state.installed.layers = {}; }
  if (scene === "critical" || scene === "system-progress" || scene.startsWith("done-")) {
    const success = scene === "done-success" || scene === "done-identity";
    const activeIndex = scene === "system-progress" ? 5 : 3;
    const steps = state.choices.find((c) => c.id === "full-reinstall").steps.map((s, i) => ({ ...s, status: success ? "ok" : i < activeIndex ? "ok" : i === activeIndex ? ["critical", "system-progress"].includes(scene) ? "running" : "failed" : "pending" }));
    state.job = { profile: "ufi003", serial, plan: "repair", status: ["critical", "system-progress"].includes(scene) ? "running" : success ? "succeeded" : "failed",
      started: new Date(Date.now()-162000).toISOString(), finished: new Date().toISOString(), failed: success ? -1 : 3, observed: success,
      steps, log: "", installed: structuredClone(facts), left: success ? "installed" : "partial",
      findings: { "df -h /": "/dev/root 3.6G 1.3G 2.3G 37% /", "systemctl --failed --no-pager": "0 loaded units listed.", backupComparison: "different" },
      ...(!success && scene !== "critical" ? { problem: { code: "reconnected" } } : {}) };
    if (!success) state.resume = { serial, finished: false, restoreSet: "20261021T080000Z" };
    if (scene === "system-progress") state.job.transfer = { phase: "sending", piece: 2, pieces: 5, done: 73400320, total: 209715200, estimateSeconds: 27 };
    if (scene === "done-identity") state.nextSteps = { serial, identity: "present", forward: { active: true, serial, address: "https://127.0.0.1:57431/" } };
  }
  const parts = Object.fromEntries(["fsc", "fsg", "modemst1", "modemst2"].map((name, i) => [name, { size: i === 0 ? 1024 : 1572864, sha256: hash, shortSHA256: hash.slice(0, 12), source: "oem-dump" }]));
  const backups = { profile: "ufi003", serial, folder: "/Users/owner/Documents/OpenXPlay Backups/ufi003-"+serial, sets: scene === "backup-new" ? [] :
    [{ set: "20261021T083000Z", created: "2026-10-21T08:30:00Z", partitions: parts }, { set: "20261021T080000Z", created: "2026-10-21T08:00:00Z", partitions: parts }],
    repairSource: "previous-job", repairSet: "20261021T080000Z" };
  return { state, devices, backups };
}

const sessions = new Map();
const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1");
  let scene = /(?:^|; )scene=([^;]+)/.exec(req.headers.cookie || "")?.[1] || "choose";
  let language = /(?:^|; )language=([^;]+)/.exec(req.headers.cookie || "")?.[1] || "zh-CN";
  if (url.searchParams.has("scene")) {
    scene = url.searchParams.get("scene"); language = url.searchParams.get("lang") || "zh-CN";
    sessions.set(scene+language, fixture(scene));
    res.setHeader("Set-Cookie", ["scene="+scene+"; SameSite=Strict", "language="+language+"; SameSite=Strict"]);
  }
  const data = sessions.get(scene+language) || fixture(scene);
  const json = (value, status = 200) => { res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" }); res.end(JSON.stringify(value)); };
  if (url.pathname === "/boot.js") {
    const links = Object.fromEntries(["en", "zh-CN"].map((l) => [l, { manual: "https://openxplay.org/"+(l === "en" ? "en/" : "")+"ufi003-flash", tutorial: "https://openxplay.org/ufi003-relay#flasher", source: "https://github.com/OpenXPlay/OpenXPlay-Flasher" }]));
    res.setHeader("Content-Type", "text/javascript"); res.end("window.bootPrefs="+JSON.stringify({ lang: language, systemLang: language, saved: language, web: true, platform: "darwin", links })+";"); return;
  }
  if (url.pathname === "/api/state") {
    if (Number(url.searchParams.get("after")) >= (data.rev || 1)) await new Promise((resolve) => setTimeout(resolve, 1200));
    json({ rev: data.rev || 1, devices: data.devices, install: data.state }); return;
  }
  if (url.pathname === "/api/profiles") { json([{ id: "ufi003", name: text("UFI003 4G USB dongle", "UFI003 随身 Wi-Fi"), layers: Object.entries(names).map(([id,name]) => ({id,name,destructive:id === "base"})) }]); return; }
  if (url.pathname === "/api/info") { json({ version: "vx0w.5-fixture", license: "AGPL-3.0-only" }); return; }
  if (url.pathname.startsWith("/api/backups/")) { json(data.backups); return; }
  if (url.pathname === "/api/devices/designate-stock") {
    const next = fixture("connect-stock"); Object.assign(data, next, { rev: 2 }); sessions.set(scene+language, data); json(next.state); return;
  }
  if (url.pathname === "/api/install" || url.pathname === "/api/install/prepare") { json(data.state); return; }
  if (url.pathname === "/api/settings") { json({ lang: language }); return; }
  if (url.pathname.startsWith("/api/")) { json({ code: "fixture", error: "Fixture-only API" }, 409); return; }
  const name = url.pathname === "/" ? "index.html" : url.pathname.slice(1);
  if (!/^[a-z][a-z0-9.-]*$/.test(name)) { res.writeHead(404); res.end(); return; }
  try {
    const bytes = await readFile(join(assets, name));
    res.setHeader("Content-Type", name.endsWith(".js") ? "text/javascript" : name.endsWith(".css") ? "text/css" : name.endsWith(".png") ? "image/png" : "text/html");
    res.end(bytes);
  } catch { res.writeHead(404); res.end(); }
});
server.listen(Number(process.argv[2] || 0), "127.0.0.1", () => process.stdout.write("Fixture GUI: http://127.0.0.1:"+server.address().port+"/\n"));

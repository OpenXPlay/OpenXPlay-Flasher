// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { canChoose, canContinue, canStart, canNavigate, canSkipBackup, neededRows, confirmationWarning,
  guardBeforeUnload, WARNING_TEXT, unplugText, problemText, elapsed, displayVersion, phase, mergeState, ACTION_TEXT,
  suggestedAction, phaseStyle, phaseGroups, nextStepsState, backupRestoration, choiceGroups, parsePaths } from "../assets/installlogic.js";
import { setLang, t } from "../assets/i18n.js";

const checked = (layer) => ({ layer, package: { checked: true }, status: "checked", ready: true });
const choice = (id, layers, needs = layers.map((layer) => ({ layer, status: "missing", ready: false }))) =>
  ({ id, layers, needs, steps: layers.map((layer) => ({ layer, action: "install" })), ready: false });
const state = () => ({ target: {}, installed: { layers: { base: "kernel" } }, choices: [choice("full", ["device", "relay"]), choice("device", ["device"]), choice("relay", ["relay"])] });

test("Choose and its Continue never require a package", () => {
  const s = state();
  for (const id of ["full", "device", "relay"]) {
    assert.equal(canChoose(s, id), true);
    assert.equal(canNavigate(s, id, 2), true);
    assert.equal(canContinue(s, id), false);
  }
  s.choices[2].problem = { code: "below-missing", detail: "device" };
  assert.equal(canChoose(s, "relay"), false);
  assert.equal(canChoose(s, "device"), true);
  assert.equal(canChoose(s, "full", true), false);
});

test("Prepare Continue uses every needed row and ignores unneeded session packages", () => {
  const s = state();
  s.packages = [{ layer: "device", checked: false }];
  s.choices[2].needs = [checked("relay")];
  assert.deepEqual(neededRows(s, "relay").map((n) => n.layer), ["relay"]);
  assert.equal(canContinue(s, "relay"), true);
  assert.equal(canStart(s, "relay", false), true);
  s.choices[0].needs[1] = checked("relay");
  assert.equal(canContinue(s, "full"), false);
  s.choices[0].needs[0] = checked("device");
  assert.equal(canContinue(s, "full"), true);
  s.choices[0].needs[0].package.checked = false;
  assert.equal(canContinue(s, "full"), false);
  assert.equal(canContinue(s, "unknown"), false);
});

test("only explicit downgrade opt-in satisfies a downgrade row", () => {
  const s = state();
  const n = { ...checked("relay"), status: "unusable", ready: false, problem: { code: "downgrade" } };
  s.choices[2].needs = [n];
  assert.equal(canContinue(s, "relay"), false);
  assert.equal(canStart(s, "relay", true), true);
  n.problem.code = "wrong-device";
  assert.equal(canContinue(s, "relay", true), false);
  n.problem.code = "requirement";
  assert.equal(canContinue(s, "relay", true), false);
});

test("step navigation returns to reachable steps, and locks during jobs", () => {
  const s = state();
  assert.equal(canNavigate(null, "", 0), true);
  assert.equal(canNavigate(null, "", 1), false);
  for (const step of [0, 1, 2]) assert.equal(canNavigate(s, "full", step), true);
  for (const step of [3, 4, 5]) assert.equal(canNavigate(s, "full", step), false);
  s.job = { status: "running" };
  for (const step of [0, 1, 2]) assert.equal(canNavigate(s, "full", step), false);
  assert.equal(canStart(s, "relay", true), false);
  assert.equal(phase(s, 2), 4);
  s.job.status = "failed";
  assert.equal(phase(s, 1), 1);
  assert.equal(canSkipBackup({ destructive: false }), true);
  assert.equal(canSkipBackup({ destructive: true }), false);
});

test("warnings name power, bricking and erasure; unplug advice follows the left state", () => {
  setLang("en");
  assert.match(confirmationWarning({}), /cut its power.*bricked/);
  assert.match(confirmationWarning({ destructive: true }), /erases.*system and data/);
  assert.match(WARNING_TEXT.running, /powered and awake/);
  assert.match(unplugText({ status: "succeeded" }), /safe to unplug/);
  for (const left of ["unchanged", "previous-relay"]) assert.match(unplugText({ status: "failed", left }), /safe to unplug/);
  assert.match(unplugText({ status: "failed", left: "partial" }), /cannot be confirmed/);
  setLang("zh-CN");
  assert.equal(t(confirmationWarning({})), "在刷机工具提示完成之前，不要拔出设备，也不要断电。中途中断可能导致设备无法启动（变砖）。");
  assert.match(t(WARNING_TEXT.running), /供电.*睡眠/);
  setLang("en");
});

test("browser close guard is armed only while a job runs", () => {
  let prevented = 0;
  const event = { preventDefault() { prevented++; } };
  assert.equal(guardBeforeUnload(event, null), false);
  for (const status of ["succeeded", "failed"]) assert.equal(guardBeforeUnload(event, { job: { status } }), false);
  assert.equal(prevented, 0);
  assert.equal(guardBeforeUnload(event, { job: { status: "running" } }), true);
  assert.equal(prevented, 1);
  assert.equal(event.returnValue, "");
});

test("problem codes and step labels have both languages and localized layer dependencies", () => {
  setLang("en");
  assert.match(problemText(t, { code: "base-missing" }), /base system first/);
  assert.equal(problemText(t, { code: "below-missing", detail: "device" }), "Install Device support first.");
  assert.match(problemText(t, { code: "requirement", detail: "debianVersion=11" }), /debianVersion=11/);
  assert.equal(problemText(t, { code: "reboot-not-started" }), "The device did not start rebooting within 120 seconds.");
  assert.equal(problemText(t, { code: "reboot-timeout" }), "The device did not reconnect within 120 seconds.");
  setLang("zh-CN");
  assert.equal(problemText(t, { code: "below-missing", detail: "device" }), "请先安装设备支持。");
  assert.match(problemText(t, { code: "rolled-back" }), /已恢复/);
  assert.equal(t(ACTION_TEXT.reboot), "重启设备");
  assert.match(problemText(t, { code: "unknown", detail: "exit 1" }), /exit 1/);
  setLang("en");
});

test("elapsed time stops at completion and Debian beta versions use release spelling", () => {
  const job = { status: "running", started: "2026-10-21T00:00:00Z", finished: "2026-10-21T00:01:02Z" };
  assert.equal(elapsed(job, Date.parse("2026-10-21T00:01:05Z")), "1:05");
  job.status = "succeeded";
  assert.equal(elapsed(job, Date.parse("2026-10-21T00:10:00Z")), "1:02");
  assert.equal(displayVersion("relay", "0.1.5~beta.1"), "0.1.5-beta.1");
  assert.equal(displayVersion("base", "kernel+"), "OpenStick v1");
});

test("late responses cannot restore stale summaries or move a finished job back to running", () => {
  const current = { installedStale: false, installed: { layers: { relay: "0.1.5" } }, job: { started: "2026-10-21T00:01:00Z", status: "succeeded" } };
  const older = { packages: [], job: { started: "2026-10-21T00:00:00Z", status: "running" } };
  assert.equal(mergeState(current, older), current);
  assert.equal(mergeState(current, { job: { ...current.job, status: "running" } }), current);
  assert.equal(mergeState(current, { packages: [] }).installed, current.installed);
  const newer = { job: { started: "2026-10-21T00:02:00Z", status: "running" } };
  assert.equal(mergeState(current, newer), newer);
});

test("suggested actions follow stock, interrupted fastboot, OpenStick and installed relay states", () => {
  assert.equal(suggestedAction({ identity: { mode: "stock" }, choices: [{id:"stock"}] }), "stock");
  assert.equal(suggestedAction({ identity: { mode: "fastboot" }, resume: { finished: false }, choices: [{id:"repair"}] }), "repair");
  assert.equal(suggestedAction({ installed: { layers: { base: "kernel" } }, choices: [{id:"full"}] }), "full");
  assert.equal(suggestedAction({ installed: { layers: { base: "kernel", device: "1", relay: "1" } }, choices: [{id:"relay"}] }), "relay");
  assert.equal(suggestedAction(null), "");
});

test("Prepare gates every selected file and cannot bypass the catalog with another package", () => {
  const s = state();
  const need = checked("relay");
  need.package.sha256 = "local-hash";
  need.artifacts = [{ role: "deb", verified: true }, { role: "manifest", verified: false }];
  s.choices[2].needs = [need];
  assert.equal(canContinue(s, "relay"), false);
  need.artifacts[1].verified = true;
  assert.equal(canContinue(s, "relay"), true);
  need.artifacts[0].error = { code: "file-changed" };
  assert.equal(canContinue(s, "relay"), false);
  need.artifacts.push({ role: "local", sha256: "local-hash", verified: true });
  assert.equal(canContinue(s, "relay"), false);
});

test("destructive start requires the exact board serial, independently of Prepare readiness", () => {
  const s = state();
  s.identity = { serial: "43fb82bd" };
  s.choices.push({ id: "base", layers: ["base"], destructive: true, needs: [checked("base")] });
  assert.equal(canContinue(s, "base"), true);
  for (const serial of ["", "another-board", "43FB82BD", "43fb82bd "]) assert.equal(canStart(s, "base", false, false, serial), false);
  assert.equal(canStart(s, "base", false, false, "43fb82bd"), true);
  assert.equal(canStart(s, "base", false, true, "43fb82bd"), false);
});

test("phase groups retain engine order and distinguish critical, resumable and neutral steps", () => {
  const steps = [
    { layer: "base", action: "confirm-stock" },
    { layer: "base", critical: true, resumable: false, action: "flash" },
    { layer: "base", action: "backup", resumable: true },
    { layer: "base", id: "rootfs", action: "flash", resumable: true },
    { layer: "base", action: "wait-linux", resumable: true },
    { layer: "device", resumable: true }, { layer: "relay", resumable: true },
  ];
  assert.deepEqual(phaseGroups(steps).map((p) => p.phase), ["prepare", "critical", "backup", "system", "boot", "device", "relay"]);
  assert.deepEqual(steps.slice(0, 3).map(phaseStyle), ["neutral", "critical", "resumable"]);
});

test("next steps bind identity and forward status to the confirmed Linux relay", () => {
  const s = { target: { profile: "ufi003", device: { mode: "adb" } }, identity: { mode: "adb", serial: "board1" }, installed: { layers: { relay: "1" } },
    job: { status: "succeeded" }, nextSteps: { serial: "board1", identity: "absent", forward: { active: true, serial: "board1", address: "https://127.0.0.1:60001/" } } };
  assert.equal(nextStepsState(s).relay, true);
  assert.equal(nextStepsState(s).identity, "absent");
  assert.equal(nextStepsState(s).forward.address, s.nextSteps.forward.address);
  s.nextSteps.identity = "present";
  assert.equal(nextStepsState(s).identity, "present");
  s.identity.serial = "board2";
  assert.equal(nextStepsState(s).identity, "unknown");
  assert.equal(nextStepsState(s).forward, null);
  s.problem = { code: "no-device" };
  assert.equal(nextStepsState(s).connected, false);
  s.job.status = "failed";
  assert.equal(nextStepsState(s).available, false);
  delete s.problem;
  assert.equal(nextStepsState(s).available, true, "an earlier failure does not hide a freshly confirmed relay");
  delete s.job;
  assert.equal(nextStepsState(s).available, true, "no app install is required");
});

test("browser multiple paths preserve spaces and arbitrary filenames", () => {
  assert.deepEqual(parsePaths(' /tmp/some file.bin\r\n"/tmp/other name"\n\n'), ["/tmp/some file.bin", "/tmp/other name"]);
});

test("Choose groups the suggestion, updates and applicable reflash actions", () => {
  const s = state();
  s.choices.push({id:"base"}, {id:"full-reinstall"}, {id:"repair"});
  s.identity = {mode:"adb"};
  let groups = choiceGroups(s);
  assert.equal(groups.suggested.id, "full");
  assert.deepEqual(groups.updates.map(c=>c.id), ["device", "relay"]);
  assert.deepEqual(groups.reflash.map(c=>c.id), ["base", "full-reinstall", "repair"]);
  s.resume = {finished:false};
  assert.equal(choiceGroups(s).suggested.id, "repair");
  assert.equal(choiceGroups(s).reflash.some(c=>c.id === "repair"), false);
  s.identity.mode = "stock"; s.choices.push({id:"stock"});
  groups = choiceGroups(s);
  assert.equal(groups.suggested.id, "stock");
  assert.deepEqual(groups.updates, []); assert.deepEqual(groups.reflash, []);
});

test("backup restoration follows the actual engine plan, including system-only full reinstalls", () => {
  const listing = { repairSource: "previous-job", repairSet: "20261021T080000Z" };
  assert.deepEqual(backupRestoration({ id: "repair", basePlan: "repair" }, listing), { source: "previous-job", set: listing.repairSet });
  for (const id of ["base", "full-reinstall"]) assert.deepEqual(backupRestoration({ id, basePlan: "system" }, listing), { source: "none" });
  assert.deepEqual(backupRestoration({ id: "stock", basePlan: "stock" }, listing), { source: "fresh-capture" });
});

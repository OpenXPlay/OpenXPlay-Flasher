// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { canChoose, canContinue, canStart, canNavigate, canSkipBackup, neededRows, confirmationWarning,
  guardBeforeUnload, WARNING_TEXT, unplugText, problemText, elapsed, displayVersion, phase, mergeState, ACTION_TEXT } from "../assets/installlogic.js";
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
  assert.equal(problemText(t, { code: "reboot-timeout" }), "The device did not return over adb within 120 seconds.");
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
  assert.equal(displayVersion("base", "kernel+"), "kernel+");
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

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { canStart, problemText, elapsed, displayVersion, phase, mergeState, ACTION_TEXT } from "../assets/installlogic.js";
import { setLang, t } from "../assets/i18n.js";

test("Start requires an available plan, idle device and explicit downgrade opt-in", () => {
  const state = { choices: [{ choice: "full", steps: [{ action: "install" }] }] };
  assert.equal(canStart(state, "full", false), true);
  assert.equal(canStart(state, "relay", false), false);
  state.choices[0].downgrade = true;
  assert.equal(canStart(state, "full", false), false);
  assert.equal(canStart(state, "full", true), true);
  assert.equal(canStart(state, "full", true, true), false);
  state.job = { status: "running" };
  assert.equal(canStart(state, "full", true), false);
  state.job.status = "failed";
  state.choices[0].problem = { code: "base-missing" };
  assert.equal(canStart(state, "full", true), false);
});

test("problem codes and step labels have both languages", () => {
  setLang("en");
  assert.match(problemText(t, { code: "base-missing" }), /base system first/);
  assert.match(problemText(t, { code: "requirement", detail: "debianVersion=11" }), /debianVersion=11/);
  assert.equal(problemText(t, { code: "reboot-not-started" }), "The device did not start rebooting within 120 seconds.");
  assert.equal(problemText(t, { code: "reboot-timeout" }), "The device did not return over adb within 120 seconds.");
  setLang("zh-CN");
  assert.match(problemText(t, { code: "rolled-back" }), /已恢复/);
  assert.equal(problemText(t, { code: "reboot-not-started" }), "设备未在 120 秒内开始重启。");
  assert.equal(problemText(t, { code: "reboot-timeout" }), "设备未在 120 秒内恢复 adb 连接。");
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

test("phase follows connection, confirmation, running and completion", () => {
  assert.equal(phase(null, false), 0);
  assert.equal(phase({ target: {} }, false), 1);
  assert.equal(phase({ target: {} }, true), 2);
  assert.equal(phase({ job: { status: "running" } }, false), 4);
  assert.equal(phase({ job: { status: "succeeded" } }, false), 5);
});

test("late responses keep a newer job and never move a finished job back to running", () => {
  const current = { installed: { layers: { relay: "0.1.5" } }, job: { started: "2026-10-21T00:01:00Z", status: "succeeded" } };
  const older = { packages: [], job: { started: "2026-10-21T00:00:00Z", status: "running" } };
  assert.equal(mergeState(current, older).job, current.job);
  assert.equal(mergeState(current, { job: { ...current.job, status: "running" } }).job.status, "succeeded");
  assert.equal(mergeState(current, { packages: [] }).installed, current.installed);
  const newer = { job: { started: "2026-10-21T00:02:00Z", status: "running" } };
  assert.equal(mergeState(current, newer), newer);
});

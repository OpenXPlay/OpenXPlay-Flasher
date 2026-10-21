// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

import { test } from "node:test";
import assert from "node:assert/strict";
import { deviceView, modelView, modeLabel, otherModeLabel, text, usbId, usbPort } from "../assets/devices.js";

const dongle = { mode: "adb", profiles: ["ufi003"], usb: { vendorId: 0x18d1, productId: 0xd001, bus: 0, path: [1, 1, 1] } };
const phone = { mode: "adb", profiles: [], usb: { vendorId: 0x2717, productId: 0xff08, bus: 0, path: [1, 1, 4] } };
const edl = { mode: "edl", profiles: ["ufi003"], usb: { vendorId: 0x05c6, productId: 0x9008, bus: 1, path: [] } };

const profiles = [
  { id: "ufi001", name: { en: "UFI001", "zh-CN": "UFI001 随身 Wi-Fi" }, layers: [] },
  { id: "ufi003", name: { en: "UFI003 4G USB dongle", "zh-CN": "UFI003 随身 Wi-Fi" }, layers: [
    { id: "base", name: { en: "Base system", "zh-CN": "基础系统" }, destructive: true },
    { id: "relay", name: { en: "OpenXPlay relay", "zh-CN": "OpenXPlay 中继" } },
  ] },
];

test("no snapshot yet is scanning", () => {
  assert.equal(deviceView(null).status, "scanning");
});

test("phones are listed apart and never count as dongles", () => {
  const view = deviceView({ candidates: [phone, dongle] });
  assert.equal(view.status, "one");
  assert.deepEqual(view.supported, [dongle]);
  assert.deepEqual(view.others, [phone]);
  assert.equal(deviceView({ candidates: [phone] }).status, "none");
});

test("two dongles ask for one", () => {
  assert.equal(deviceView({ candidates: [dongle, edl] }).status, "many");
});

test("a bus error wins over the device list", () => {
  const view = deviceView({ candidates: [], error: "access denied" });
  assert.equal(view.status, "error");
  assert.equal(view.error, "access denied");
});

test("IDs and ports are formatted like lsusb", () => {
  assert.equal(usbId(dongle.usb), "18d1:d001");
  assert.equal(usbPort(dongle.usb), "0-1.1.1");
  assert.equal(usbPort(edl.usb), "1");
});

test("one matching profile names the model and its layers", () => {
  const model = modelView(dongle, profiles, "zh-CN");
  assert.deepEqual(model.names, ["UFI003 随身 Wi-Fi"]);
  assert.equal(model.sure, true);
  assert.deepEqual(model.layers, [
    { id: "base", name: "基础系统", destructive: true },
    { id: "relay", name: "OpenXPlay 中继", destructive: false },
  ]);
});

test("a shared USB ID leaves the model to be confirmed", () => {
  const model = modelView({ ...dongle, profiles: ["ufi001", "ufi003"] }, profiles, "en");
  assert.deepEqual(model.names, ["UFI001", "UFI003 4G USB dongle"]);
  assert.equal(model.sure, false);
  assert.deepEqual(model.layers, []);
});

test("profile text falls back to English", () => {
  assert.equal(text({ en: "Relay" }, "zh-CN"), "Relay");
  assert.equal(text(undefined, "en"), "");
});

test("unsupported devices are not described as running Linux", () => {
  assert.equal(modeLabel("adb"), "Linux running, adb on");
  assert.equal(otherModeLabel("adb"), "adb device");
  assert.equal(otherModeLabel("edl"), "Qualcomm EDL device");
});

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// The English text is the key and the fallback; ZH holds its Simplified Chinese translation.
// Placeholders are written {name} and filled from t()'s second argument.

export const ZH = {
  "OpenXPlay Flasher": "OpenXPlay 刷机工具",
  "Install the OpenXPlay relay on supported devices, from scratch or one layer at a time.": "在受支持的设备上安装 OpenXPlay 中继：可以从头完整刷写，也可以单独更新某一层。",
  "Development preview: this version only detects devices and never writes to them.": "开发预览版：本版本只检测设备，不会向设备写入任何内容。",
  "Connect": "连接",
  "Choose": "选择",
  "Prepare": "准备",
  "Back up": "备份",
  "Flash": "刷写",
  "Done": "完成",
  "Connect your device": "连接设备",
  "Looking for USB devices…": "正在查找 USB 设备…",
  "Cannot read USB devices": "无法读取 USB 设备",
  "No supported device found": "未找到受支持的设备",
  "Plug the device into a USB port directly on this computer, not through a hub without its own power.": "请将设备直接插在这台电脑的 USB 口上，不要通过无独立供电的集线器连接。",
  "Device found": "已找到设备",
  "Confirming the model and reading its installed layers over adb comes in the next version.": "通过 adb 确认型号并读取已安装的各层，将在下个版本加入。",
  "Several supported devices found": "发现多台受支持的设备",
  "Connect only one device at a time; unplug the others.": "一次只能连接一台设备，请拔掉其余设备。",
  "Model": "型号",
  "{models} (to be confirmed)": "{models}（待确认）",
  "Mode": "模式",
  "USB ID": "USB ID",
  "Port": "端口",
  "Layers": "分层",
  "erases the device": "会清空设备",
  "Linux running, adb on": "Linux 运行中，adb 已开启",
  "Fastboot (bootloader)": "Fastboot（引导程序）",
  "Emergency download (EDL 9008)": "紧急下载模式（EDL 9008）",
  "{n} other Android devices are connected and ignored.": "另有 {n} 台 Android 设备已连接，已忽略。",
  "1 other Android device is connected and ignored.": "另有 1 台 Android 设备已连接，已忽略。",
  "A supported device running its original firmware may be among them; it is recognised once its USB IDs are added to its profile.": "其中可能有运行原厂固件的受支持设备；在该型号的配置中加入它的 USB ID 后即可识别。",
  "Lost contact with the flasher. Retrying…": "与刷机工具的连接中断，正在重试…",
  "Version {v}": "版本 {v}",
  "License: {l}": "许可证：{l}",
};

const tables = { "zh-CN": ZH };
let current = "en";

export function setLang(lang) {
  current = tables[lang] ? lang : "en";
}

export function lang() {
  return current;
}

// t translates an English string and fills its {placeholders}.
export function t(text, vars) {
  const table = tables[current];
  let out = (table && table[text]) || text;
  if (vars) {
    for (const key of Object.keys(vars)) {
      out = out.split("{" + key + "}").join(String(vars[key]));
    }
  }
  return out;
}

// translateStatic re-renders every element marked data-t from its English text, kept in data-en.
export function translateStatic(root) {
  for (const el of root.querySelectorAll("[data-t]")) {
    if (!el.dataset.en) el.dataset.en = el.dataset.t;
    el.textContent = t(el.dataset.en);
  }
}

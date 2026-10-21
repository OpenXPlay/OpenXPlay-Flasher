// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// The English text is the key and the fallback; ZH holds its Simplified Chinese translation.
// Placeholders are written {name} and filled from t()'s second argument.

export const ZH = {
  "Removed. The relay returns to its self-signed certificate within 10 seconds.": "已移除，中继将在 10 秒内恢复使用自签名证书。",
  "Restored. The relay picks it up within 10 seconds.": "已恢复，中继将在 10 秒内生效。",
  "The device changed or is unavailable. Your edits are kept. Reload before saving.": "设备已变化或不可用，修改已保留。请重新读取后再保存。",
  "System": "系统配置",
  "User": "用户配置",
  "Privacy": "隐私配置",
  "Wrong values here can keep the relay from starting.": "此处的错误设置可能导致中继无法启动。",
  "Trusted HTTPS": "可信 HTTPS",
  "Private data stays on the device. The flasher never shows or exports it.": "隐私数据只保存在设备上，刷机工具不会显示或导出它。",
  "Open the head-unit page without a certificate warning, using a free domain name.": "用免费域名，让车机打开网页时不再出现证书警告。",
  "Use a free domain name": "使用免费域名",
  "DNS provider": "域名服务商",
  "Domain name": "域名",
  "For example myrelay.duckdns.org": "例如 myrelay.duckdns.org",
  "Token": "Token",
  "Token saved": "已保存 Token",
  "Copy it from your provider account. It stays on this relay.": "从服务商账号中复制，只保存在这台中继上。",
  "I agree to the Let's Encrypt Subscriber Agreement": "我同意 Let's Encrypt 订户协议",
  "Fill in the provider, domain name and token, and agree to the terms.": "请填写域名服务商、域名和 Token，并同意协议。",
  "Save": "保存",
  "Import configuration file…": "导入配置文件…",
  "Remove": "移除",
  "Saved. The relay picks it up within 10 seconds.": "已保存，中继将在 10 秒内生效。",
  "Remove the trusted HTTPS configuration from the device? Its earlier versions are deleted too, and this cannot be undone.": "要从设备上删除可信 HTTPS 配置吗？之前的版本也会一并删除，且无法撤销。",
  "Working…": "正在处理…",
  "Check the marked fields.": "请检查标出的字段。",
  "This field is missing.": "尚未填写此字段。",
  "This field is invalid.": "此字段无效。",
  "The file contains an unknown key.": "文件包含未知字段。",
  "The file version must be 1.": "文件版本必须为 1。",
  "The privacy file is larger than 16 KiB.": "隐私配置文件大于 16 KiB。",
  "The file must contain one JSON object.": "文件必须包含一个 JSON 对象。",
  "This file holds private data. Open it under Privacy.": "此文件包含隐私数据，请在“隐私配置”中打开。",

  "OpenXPlay Flasher": "OpenXPlay 刷机工具",
  "Install the OpenXPlay relay on supported devices, from scratch or one layer at a time.": "在受支持的设备上安装 OpenXPlay 中继：可以从头完整刷写，也可以单独更新某一层。",
  "Development preview: installing and updating are not available yet; this view only detects devices.": "开发预览版：暂不支持安装和更新，此页面只检测设备。",
  "Views": "视图",
  "Install and update": "安装与更新",
  "Configuration": "配置",
  "Device configuration": "设备配置",
  "Choose a file to read it from the device.": "选择一个文件，从设备上读取它。",
  "Other file": "其他文件",
  "Open": "打开",
  "Cancel": "取消",
  "{n} bytes": "{n} 字节",
  "The file does not exist yet; saving creates it.": "该文件尚不存在，保存时会创建它。",
  "File content": "文件内容",
  "Valid JSON": "JSON 格式正确",
  "Not valid JSON: {msg}": "JSON 格式错误：{msg}",
  "Saving restarts the relay; CarPlay stops for about half a minute.": "保存后会重启中继，CarPlay 会中断约半分钟。",
  "Restart the relay after saving": "保存后重启中继",
  "Write this file to the device? The device keeps the current version as a backup, restarts the relay, and puts the backup back if the relay does not stay up.": "要把此文件写入设备吗？设备会把当前版本保存为备份并重启中继；如果中继无法正常运行，会自动恢复备份。",
  "Write this file to the device? The device keeps the current version as a backup.": "要把此文件写入设备吗？设备会把当前版本保存为备份。",
  "Save to device": "保存到设备",
  "Put this earlier version back on the device? The current version is kept as a backup.": "要在设备上恢复这个较早的版本吗？当前版本会保存为备份。",
  "Restore": "恢复",
  "Reading from the device…": "正在从设备读取…",
  "Saving and restarting the relay…": "正在保存并重启中继…",
  "Saving…": "正在保存…",
  "Restoring…": "正在恢复…",
  "Reload": "重新读取",
  "Import…": "导入…",
  "Export…": "导出…",
  "Earlier versions": "较早版本",
  "Earlier versions on the device": "设备上的较早版本",
  "No earlier versions yet.": "暂无较早版本。",
  "Discard your changes to this file?": "要放弃对此文件的修改吗？",
  "Discard": "放弃",
  "The relay did not stay up with the new file, so the previous version was put back.": "使用新文件后中继无法正常运行，已恢复为之前的版本。",
  "The relay did not stay up. Check the file and the relay log.": "中继无法正常运行，请检查文件和中继日志。",
  "Saved. The relay restarted and is running.": "已保存。中继已重启并正常运行。",
  "Saved.": "已保存。",
  "Loaded {name}. Save to write it to the device.": "已载入 {name}，保存后才会写入设备。",
  "Saved a copy to {path}.": "已将副本保存到 {path}。",
  "This build cannot reach devices over USB.": "此版本无法通过 USB 访问设备。",
  "Connect a supported device that runs Linux; its configuration files appear here.": "请连接运行 Linux 的受支持设备，它的配置文件会显示在这里。",
  "Several supported devices are connected. Connect only one.": "连接了多台受支持的设备，请只连接一台。",
  "The device is not running Linux with adb, for example it is in fastboot or EDL mode.": "设备未运行 Linux 或未开启 adb，例如正处于 fastboot 或 EDL 模式。",
  "The device model is not confirmed yet.": "尚未确认设备型号。",
  "This device model has no configuration files.": "此设备型号没有可编辑的配置文件。",
  "This file is protected and cannot be opened.": "此文件受保护，无法打开。",
  "That path is not a regular file.": "该路径不是普通文件。",
  "The file is larger than 1 MiB.": "文件大于 1 MiB。",
  "This is not a text file.": "这不是文本文件。",
  "Enter an absolute path, for example /etc/openxplay/host.json.": "请输入绝对路径，例如 /etc/openxplay/host.json。",
  "The device does not answer over adb.": "设备没有通过 adb 响应。",
  "Another program is using the device's adb connection.": "另一个程序正在使用设备的 adb 连接。",
  "The content is not valid: {msg}": "内容无效：{msg}",
  "The device reported an error: {msg}": "设备返回错误：{msg}",
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
  "Confirming the model and reading its installed layers over adb comes in the next version.": "下一版本将支持通过 adb 确认设备型号，并读取各层的安装状态。",
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
  "adb device": "adb 设备",
  "fastboot device": "fastboot 设备",
  "Qualcomm EDL device": "高通 EDL 设备",
  "{n} other Android devices are connected and ignored.": "另有 {n} 台 Android 设备已连接，已忽略。",
  "1 other Android device is connected and ignored.": "另有 1 台 Android 设备已连接，已忽略。",
  "A supported model that is not recognised yet, for example one still on its original firmware, may be among them; it is recognised once its USB IDs are added.": "其中可能有尚未识别的受支持型号（例如仍运行原厂固件的设备）；加入对应的 USB ID 后即可识别。",
  "Lost contact with the flasher. Retrying…": "与刷机工具的连接中断，正在重试…",
  "Language": "语言",
  "Steps": "步骤",
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

// translateStatic re-renders every element marked data-t from its English text, and the
// aria-label of every element marked data-ta.
export function translateStatic(root) {
  for (const el of root.querySelectorAll("[data-t]")) {
    el.textContent = t(el.dataset.t);
  }
  for (const el of root.querySelectorAll("[data-ta]")) {
    el.setAttribute("aria-label", t(el.dataset.ta));
  }
}

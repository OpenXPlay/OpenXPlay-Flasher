<p align="center"><a href="README.md">English</a> · <b>简体中文</b></p>

# OpenXPlay-Flasher

一款 Windows 和 macOS 桌面应用，用于在受支持的设备上安装 OpenXPlay 中继并保持更新。每台设备的系统由若干层叠加而成（例如基础系统、设备支持、中继）。刷机工具既可以从头刷写全部各层，也可以单独更新某一层；通常只需要更新中继这一层。“配置”页面可以通过 USB 读取和修改已连接的 Linux 中继的配置文件：设备会保留每个版本的备份，如果修改后中继无法正常运行，会自动恢复之前的版本。

> 预览版。可以安装全部各层：用 fastboot 刷写基础系统（目前仅限 macOS），通过 adb 安装设备支持层和中继（Windows 版需要电脑上已在运行 adb 服务）。刷机工具不会自动下载安装包：“准备”页面列出每个文件和下载链接，请自行下载后选择文件，刷机工具会按签名软件包列表中的 SHA-256 核对。

| 设备 | 分层 |
| --- | --- |
| UFI003 随身 Wi-Fi（OpenStick） | OpenStick v1、设备支持、OpenXPlay 中继 |

设备配置位于 [`internal/profile/profiles`](./internal/profile/profiles)。

## 打开应用

从 [Releases](https://github.com/OpenXPlay/OpenXPlay-Flasher/releases) 下载对应系统的版本，并用其中的 `SHA256SUMS` 核对文件的 SHA-256。安装包没有使用 Apple 或 Microsoft 证书签名，所以首次打开时系统会先询问。

**macOS 12 或更高版本**（`OpenXPlay-Flasher-<版本>-macos-universal.dmg`）：把应用拖进“应用程序”文件夹，然后打开一次。

- macOS 15 或更高版本：出现“未打开”提示时选择“完成”，打开“系统设置 > 隐私与安全性”，在关于 OpenXPlay Flasher 的提示旁选择**仍要打开**。
- macOS 12 至 14：按住 Control 键点按应用，选择“打开”，再选择“打开”。
- 也可以在“终端”中移除隔离标记：
  `xattr -dr com.apple.quarantine "/Applications/OpenXPlay Flasher.app"`

**Windows 10 或 11**（`openxplay-flasher-windows-amd64.exe`；Arm 电脑使用 `-arm64.exe`）：出现“Windows 已保护你的电脑”时，选择**更多信息**，再选择**仍要运行**。如果应用提示缺少 Microsoft Edge WebView2 运行时，请安装后重新打开应用。

## 构建

需要 Go 1.26 或更高版本；macOS 上还需要 Xcode 命令行工具、pkg-config，以及供 `make build` 和测试使用的 libusb（`brew install libusb pkg-config`）；运行界面测试需要 Node.js 22。`make app` 会自行构建适用于 macOS 12 及以上的通用 libusb（只需下载一次），并用 Go 1.26（可在 macOS 12 上运行的最后一个 Go 版本）构建应用。

```bash
make app        # macOS：dist/OpenXPlay Flasher.app（通用二进制；设置 SIGN_IDENTITY=... 可签名）
make dmg        # macOS：生成磁盘映像；make notarize 提交 Apple 公证
make windows    # 在 dist/ 中生成 Windows exe（交叉编译，不使用 cgo；通过 SetupAPI 列出 USB 设备）
make test       # Go、界面和跨平台检查
make dev        # 运行应用，页面直接读取 internal/gui/assets
make web        # 在本机浏览器中打开页面，便于开发界面
```

`openxplay-flasher help` 会列出命令行命令（`devices`、`watch`、`profiles` 等）。

## 参与贡献

贡献须遵循[贡献者许可协议](./CLA.md)（协议文本为英文）。每个 pull request 上的 CLA Assistant 检查会记录你对协议的接受。

## 许可证

本项目采用 [GNU Affero General Public License v3.0 (AGPLv3)](./LICENSE) 授权，仅限第 3 版。

本项目贡献的内容适用依据 AGPLv3 第 7 条设定的附加条款。修改版本必须在相应的法律声明中，以及用户界面中任何显著的“关于”、法律、页脚或署名位置，保留署名声明 `Powered by OpenXPlay.`，并附上指向原项目的可见链接：<https://github.com/OpenXPlay/OpenXPlay-Flasher>。详见 [NOTICE](./NOTICE)。

第三方组件保留各自的许可证，详见 [THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md)。

如果贵组织的政策不允许使用 AGPLv3 许可的软件，或你希望免于履行 AGPLv3 的开源义务，请联系：[support@openxplay.org](mailto:support@openxplay.org)。

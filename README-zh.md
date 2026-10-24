<p align="center"><a href="README.md">English</a> · <b>简体中文</b></p>

# OpenXPlay-Flasher

一款 Windows 和 macOS 桌面应用，用于在受支持的设备上安装 OpenXPlay 中继并保持更新。每台设备的系统由若干层叠加而成（例如基础系统、设备支持、中继）。刷机工具既可以从头刷写全部各层，也可以单独更新某一层；通常只需要更新中继这一层。“配置”页面可以通过 USB 读取和修改已连接的 Linux 中继的配置文件：设备会保留每个版本的备份，如果修改后中继无法正常运行，会自动恢复之前的版本。

> 预览版。用 fastboot 刷写基础系统，通过 adb 安装设备支持层和中继。Windows 原生通过 WinUSB 支持这两种协议，无需外部 adb 服务，也可以继续使用电脑上已在运行的 adb 服务。刷机工具不会自动下载安装包：“准备”页面列出每个文件和下载链接，请自行下载后选择文件，刷机工具会按签名软件包列表中的 SHA-256 核对。已在 Windows 真机上跑通 UFI003 原厂系统的完整安装，以及已有 OpenStick 系统的中继升级。

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

Windows 通过 SetupAPI 列出已连接的 USB 设备，扫描不依赖 adb 驱动。原生 adb 和 fastboot 通信则要求对应的设备接口使用 **WinUSB** 驱动。设备已列出但无法打开时，错误信息会指出接口并提示检查驱动。请在“设备管理器”中检查 **ADB 接口**，或进入引导加载程序后检查 **fastboot 接口**，为该接口安装兼容的 WinUSB 驱动；这两种模式可能需要分别安装驱动。对于包含多个接口的 OpenStick 设备，请保留 **RNDIS 网卡**及其网络驱动，只调整 adb 或 fastboot 接口，不要替换整个复合设备的驱动。若已运行的 adb 服务能访问设备，也可以继续通过它连接。

### Windows 驱动提示与安装

0.2.0-beta.2 在读取设备或打开 fastboot 接口失败时，会把底层错误显示在界面的错误区。缺少已注册的 WinUSB 接口时，提示中会包含 `needs a WinUSB driver`、`install the ADB/fastboot WinUSB driver` 或 `has no WinUSB … interface`。这些底层信息目前可能是英文；应用不会自动下载或安装驱动，也没有一键安装驱动的按钮。

1. 在“设备管理器”中找到这台 UFI003 的 ADB 或 fastboot 接口，打开“属性 → 详细信息 → 硬件 ID”，核对当前模式对应的 ID。不要选择 RNDIS 网卡或整个 USB 复合设备。
2. 下载匹配硬件 ID、Windows 版本和处理器架构的已签名驱动并解压。Windows 11 上已验证的 `USB\VID_18D1&PID_D00D` fastboot 接口可使用 [Microsoft Update Catalog 的 Android Bootloader Interface 驱动](https://www.catalog.update.microsoft.com/ScopedViewInline.aspx?updateid=76f2c233-6100-4a64-8bee-113c2da0991d)；其他 ID 应使用其对应的驱动。
3. 右键该接口，选择“更新驱动程序 → 浏览我的电脑以查找驱动程序”，选择解压目录并完成安装。Windows 的手动安装步骤见 [Microsoft 说明](https://support.microsoft.com/en-us/windows/update-drivers-through-device-manager-in-windows-ec62f46c-ff14-c91d-eead-d7126dc1f7b6)。保持驱动签名校验开启。
4. 回到 Flasher 点击“重新读取”。ADB 和 fastboot 是不同接口，进入 fastboot 后可能还需要为新出现的接口安装驱动。若安装任务已失败，按应用提示重新读取并选择“修复”；不要手工刷写分区。

如果设备管理器中插拔前后完全没有新增 USB 设备，先检查供电、USB 连接和直连端口；这与已列出设备但缺少通信驱动的情况不同。完整教程见 [Windows 安装与排查](https://openxplay.org/ufi003-relay#flasher-windows-drivers)。

## 构建

需要 Go 1.26 或更高版本；macOS 上还需要 Xcode 命令行工具、pkg-config，以及供 `make build` 和测试使用的 libusb（`brew install libusb pkg-config`）；运行界面测试需要 Node.js 22。`make app` 会自行构建适用于 macOS 12 及以上的通用 libusb（只需下载一次），并用 Go 1.26（可在 macOS 12 上运行的最后一个 Go 版本）构建应用。

```bash
make app        # macOS：dist/OpenXPlay Flasher.app（通用二进制；设置 SIGN_IDENTITY=... 可签名）
make dmg        # macOS：生成磁盘映像；make notarize 提交 Apple 公证
make windows    # 在 dist/ 中生成 Windows exe（不使用 cgo；SetupAPI 扫描、WinUSB adb/fastboot）
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

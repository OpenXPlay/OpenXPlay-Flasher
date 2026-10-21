<p align="center"><a href="README.md">English</a> · <b>简体中文</b></p>

# OpenXPlay-Flasher

一款 Windows 和 macOS 桌面应用，用于在受支持的设备上安装 OpenXPlay 中继并保持更新。每台设备的系统由若干层叠加而成（例如基础系统、设备支持、中继）。刷机工具既可以从头刷写全部各层，也可以单独更新某一层；通常只需要更新中继这一层。

> 开发预览版：本版本可以检测设备并显示其分层，暂不会向任何设备写入内容。

| 设备 | 分层 |
| --- | --- |
| UFI003 随身 Wi-Fi（OpenStick） | 基础系统（OpenStick Debian 11）、设备支持、OpenXPlay 中继 |

设备配置位于 [`internal/profile/profiles`](./internal/profile/profiles)。

## 构建

需要 Go 1.26 或更高版本；macOS 上还需要 Xcode 命令行工具和 libusb（`brew install libusb pkg-config`）；运行界面测试需要 Node.js 22。

```bash
make app        # macOS：dist/OpenXPlay Flasher.app
make windows    # 在 dist/ 中生成 Windows exe（交叉编译，不使用 cgo；Windows 上的 USB 访问尚未完成）
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

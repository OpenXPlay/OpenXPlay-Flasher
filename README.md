<p align="center"><b>English</b> · <a href="README-zh.md">简体中文</a></p>

# OpenXPlay-Flasher

A desktop app for Windows and macOS that installs the OpenXPlay relay on supported devices and keeps
it up to date. Each device's system is a stack of layers (for example base system, device support,
relay). The flasher can install every layer from scratch or update a single layer on its own, which
is usually only the relay.

> Development preview: this version detects devices and shows their layers; it does not write to
> any device yet.

| Device | Layers |
| --- | --- |
| UFI003 4G USB dongle (OpenStick) | base system (OpenStick Debian 11), device support, OpenXPlay relay |

Device profiles live in [`internal/profile/profiles`](./internal/profile/profiles).

## Open the app

Download the build for your system from [Releases](https://github.com/OpenXPlay/OpenXPlay-Flasher/releases)
and compare its SHA-256 with `SHA256SUMS` there. The builds are not signed with an Apple or
Microsoft certificate, so the system asks before the first start.

**macOS 12 or later** (`OpenXPlay-Flasher-<version>-macos-universal.dmg`): drag the app into
Applications, then open it once.

- macOS 15 or later: when the "not opened" message appears, choose Done, open System Settings >
  Privacy & Security, and choose **Open Anyway** next to the message about OpenXPlay Flasher.
- macOS 12 to 14: Control-click the app, choose Open, then Open again.
- Or remove the quarantine flag in Terminal:
  `xattr -dr com.apple.quarantine "/Applications/OpenXPlay Flasher.app"`

**Windows 10 or 11** (`openxplay-flasher-windows-amd64.exe`, or `-arm64.exe` for Arm PCs): when
"Windows protected your PC" appears, choose **More info**, then **Run anyway**. If the app reports
that the Microsoft Edge WebView2 Runtime is missing, install it and start the app again.

## Build

Requirements: Go 1.26 or later; on macOS the Xcode command line tools, pkg-config and, for
`make build` and the tests, libusb (`brew install libusb pkg-config`); Node.js 22 for the UI tests.
`make app` builds its own universal libusb for macOS 12 and later (downloaded once) and the app with
Go 1.26, the last Go that runs on macOS 12.

```bash
make app        # macOS: dist/OpenXPlay Flasher.app (universal; SIGN_IDENTITY=... to sign)
make dmg        # macOS: the disk image; make notarize submits it to Apple
make windows    # Windows exes in dist/ (cross-built without cgo; lists USB devices through SetupAPI)
make test       # Go, UI and cross-platform checks
make dev        # run the app with the page read from internal/gui/assets
make web        # serve the page to a browser on this computer, for UI work
```

`openxplay-flasher help` lists the command-line commands (`devices`, `watch`, `profiles`, ...).

## Contributing

Contributions are accepted under the [Contributor License Agreement](./CLA.md). The CLA Assistant
check on each pull request records your acceptance.

## License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPLv3)](./LICENSE),
version 3 only.

Additional terms under AGPLv3 Section 7 apply to the material contributed by this project. Modified
versions must preserve the attribution notice `Powered by OpenXPlay.` in the appropriate legal
notices and in any prominent about, legal, footer, or attribution location presented by the user
interface, together with a visible link to the original project:
<https://github.com/OpenXPlay/OpenXPlay-Flasher>. See [NOTICE](./NOTICE).

Third-party components keep their own licences; see
[THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md).

If your organization's policies do not permit the use of AGPLv3-licensed software, or if you wish
to avoid the open-source obligations of AGPLv3, please contact us at:
[support@openxplay.org](mailto:support@openxplay.org).

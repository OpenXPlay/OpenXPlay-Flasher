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

## Build

Requirements: Go 1.26 or later; on macOS the Xcode command line tools and libusb
(`brew install libusb pkg-config`); Node.js 22 for the UI tests.

```bash
make app        # macOS: dist/OpenXPlay Flasher.app
make windows    # Windows exes in dist/ (cross-built, no cgo; USB access on Windows is not done yet)
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

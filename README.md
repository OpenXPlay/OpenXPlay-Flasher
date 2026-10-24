<p align="center"><b>English</b> · <a href="README-zh.md">简体中文</a></p>

# OpenXPlay-Flasher

A desktop app for Windows and macOS that installs the OpenXPlay relay on supported devices and keeps
it up to date. Each device's system is a stack of layers (for example base system, device support,
relay). The flasher can install every layer from scratch or update a single layer on its own, which
is usually only the relay. Its Configuration view reads and changes the configuration files of a
connected Linux relay over USB: the device keeps a backup of each version, and if the relay does not
stay up after a change, the previous version is put back.

> Preview release. It installs the base system with fastboot, and device support and the relay over
> adb. Windows uses native WinUSB for both protocols; an external adb server is not required, and
> an already-running adb server can still be used. It never downloads packages: Prepare lists each
> file with its download links, you choose the files you downloaded, and they are checked by SHA-256
> against the signed package list. On Windows, a complete UFI003 installation from stock firmware
> and a relay upgrade on an existing OpenStick system have both been verified on hardware.

| Device | Layers |
| --- | --- |
| UFI003 4G USB dongle (OpenStick) | OpenStick v1, device support, OpenXPlay relay |

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

Windows lists attached USB devices through SetupAPI, which does not require an adb driver. Native
adb and fastboot communication requires the matching device interface to use the **WinUSB** driver.
If a listed device cannot be opened, the error identifies the interface and explains that its driver
must be checked. In Device Manager, check the driver for the **ADB interface** or, after entering
the bootloader, the **fastboot interface**. Install a compatible WinUSB driver for that interface;
the driver may need to be installed separately for each mode. On a composite OpenStick device,
keep the **RNDIS network adapter** and its network driver intact: change only the adb or fastboot
interface, never the whole composite device. An already-running adb server that can access the
device remains another supported connection path.

### Windows driver prompts and installation

In 0.2.0-beta.2, failures while reading a device or opening fastboot appear in the app's error area.
A missing registered WinUSB interface produces text such as `needs a WinUSB driver`,
`install the ADB/fastboot WinUSB driver` or `has no WinUSB … interface`. These low-level details
may remain in English. The app does not download or install drivers and has no one-click driver installer.

1. Find this UFI003's ADB or fastboot interface in Device Manager. Open Properties → Details → Hardware Ids and check the ID for its current mode. Do not select the RNDIS adapter or the whole USB composite device.
2. Download and extract a signed driver matching that hardware ID, Windows version and architecture. For the fastboot ID `USB\VID_18D1&PID_D00D`, the [Android Bootloader Interface driver from Microsoft Update Catalog](https://www.catalog.update.microsoft.com/ScopedViewInline.aspx?updateid=76f2c233-6100-4a64-8bee-113c2da0991d) has been verified on Windows 11. Other IDs need their own matching driver.
3. Right-click that interface, choose Update driver → Browse my computer for drivers, and select the extracted folder. See [Microsoft's manual installation instructions](https://support.microsoft.com/en-us/windows/update-drivers-through-device-manager-in-windows-ec62f46c-ff14-c91d-eead-d7126dc1f7b6). Keep driver signature enforcement enabled.
4. Return to Flasher and choose Read again. ADB and fastboot are separate interfaces, so the newly appearing fastboot interface may need another driver installation. If an install job has failed, follow the app's instructions to read the device again and choose Repair; do not flash partitions manually.

If plugging the dongle in adds no USB device in Device Manager, check power, the USB connection and a direct USB port first. This differs from a listed device whose communication driver is missing. See the [Windows installation and troubleshooting guide](https://openxplay.org/en/ufi003-relay#flasher-windows-drivers).

## Build

Requirements: Go 1.26 or later; on macOS the Xcode command line tools, pkg-config and, for
`make build` and the tests, libusb (`brew install libusb pkg-config`); Node.js 22 for the UI tests.
`make app` builds its own universal libusb for macOS 12 and later (downloaded once) and the app with
Go 1.26, the last Go that runs on macOS 12.

```bash
make app        # macOS: dist/OpenXPlay Flasher.app (universal; SIGN_IDENTITY=... to sign)
make dmg        # macOS: the disk image; make notarize submits it to Apple
make windows    # Windows exes in dist/ (without cgo; SetupAPI listing, WinUSB adb/fastboot)
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

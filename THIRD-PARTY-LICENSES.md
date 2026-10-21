# Third-party licences

Components linked into or shipped with OpenXPlay Flasher builds. Each keeps its own licence; the
licence texts are in the module sources listed.

| Component | Version | Licence | Used in | Source |
| --- | --- | --- | --- | --- |
| Go standard library | go.mod | BSD-3-Clause | all builds | https://go.dev/ |
| Wails | v3.0.0-beta.34 | MIT | app builds | https://github.com/wailsapp/wails |
| Wails `internal/go-common-file-dialog` (Harry Phillips) | v3.0.0-beta.34 | MIT | Windows app | https://github.com/wailsapp/wails |
| Wails `internal/webview2/webviewloader` (John Chadwick, Wails Project Developers) | v3.0.0-beta.34 | ISC | Windows app | https://github.com/wailsapp/wails |
| gousb | v1.1.3 | Apache-2.0 | macOS and Linux builds | https://github.com/google/gousb |
| libusb | 1.0 (dynamic library, inside the macOS app bundle) | LGPL-2.1-or-later | macOS app | https://github.com/libusb/libusb |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause | all builds | https://go.googlesource.com/sys |
| adrg/xdg | v0.5.3 | MIT | app builds | https://github.com/adrg/xdg |
| go-ole | v1.3.0 | MIT | Windows app | https://github.com/go-ole/go-ole |
| mattn/go-colorable | v0.1.14 | MIT | Windows app | https://github.com/mattn/go-colorable |
| mattn/go-isatty | v0.0.20 | MIT | app builds | https://github.com/mattn/go-isatty |

libusb is linked dynamically and ships as a separate file (`Contents/Frameworks/libusb-1.0.0.dylib`),
which you may replace with your own build of libusb under the terms of the LGPL.

The application structure (Wails as the native shell around one Go `http.Handler` that serves the
embedded page and a JSON API) follows magpie, Copyright (c) 2026 yetone, MIT License,
https://github.com/yetone/magpie.

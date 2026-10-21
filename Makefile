# SPDX-FileCopyrightText: 2026 OpenXPlay.org
# SPDX-License-Identifier: AGPL-3.0-only
# Created by riko chen on 2026-10-20

# The desktop app needs cgo on macOS (Wails' WebKit window and libusb through gousb) and builds
# without cgo on Windows (Wails' WebView2 needs none; USB access there comes with a pure-Go WinUSB
# backend later). -tags nogui leaves Wails out for the command-line build.

BIN       := openxplay-flasher
APP       := OpenXPlay Flasher.app
PKG       := github.com/OpenXPlay/OpenXPlay-Flasher/internal/version
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//; s/[^A-Za-z0-9._+-]/-/g')
VERSION   := $(or $(VERSION),dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildDate=$(BUILD_DATE)
TAGS      := production
# Numeric part of the version, for Windows file versions and the macOS bundle version, which
# take numbers only (0.1.0-rc.1 -> 0.1.0).
NUMVER    := $(shell echo '$(VERSION)' | sed -n 's/^\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')

# macOS: keep in step with LSMinimumSystemVersion in build/darwin/Info.plist.
MACOS_MIN := 12.0
export CGO_CFLAGS  += -mmacosx-version-min=$(MACOS_MIN)
export CGO_LDFLAGS += -mmacosx-version-min=$(MACOS_MIN)
# The app is built with the last Go that runs on macOS 12 (Go 1.27 needs macOS 13), against the
# libusb that build/libusb.sh builds (keep the version in step with it).
APP_GO    := go1.26.10
LIBUSB    := $(CURDIR)/build/.cache/libusb-1.0.30
DMG       := dist/OpenXPlay-Flasher-$(VERSION)-macos-universal.dmg
# Code signing: - is ad hoc; a Developer ID Application identity signs for release, with the
# hardened runtime and a secure timestamp.
SIGN_IDENTITY ?= -
SIGN      := codesign --force --sign "$(SIGN_IDENTITY)" $(if $(filter-out -,$(SIGN_IDENTITY)),--options runtime --timestamp)

.PHONY: build dev web cli catalog-tool test test-go test-ui vet-cross app dmg notarize windows checksums icons clean

build:
	go build -tags "$(TAGS)" -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) .

# The app with the page read from internal/gui/assets on every request: edit, then reload.
dev:
	go run -tags dev . app

# The page in a browser with its developer tools; prints the address to open.
web:
	go run -tags dev . web

cli:
	go build -tags nogui -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) .

# A separate maintainer tool; it has no GUI, USB or application dependencies.
catalog-tool:
	@mkdir -p dist
	go build -trimpath -o dist/oxp-catalog ./cmd/oxp-catalog

test: test-go test-ui vet-cross

test-go:
	go vet ./...
	go test ./...
	go test -tags nogui ./...

test-ui:
	node --test internal/gui/tests/*.test.mjs

# A cross build skips _test.go files; vet compiles them.
vet-cross:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet -tags nogui ./...

# macOS app bundle in dist/: one universal binary (arm64, x86_64) with libusb inside the bundle, so
# the app runs without Homebrew. make build stays host-only and uses the libusb pkg-config finds.
app:
	@bash build/libusb.sh >/dev/null
	@rm -rf "dist/$(APP)"
	@mkdir -p "dist/$(APP)/Contents/MacOS" "dist/$(APP)/Contents/Resources" "dist/$(APP)/Contents/Frameworks"
	@for a in arm64:arm64 amd64:x86_64; do goarch=$${a%:*} arch=$${a#*:}; \
		GOTOOLCHAIN=$(APP_GO) CGO_ENABLED=1 GOOS=darwin GOARCH=$$goarch CC="clang -arch $$arch" \
		CGO_CFLAGS="-arch $$arch -mmacosx-version-min=$(MACOS_MIN)" \
		CGO_LDFLAGS="-arch $$arch -mmacosx-version-min=$(MACOS_MIN)" \
		PKG_CONFIG_PATH="$(LIBUSB)/$$arch/lib/pkgconfig" PKG_CONFIG_LIBDIR="$(LIBUSB)/$$arch/lib/pkgconfig" \
		go build -tags "$(TAGS)" -trimpath -o dist/$(BIN)-darwin-$$goarch \
			-ldflags="$(LDFLAGS) -extldflags=-Wl,-rpath,@executable_path/../Frameworks" . || exit 1; \
	done
	@lipo -create -output "dist/$(APP)/Contents/MacOS/$(BIN)" dist/$(BIN)-darwin-arm64 dist/$(BIN)-darwin-amd64
	@rm -f dist/$(BIN)-darwin-arm64 dist/$(BIN)-darwin-amd64
	@cp "$(LIBUSB)/universal/libusb-1.0.0.dylib" "dist/$(APP)/Contents/Frameworks/libusb-1.0.0.dylib"
	@cp build/darwin/$(BIN).icns "dist/$(APP)/Contents/Resources/$(BIN).icns"
	@sed 's/@VERSION@/$(or $(NUMVER),0.0.0)/' build/darwin/Info.plist > "dist/$(APP)/Contents/Info.plist"
	@$(SIGN) "dist/$(APP)/Contents/Frameworks/libusb-1.0.0.dylib"
	@$(SIGN) "dist/$(APP)"
	@codesign --verify --deep --strict "dist/$(APP)"
	@echo "  dist/$(APP)"

# Disk image with the app and a link to /Applications, signed unless SIGN_IDENTITY is ad hoc.
dmg: app
	@rm -rf dist/dmg "$(DMG)" && mkdir -p dist/dmg
	@ditto "dist/$(APP)" "dist/dmg/$(APP)" && ln -s /Applications dist/dmg/Applications
	@hdiutil create -quiet -volname "OpenXPlay Flasher" -srcfolder dist/dmg -fs HFS+ -format UDZO -ov "$(DMG)"
	@rm -rf dist/dmg
	@if [ "$(SIGN_IDENTITY)" != "-" ]; then codesign --force --timestamp --sign "$(SIGN_IDENTITY)" "$(DMG)"; fi
	@echo "  $(DMG)"

# Notarizes and staples the disk image made by make dmg. Credentials: NOTARY_PROFILE (a notarytool
# keychain profile), or an App Store Connect API key: NOTARY_KEY (.p8 path), NOTARY_KEY_ID and,
# for a team key, NOTARY_ISSUER. notarytool can exit 0 for a rejected upload; stapling then fails.
notarize:
	@test -f "$(DMG)" || { echo "$(DMG) is missing: run make dmg"; exit 1; }
	@if [ -n "$(NOTARY_PROFILE)" ]; then \
		xcrun notarytool submit "$(DMG)" --wait --keychain-profile "$(NOTARY_PROFILE)"; \
	elif [ -n "$(NOTARY_KEY)" ] && [ -n "$(NOTARY_KEY_ID)" ]; then \
		xcrun notarytool submit "$(DMG)" --wait --key "$(NOTARY_KEY)" --key-id "$(NOTARY_KEY_ID)" \
			$(if $(NOTARY_ISSUER),--issuer "$(NOTARY_ISSUER)"); \
	else echo "set NOTARY_PROFILE, or NOTARY_KEY, NOTARY_KEY_ID and NOTARY_ISSUER"; exit 1; fi
	xcrun stapler staple "$(DMG)"

windows:
	@mkdir -p dist
	go run github.com/tc-hib/go-winres@v0.3.3 make --in build/windows/winres.json --arch amd64,arm64 \
		--out rsrc --file-version "$(or $(NUMVER),0.0.0)" --product-version "$(or $(NUMVER),0.0.0)"
	@for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build -tags "$(TAGS)" -trimpath \
			-ldflags="$(LDFLAGS) -H windowsgui" -o dist/$(BIN)-windows-$$arch.exe . || exit 1; \
	done; rm -f rsrc_windows_*.syso

# SHA256SUMS for the release files in dist/.
checksums:
	@cd dist && files=$$(ls *.dmg *.exe *.tar.bz2 2>/dev/null); \
	test -n "$$files" || { echo "no release files in dist/"; exit 1; }; \
	shasum -a 256 $$files > SHA256SUMS && cat SHA256SUMS

# Regenerates the committed icons from build/icon/flasher-art.png, the full-bleed artwork: the
# squircle masters (macOS with Apple's 100 px grid margin, and full size for Windows and the page),
# then every size from them (macOS: sips and iconutil).
icons:
	@go run build/icon/mask.go -in build/icon/flasher-art.png -out build/icon/flasher-macos.png -margin 100
	@go run build/icon/mask.go -in build/icon/flasher-art.png -out build/icon/flasher.png
	@rm -rf build/darwin/$(BIN).iconset && mkdir -p build/darwin/$(BIN).iconset
	@for s in 16 32 128 256 512; do d=$$((s*2)); \
		sips -z $$s $$s build/icon/flasher-macos.png --out build/darwin/$(BIN).iconset/icon_$${s}x$${s}.png >/dev/null; \
		sips -z $$d $$d build/icon/flasher-macos.png --out build/darwin/$(BIN).iconset/icon_$${s}x$${s}@2x.png >/dev/null; \
	done
	@iconutil -c icns build/darwin/$(BIN).iconset -o build/darwin/$(BIN).icns && rm -rf build/darwin/$(BIN).iconset
	@for s in 16 32 48 64 128 256; do \
		sips -z $$s $$s build/icon/flasher.png --out build/windows/icon-$$s.png >/dev/null; \
	done
	@sips -z 128 128 build/icon/flasher.png --out internal/gui/assets/logo.png >/dev/null
	@sips -z 256 256 build/icon/flasher.png --out internal/gui/icon.png >/dev/null

clean:
	rm -rf $(BIN) dist rsrc_windows_*.syso

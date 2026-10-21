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
LDFLAGS   := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT)
TAGS      := production
# Numeric part of the version for Windows file versions (0.1.0-rc.1 -> 0.1.0).
WINVER    := $(shell echo '$(VERSION)' | sed -n 's/^\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')

# macOS: keep in step with LSMinimumSystemVersion in build/darwin/Info.plist.
MACOS_MIN := 12.0
export CGO_CFLAGS  += -mmacosx-version-min=$(MACOS_MIN)
export CGO_LDFLAGS += -mmacosx-version-min=$(MACOS_MIN)

.PHONY: build dev web cli test test-go test-ui vet-cross app windows icons clean

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

# macOS app bundle in dist/, with libusb inside it so the app runs without Homebrew. The bundle is
# signed ad hoc only (arm64 needs a signature to start); release signing comes later.
app: build
	@rm -rf "dist/$(APP)"
	@mkdir -p "dist/$(APP)/Contents/MacOS" "dist/$(APP)/Contents/Resources" "dist/$(APP)/Contents/Frameworks"
	@cp $(BIN) "dist/$(APP)/Contents/MacOS/$(BIN)"
	@cp build/darwin/$(BIN).icns "dist/$(APP)/Contents/Resources/$(BIN).icns"
	@sed 's/@VERSION@/$(VERSION)/' build/darwin/Info.plist > "dist/$(APP)/Contents/Info.plist"
	@lib=$$(otool -L $(BIN) | awk '/libusb-1\.0/ {print $$1; exit}'); \
	test -n "$$lib" || { echo "the binary does not link libusb"; exit 1; }; \
	cp "$$lib" "dist/$(APP)/Contents/Frameworks/libusb-1.0.0.dylib"; \
	chmod u+w "dist/$(APP)/Contents/Frameworks/libusb-1.0.0.dylib"; \
	install_name_tool -id @rpath/libusb-1.0.0.dylib "dist/$(APP)/Contents/Frameworks/libusb-1.0.0.dylib"; \
	install_name_tool -change "$$lib" @executable_path/../Frameworks/libusb-1.0.0.dylib "dist/$(APP)/Contents/MacOS/$(BIN)"
	@codesign --force --sign - "dist/$(APP)/Contents/Frameworks/libusb-1.0.0.dylib"
	@codesign --force --sign - "dist/$(APP)"
	@echo "  dist/$(APP)"

windows:
	@mkdir -p dist
	go run github.com/tc-hib/go-winres@v0.3.3 make --in build/windows/winres.json --arch amd64,arm64 \
		--out rsrc --file-version "$(or $(WINVER),0.0.0)" --product-version "$(or $(WINVER),0.0.0)"
	@for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build -tags "$(TAGS)" -trimpath \
			-ldflags="$(LDFLAGS) -H windowsgui" -o dist/$(BIN)-windows-$$arch.exe . || exit 1; \
	done; rm -f rsrc_windows_*.syso

# Regenerates the committed icons from build/icon/openxplay.png (macOS: sips and iconutil).
icons:
	@rm -rf build/darwin/$(BIN).iconset && mkdir -p build/darwin/$(BIN).iconset
	@for s in 16 32 128 256 512; do \
		sips -z $$s $$s build/icon/openxplay.png --out build/darwin/$(BIN).iconset/icon_$${s}x$${s}.png >/dev/null; \
		if [ $$s -lt 512 ]; then d=$$((s*2)); \
			sips -z $$d $$d build/icon/openxplay.png --out build/darwin/$(BIN).iconset/icon_$${s}x$${s}@2x.png >/dev/null; fi; \
	done
	@iconutil -c icns build/darwin/$(BIN).iconset -o build/darwin/$(BIN).icns && rm -rf build/darwin/$(BIN).iconset
	@for s in 16 32 48 64 128 256; do \
		sips -z $$s $$s build/icon/openxplay.png --out build/windows/icon-$$s.png >/dev/null; \
	done
	@sips -z 128 128 build/icon/openxplay.png --out internal/gui/assets/logo.png >/dev/null
	@sips -z 256 256 build/icon/openxplay.png --out internal/gui/icon.png >/dev/null

clean:
	rm -rf $(BIN) dist rsrc_windows_*.syso

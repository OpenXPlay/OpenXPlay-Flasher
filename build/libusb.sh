#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 OpenXPlay.org
# SPDX-License-Identifier: AGPL-3.0-only
# Created by riko chen on 2026-10-20

# Builds libusb for the macOS app from its unmodified release tarball: a shared library for arm64
# and one for x86_64, both for macOS 12 and later, and a universal dylib from the two for the app
# bundle. Everything goes to build/.cache/libusb-<version>/; work already done is skipped. Prints
# the pkg-config directory of each architecture. Keep the version in step with LIBUSB in Makefile.

set -euo pipefail

version=1.0.30
sha256=fea36f34f9156400209595e300840767ab1a385ede1dc7ee893015aea9c6dbaf
url=https://github.com/libusb/libusb/releases/download/v$version/libusb-$version.tar.bz2
macos_min=12.0
id=@rpath/libusb-1.0.0.dylib
# Newer SDKs declare pipe2 (macOS 27 and later only); without this, libusb would call it, weakly
# linked and missing, on older systems.
configure_vars=(ac_cv_func_pipe2=no)

cache=$(cd "$(dirname "$0")" && pwd)/.cache/libusb-$version
tarball=$cache/libusb-$version.tar.bz2
universal=$cache/universal/libusb-1.0.0.dylib
stamp="$version $sha256 macOS $macos_min $id ${configure_vars[*]}"
mkdir -p "$cache"

if ! { [ -f "$tarball" ] && shasum -a 256 -c -s <<<"$sha256  $tarball"; }; then
	echo "libusb: downloading $url" >&2
	curl -fsSL --retry 3 -o "$tarball.part" "$url"
	shasum -a 256 -c -s <<<"$sha256  $tarball.part" || {
		echo "libusb: checksum mismatch for $url" >&2
		rm -f "$tarball.part"
		exit 1
	}
	mv "$tarball.part" "$tarball"
fi

for arch in arm64 x86_64; do
	prefix=$cache/$arch
	if [ "$(cat "$prefix/.done" 2>/dev/null)" = "$stamp" ]; then continue; fi
	echo "libusb: building $arch for macOS $macos_min" >&2
	case $arch in arm64) host=aarch64-apple-darwin ;; x86_64) host=x86_64-apple-darwin ;; esac
	src=$cache/src-$arch
	rm -rf "$prefix" "$src" && mkdir -p "$src"
	tar -xjf "$tarball" -C "$src" --strip-components 1
	log=$cache/build-$arch.log
	if ! (
		cd "$src"
		flags="-arch $arch -mmacosx-version-min=$macos_min"
		MACOSX_DEPLOYMENT_TARGET=$macos_min ./configure --host="$host" --prefix="$prefix" \
			--disable-static --disable-dependency-tracking "${configure_vars[@]}" \
			CC="clang -arch $arch" CFLAGS="$flags -O2" LDFLAGS="$flags"
		MACOSX_DEPLOYMENT_TARGET=$macos_min make -j"$(sysctl -n hw.ncpu)" install
	) >"$log" 2>&1; then
		tail -n 40 "$log" >&2
		echo "libusb: $arch build failed, full log in $log" >&2
		exit 1
	fi
	install_name_tool -id "$id" "$prefix/lib/libusb-1.0.0.dylib"
	codesign --force --sign - "$prefix/lib/libusb-1.0.0.dylib" 2>/dev/null
	rm -rf "$src"
	echo "$stamp" >"$prefix/.done"
	rm -f "$universal"
done

if [ ! -f "$universal" ]; then
	mkdir -p "$(dirname "$universal")"
	lipo -create -output "$universal" "$cache/arm64/lib/libusb-1.0.0.dylib" "$cache/x86_64/lib/libusb-1.0.0.dylib"
	codesign --force --sign - "$universal" 2>/dev/null
fi

for arch in arm64 x86_64; do
	echo "$arch $cache/$arch/lib/pkgconfig"
done

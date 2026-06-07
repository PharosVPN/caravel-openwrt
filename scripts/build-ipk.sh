#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 The PharosVPN Authors
#
# Hand-assemble opkg .ipk packages without the full OpenWrt SDK. An .ipk is an
# `ar` archive of three members — debian-binary, control.tar.gz, data.tar.gz —
# a well-defined format opkg installs directly. This keeps the cross-arch matrix
# in our own CI (design §5.1) and lets a luci-app `all` package be produced on
# any host with tar+gzip+ar.
#
#   scripts/build-ipk.sh luci            # luci-app-pharos-client (arch: all)
#   scripts/build-ipk.sh caravel amd64   # pharos-caravel for a Go arch
#   scripts/build-ipk.sh all amd64       # both
#
# Output: dist/*.ipk
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="$(tr -d '[:space:]' < VERSION)"
RELEASE=1
DIST=dist
mkdir -p "$DIST"

# need <tool>… — fail early if a required tool is missing.
need() { for t in "$@"; do command -v "$t" >/dev/null || { echo "missing tool: $t" >&2; exit 1; }; done; }
need tar gzip

# make_ipk <out> <debian-binary> <control.tar.gz> <data.tar.gz> — wrap the three
# members into the package container. Modern OpenWrt opkg (24.10) reads an .ipk
# as a *gzipped tar* of ./debian-binary + ./control.tar.gz + ./data.tar.gz — NOT
# the Debian `ar` format. (Verified against a feed package on the dev VM: the
# magic is 1f 8b, a gzip stream, whose tar holds the three members.)
make_ipk() {
	local out="$1" deb="$2" ctrl="$3" data="$4"
	local d; d="$(mktemp -d)"
	cp "$deb" "$d/debian-binary"
	cp "$ctrl" "$d/control.tar.gz"
	cp "$data" "$d/data.tar.gz"
	# Member order matches the feed packages (debian-binary, data, control); opkg
	# is order-tolerant but we mirror the upstream layout. ustar, deterministic.
	tar $TARFMT $TAR_NO_XATTR --numeric-owner --owner=0 --group=0 -C "$d" -cf - \
		./debian-binary ./data.tar.gz ./control.tar.gz | gzip -9n > "$out"
	rm -rf "$d"
}

# TARFMT forces plain ustar (no PAX extended headers): opkg's minimal tar reader
# rejects PAX typeflag 'x' (0x78), which BSD/macOS tar emits by default. GNU tar
# accepts the same flag. We also drop extended attrs / resource forks.
TARFMT="--format=ustar"
case "$(uname -s)" in
	Darwin) TAR_NO_XATTR="--no-mac-metadata --no-xattrs" ;;
	*)      TAR_NO_XATTR="" ;;
esac

# tar_gz <srcdir> <out.tar.gz> — deterministic gzip'd ustar tarball of a tree's
# contents (./ root), owned by root, matching opkg's expectations.
tar_gz() {
	local src="$1" out="$2"
	tar $TARFMT $TAR_NO_XATTR --numeric-owner --owner=0 --group=0 -C "$src" -cf - ./ | gzip -9n > "$out"
}

# Map a Go arch to the OpenWrt package Architecture string (control file).
owrt_arch() {
	case "$1" in
		amd64)  echo "x86_64" ;;
		arm64)  echo "aarch64_generic" ;;
		armv7)  echo "arm_cortex-a15_neon-vfpv4" ;;
		mipsle) echo "mipsel_24kc" ;;
		mips)   echo "mips_24kc" ;;
		all)    echo "all" ;;
		*) echo "$1" ;;
	esac
}

# assemble <pkgname> <arch> <depends> <stagedir> <description>
# Builds dist/<pkgname>_<version>-<release>_<arch>.ipk from a staged file tree.
assemble() {
	local pkg="$1" arch="$2" depends="$3" stage="$4" descr="$5"
	local work; work="$(mktemp -d)"
	local ctrl="$work/control"
	mkdir -p "$ctrl"

	# Installed-Size is the byte total of the staged files (BSD/GNU-portable: sum
	# file sizes rather than rely on `du -b`, which BSD du lacks).
	local isize; isize="$(find "$stage" -type f ! -path '*/.control/*' -exec cat {} + 2>/dev/null | wc -c | tr -d ' ')"

	cat > "$ctrl/control" <<EOF
Package: $pkg
Version: $VERSION-$RELEASE
Depends: $depends
Source: feeds/pharos/$pkg
SourceName: $pkg
License: Apache-2.0
Section: net
SourceDateEpoch: 0
Maintainer: The PharosVPN Authors <noreply@pharosvpn.dev>
Architecture: $arch
Installed-Size: $isize
Description: $descr
EOF

	# Preserve conffiles / postinst if the stage shipped them as control.* files.
	[ -f "$stage/.control/conffiles" ] && cp "$stage/.control/conffiles" "$ctrl/conffiles"
	[ -f "$stage/.control/postinst" ]  && { cp "$stage/.control/postinst"  "$ctrl/postinst";  chmod 755 "$ctrl/postinst";  }
	[ -f "$stage/.control/prerm" ]     && { cp "$stage/.control/prerm"     "$ctrl/prerm";     chmod 755 "$ctrl/prerm";     }
	rm -rf "$stage/.control"

	tar_gz "$ctrl"  "$work/control.tar.gz"
	tar_gz "$stage" "$work/data.tar.gz"
	echo "2.0" > "$work/debian-binary"

	local out="$DIST/${pkg}_${VERSION}-${RELEASE}_${arch}.ipk"
	local abs_out; abs_out="$(pwd)/$out"
	rm -f "$abs_out"
	make_ipk "$abs_out" "$work/debian-binary" "$work/control.tar.gz" "$work/data.tar.gz"
	rm -rf "$work"
	echo "→ $out  ($(du -h "$out" | cut -f1))"
}

build_luci() {
	local src=packaging/luci-app-pharos-client/files
	local stage; stage="$(mktemp -d)"
	mkdir -p "$stage/www/luci-static/resources/view/pharos" \
	         "$stage/usr/share/luci/menu.d" \
	         "$stage/usr/share/rpcd/acl.d" \
	         "$stage/usr/share/rpcd/ucode"
	cp "$src"/www/luci-static/resources/view/pharos/*.js "$stage/www/luci-static/resources/view/pharos/"
	cp "$src"/usr/share/luci/menu.d/luci-app-pharos-client.json "$stage/usr/share/luci/menu.d/"
	cp "$src"/usr/share/rpcd/acl.d/luci-app-pharos-client.json  "$stage/usr/share/rpcd/acl.d/"
	cp "$src"/usr/share/rpcd/ucode/pharos                       "$stage/usr/share/rpcd/ucode/pharos"

	# Reload rpcd + restart uhttpd so the new ubus object + menu show up.
	mkdir -p "$stage/.control"
	cat > "$stage/.control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
/etc/init.d/rpcd reload 2>/dev/null || /etc/init.d/rpcd restart 2>/dev/null
rm -f /tmp/luci-indexcache* /tmp/luci-modulecache/* 2>/dev/null
exit 0
EOF
	assemble "luci-app-pharos-client" "$(owrt_arch all)" \
		"pharos-caravel, rpcd-mod-ucode" "$stage" \
		"PharosVPN client web UI (profiles + connection) for LuCI."
	rm -rf "$stage"
}

build_caravel() {
	local goarch="$1"
	local bin="bin/caravel-owrt-${goarch}"
	[ -f "$bin" ] || { echo "missing $bin — run scripts/build.sh ${goarch} first" >&2; exit 1; }
	local src=packaging/pharos-caravel/files
	local stage; stage="$(mktemp -d)"
	mkdir -p "$stage/usr/sbin" "$stage/etc/init.d" "$stage/etc/config" "$stage/etc/uci-defaults"
	install -m 0755 "$bin" "$stage/usr/sbin/caravel-owrt"
	install -m 0755 "$src/etc/init.d/pharosvpn" "$stage/etc/init.d/pharosvpn"
	install -m 0644 "$src/etc/config/pharosvpn" "$stage/etc/config/pharosvpn"
	install -m 0755 "$src/etc/uci-defaults/99-pharos-firewall" "$stage/etc/uci-defaults/99-pharos-firewall"

	mkdir -p "$stage/.control"
	printf '/etc/config/pharosvpn\n' > "$stage/.control/conffiles"
	# Apply the firewall uci-defaults on install (opkg runs them at first boot,
	# but on a live router we trigger them now).
	cat > "$stage/.control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
[ -x /etc/uci-defaults/99-pharos-firewall ] && {
	sh /etc/uci-defaults/99-pharos-firewall && rm -f /etc/uci-defaults/99-pharos-firewall
}
exit 0
EOF
	assemble "pharos-caravel" "$(owrt_arch "$goarch")" \
		"kmod-tun, ip-full" "$stage" \
		"PharosVPN client (userspace AmneziaWG over kmod-tun)."
	rm -rf "$stage"
}

case "${1:-all}" in
	luci)    build_luci ;;
	caravel) build_caravel "${2:-amd64}" ;;
	all)     build_luci; build_caravel "${2:-amd64}" ;;
	*) echo "usage: $0 {luci|caravel <goarch>|all <goarch>}" >&2; exit 2 ;;
esac

echo
ls -lh "$DIST"

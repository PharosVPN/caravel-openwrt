#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 The PharosVPN Authors
#
# Cross-compile the caravel-owrt client as a static, CGO-free binary for OpenWrt
# targets. amneziawg-go is pure Go, so every target is just a GOARCH/GOMIPS knob
# — no per-kernel rebuild (design §2.2, §7).
#
#   scripts/build.sh            # default: the dev VM (linux/amd64)
#   scripts/build.sh arm64      # aarch64 routers
#   scripts/build.sh mipsle     # little-endian MIPS (softfloat)
#   scripts/build.sh all        # the whole matrix
#
# Output: bin/caravel-owrt-<arch>  (consumed by packaging/pharos-caravel/Makefile)
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=bin
mkdir -p "$OUT"
VERSION="$(tr -d '[:space:]' < VERSION)"
LDFLAGS="-s -w -X main.version=${VERSION}"

build() { # <goarch> [KEY=VAL ...extra env]
	local goarch="$1"; shift
	echo "→ building linux/${goarch} $*"
	env CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" "$@" \
		go build -trimpath -ldflags "$LDFLAGS" \
		-o "$OUT/caravel-owrt-${goarch}" ./cmd/caravel-owrt
}

case "${1:-amd64}" in
	amd64)  build amd64 ;;
	arm64)  build arm64 ;;
	armv7)  build arm GOARM=7 ;;
	mipsle) build mipsle GOMIPS=softfloat ;;
	mips)   build mips   GOMIPS=softfloat ;;
	all)
		build amd64
		build arm64
		build arm GOARM=7
		build mipsle GOMIPS=softfloat
		build mips   GOMIPS=softfloat
		;;
	*) echo "usage: $0 [amd64|arm64|armv7|mipsle|mips|all]" >&2; exit 2 ;;
esac

echo
ls -lh "$OUT"

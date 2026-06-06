# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 The PharosVPN Authors
#
# OpenWrt package for the PharosVPN client. The Go binary is cross-compiled
# out-of-band (scripts/build.sh) and dropped at bin/caravel-owrt-$(ARCH); this
# Makefile just stages the prebuilt binary + config into the .ipk. Keeping the
# cross-arch matrix in our own CI avoids SDK Go-version drift (design §5.1).
#
# Build with the OpenWrt SDK for your target:
#   ln -s $(pwd)/packaging/pharos-caravel  <sdk>/package/pharos-caravel
#   make -C <sdk> package/pharos-caravel/compile V=s

include $(TOPDIR)/rules.mk

PKG_NAME:=pharos-caravel
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
PKG_MAINTAINER:=The PharosVPN Authors <noreply@pharosvpn.dev>
PKG_LICENSE:=Apache-2.0

include $(INCLUDE_DIR)/package.mk

# scripts/build.sh names its outputs by Go GOARCH (amd64/arm64/arm/mipsle/mips),
# but the SDK's $(ARCH) is the compiler arch (x86_64/aarch64/arm/mipsel/mips).
# Map the two so the default points at the binary build.sh actually produced.
PHAROS_GOARCH:=$(ARCH)
ifeq ($(ARCH),x86_64)
  PHAROS_GOARCH:=amd64
endif
ifeq ($(ARCH),aarch64)
  PHAROS_GOARCH:=arm64
endif
ifeq ($(ARCH),mipsel)
  PHAROS_GOARCH:=mipsle
endif

# Where scripts/build.sh left the static binary for this target. Override on the
# make command line if your layout differs:
#   make ... PHAROS_BIN=/abs/path/caravel-owrt-arm64
PHAROS_BIN?=$(CURDIR)/../../bin/caravel-owrt-$(PHAROS_GOARCH)

define Package/pharos-caravel
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=VPN
  TITLE:=PharosVPN client (userspace AmneziaWG)
  URL:=https://github.com/PharosVPN/caravel-openwrt
  DEPENDS:=+kmod-tun +ip-full
endef

define Package/pharos-caravel/description
  PharosVPN client for OpenWrt. Imports a .pharos profile and routes the router
  (and, with the bundled firewall config, the whole LAN) through an obfuscated
  AmneziaWG tunnel — the userspace amneziawg-go engine over kmod-tun, the same
  data plane the macOS/iOS/Android clients use. CGO-free static binary, ~8-20 MB;
  targets routers with >=128 MB storage.
endef

define Package/pharos-caravel/conffiles
/etc/config/pharosvpn
endef

# The binary is prebuilt; nothing to compile inside the SDK.
define Build/Compile
endef

define Package/pharos-caravel/install
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) $(PHAROS_BIN) $(1)/usr/sbin/caravel-owrt
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/pharosvpn $(1)/etc/init.d/pharosvpn
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/etc/config/pharosvpn $(1)/etc/config/pharosvpn
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/etc/uci-defaults/99-pharos-firewall $(1)/etc/uci-defaults/99-pharos-firewall
endef

$(eval $(call BuildPackage,pharos-caravel))

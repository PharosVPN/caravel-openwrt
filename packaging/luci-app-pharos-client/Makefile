# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 The PharosVPN Authors
#
# OpenWrt package: the LuCI web UI for the PharosVPN client. Pure JS + JSON +
# a ucode rpcd backend — Architecture: all (arch-independent; only the Go
# binary in pharos-caravel is per-arch). Design §5.2.
#
# Build with the OpenWrt SDK (LuCI feed present):
#   ln -s $(pwd)/packaging/luci-app-pharos-client <sdk>/package/luci-app-pharos-client
#   make -C <sdk> package/luci-app-pharos-client/compile V=s
#
# If the SDK has the luci feed, luci.mk wires the standard install dirs; we
# fall back to package.mk + an explicit install for a bare SDK.

include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-pharos-client
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
PKG_MAINTAINER:=The PharosVPN Authors <noreply@pharosvpn.dev>
PKG_LICENSE:=Apache-2.0

LUCI_TITLE:=PharosVPN client web UI (profiles + connection)
LUCI_DEPENDS:=+pharos-caravel +rpcd-mod-ucode
LUCI_PKGARCH:=all

# Use luci.mk when the LuCI feed is available (the normal case); it provides the
# standard /www + menu.d + acl.d install layout and the "all" arch.
LUCI_MK:=$(wildcard $(TOPDIR)/feeds/luci/luci.mk)
ifneq ($(LUCI_MK),)
  include $(LUCI_MK)
else
  # Bare-SDK fallback: define the package by hand.
  include $(INCLUDE_DIR)/package.mk

  define Package/luci-app-pharos-client
    SECTION:=luci
    CATEGORY:=LuCI
    SUBMENU:=3. Applications
    TITLE:=$(LUCI_TITLE)
    DEPENDS:=$(LUCI_DEPENDS)
    PKGARCH:=all
  endef

  define Package/luci-app-pharos-client/description
    The LuCI web UI for the PharosVPN OpenWrt client: upload/import .pharos
    profiles, run account sync, pick a node, toggle full-tunnel and kill-switch,
    connect/disconnect, and watch live status. Shells to caravel-owrt via a
    ucode rpcd backend; no crypto in the browser.
  endef

  define Build/Compile
  endef

  define Package/luci-app-pharos-client/install
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view/pharos
	$(INSTALL_DATA) ./files/www/luci-static/resources/view/pharos/*.js $(1)/www/luci-static/resources/view/pharos/
	$(INSTALL_DIR) $(1)/usr/share/luci/menu.d
	$(INSTALL_DATA) ./files/usr/share/luci/menu.d/luci-app-pharos-client.json $(1)/usr/share/luci/menu.d/
	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d
	$(INSTALL_DATA) ./files/usr/share/rpcd/acl.d/luci-app-pharos-client.json $(1)/usr/share/rpcd/acl.d/
	$(INSTALL_DIR) $(1)/usr/share/rpcd/ucode
	$(INSTALL_DATA) ./files/usr/share/rpcd/ucode/pharos $(1)/usr/share/rpcd/ucode/pharos
  endef

  $(eval $(call BuildPackage,luci-app-pharos-client))
endif

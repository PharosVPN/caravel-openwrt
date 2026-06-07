<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".assets/logo-inverse.svg">
    <img src=".assets/logo.svg" alt="PharosVPN" width="120" height="120">
  </picture>
</p>
# caravel-openwrt

PharosVPN for **OpenWRT** routers — turn a router into a PharosVPN endpoint.

> Part of [PharosVPN](https://github.com/PharosVPN). Design: [`docs/integrations/openwrt.md`](https://github.com/PharosVPN/docs/blob/main/integrations/openwrt.md).

Two modes (the same box can do either):

- **Client mode** — the router is a [caravel](https://github.com/PharosVPN/caravel)
  client: import a profile, bring up the obfuscated tunnel, and route the whole
  LAN through it, so one device protects every device behind it. For a multi-hop
  profile the router dials only the entry node; the controller routes the cascade.
- **Server mode** — the router runs a PharosVPN server component: a **node** (a
  VPN gateway others connect to), a **relay**, or **coxswain** (the controller).

## Data plane

Userspace **[amneziawg-go](https://github.com/amnezia-vpn/amneziawg-go)** over
`kmod-tun` — the *same* obfuscated-WireGuard engine the macOS / iOS / Android
clients use. OpenWRT ships no AmneziaWG kernel module, and stock WireGuard can't
carry the obfuscation parameters, so the userspace engine is the single data-plane
strategy. Cross-compiled `CGO_ENABLED=0`, portable across architectures.

## Architecture

- `cmd/caravel-owrt/` — the Go client worker, reusing `caravel/go`'s `profile` /
  `vp` / `sync` unchanged; the only OpenWRT-specific code is the `awg0` tun +
  `ip` routing in [`connect.go`](cmd/caravel-owrt/connect.go). Cross-compiled
  `CGO_ENABLED=0` per arch.
- `packaging/pharos-caravel/` — OpenWRT SDK Makefile + procd init + uci config +
  the fw4 zone/forwarding seed.
- `packaging/luci-app-pharos-client/` — the `luci-app-pharos-client` web UI
  (`Architecture: all`): client-JS views (Profiles + Connection) under
  `/www/luci-static/resources/view/pharos/`, a `menu.d`/`acl.d` entry under
  **VPN → PharosVPN**, and a ucode rpcd backend (`/usr/share/rpcd/ucode/pharos`)
  that shells to `caravel-owrt` and reads/writes the non-secret uci. No crypto
  in the browser — `.pharos` decryption stays in the Go binary.
- `scripts/` — `build.sh` (cross-arch matrix), `build-ipk.sh` (hand-assemble
  installable `.ipk`s without the full SDK), and `livetest-local.sh` (stand up a
  throwaway AmneziaWG server on a LAN box to verify the client end-to-end).

## Status

🚧 Pre-alpha. **Client mode is CLI + procd + uci + fw4 + a LuCI app
(`luci-app-pharos-client`), shipped as installable `.ipk`s.**

Verified end-to-end on a real OpenWRT 24.10 x86 VM:

- **CLI/data plane:** import a `.pharos`, bring up the userspace AmneziaWG tunnel
  over `kmod-tun`, full-tunnel egress through the node (traceroute first hop =
  the tunnel server), clean teardown on stop.
- **LuCI app:** both `.ipk`s (`pharos-caravel` x86_64 + `luci-app-pharos-client`
  `all`) install via `opkg`; the **VPN → PharosVPN** menu + both views land; the
  `pharos` ucode rpcd object publishes over ubus and its methods work
  (`import`/`list`/`inspect`/`status`/`getconfig`/`setconfig`/`connect`/
  `disconnect`). The bundled `livetest/local.pharos` imports through the UI path
  and lists; `connect` from the rpcd backend brought the tunnel up via procd
  (handshake completed against the LAN test server) and `disconnect` tore it down
  cleanly. The fw4 `pharos` zone + forwarding and the `pharos` network interface
  are seeded by the package postinst.
- **Not yet verified:** a full multi-hop fleet connect (needs a real fleet node +
  a valid profile endpoint); the *browser* UI was validated by serving the view
  JS + driving the rpcd methods directly (a click-through login wasn't done — the
  VM's LuCI password is in the homelab vault, not used here).

`status --json`, `list --json`, and `inspect --profile NAME --json` were added to
`caravel-owrt` so the rpcd layer reads structured output instead of screen-
scraping; all crypto stays in Go. Next: relay packaging → cross-arch CI + a real
ARM router → node ports (design §9 sequencing).

> **Note (breaking core change):** the caravel core moved to the `profiles[]`
> bundle (one named connection profile per config). `connect`/`inspect` and the
> `livetest/local.pharos` fixture + generator were updated to that shape; older
> top-level-`nodes[]` profiles no longer resolve.

Known constraints: the static Go binary is ~31 MB today (the core's `vp` pulls in
XRay — a build tag to drop it is a planned size optimization; targets routers
with ≥128 MB storage), small root-flash budget, fw4/nftables (no iptables), and
userspace throughput on arm/mips needs benchmarking. Slice-1 limitations: the
kill-switch and IPv6/DNS-leak prevention are not yet enforced (full-tunnel
captures IPv4 only); password/account-mode profiles need an interactive
`connect` (procd handles `none`-mode).

## Build & test

```sh
# Cross-compile the client (default: linux/amd64 for the dev VM; also arm64/mipsle/all)
./scripts/build.sh                       # → bin/caravel-owrt-amd64

# Build installable .ipk packages (no full SDK needed — hand-assembled gzip-tar)
./scripts/build-ipk.sh all amd64         # → dist/{pharos-caravel,luci-app-pharos-client}_*.ipk

# Stand up a throwaway AmneziaWG server on a LAN box + write livetest/local.pharos
SRV_IP=192.168.0.205 ./scripts/livetest-local.sh up

# On the router: install kmod-tun + ip-full, then the packages
opkg update && opkg install kmod-tun ip-full rpcd-mod-ucode
opkg install ./pharos-caravel_*.ipk ./luci-app-pharos-client_*.ipk

# CLI: import, connect
caravel-owrt import local.pharos --name local
caravel-owrt connect --profile local --full-tunnel    # Ctrl-C / procd stop to tear down

./scripts/livetest-local.sh down         # bring the test server down
```

Packaged install (procd): `uci set pharosvpn.main.{enabled=1,profile=local}` then
`/etc/init.d/pharosvpn enable && /etc/init.d/pharosvpn start`. Or use the LuCI app
(**VPN → PharosVPN**): upload a `.pharos` on the Profiles page, then Connect on the
Connection page.

The `.ipk`s use the modern OpenWRT opkg container (a gzipped tar of
`debian-binary` + `control.tar.gz` + `data.tar.gz`, ustar — **not** the Debian
`ar` format), so `build-ipk.sh` runs on any host with `tar`+`gzip`. The LuCI app
is `Architecture: all`; only `pharos-caravel` is per-arch.

## License

Apache-2.0. Contributions under the DCO (`git commit -s`).

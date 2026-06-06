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

## Architecture (planned)

- `cmd/` — the Go worker, reusing `caravel/go`'s `profile` / `vp` / `sync`,
  cross-compiled for the router's arch.
- `luci/` — a `luci-app-pharos` web UI (profile import, connect, kill-switch, status).
- `packaging/` — OpenWRT SDK Makefiles + a signed opkg feed.

## Status

🚧 Pre-alpha — **design complete** (see the design doc), implementation pending.
Recommended order: **client mode** (the headline) → relay → experimental node.
Known constraints: small root-flash budget, fw4/nftables (no iptables), and
userspace throughput on arm/mips needs benchmarking.

## License

Apache-2.0. Contributions under the DCO (`git commit -s`).

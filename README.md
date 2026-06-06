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
- `scripts/` — `build.sh` (cross-arch matrix) and `livetest-local.sh` (stand up a
  throwaway AmneziaWG server on a LAN box to verify the client end-to-end).
- `luci/` *(planned)* — a `luci-app-pharos-client` web UI (profile import,
  connect, kill-switch, status).

## Status

🚧 Pre-alpha. **Client mode (CLI + procd) is implemented and verified
end-to-end** on a real OpenWRT 24.10 x86 VM: import a `.pharos`, bring up the
userspace AmneziaWG tunnel over `kmod-tun`, full-tunnel egress through the node
(traceroute first hop = the tunnel server), clean teardown on stop. Next:
`luci-app-pharos-client` → relay packaging → cross-arch CI + a real ARM router →
node ports (design §9 sequencing).

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

# Stand up a throwaway AmneziaWG server on a LAN box + write livetest/local.pharos
SRV_IP=192.168.0.205 ./scripts/livetest-local.sh up

# On the router: install kmod-tun + ip-full, drop in the binary, import, connect
opkg update && opkg install kmod-tun ip-full
caravel-owrt import local.pharos --name local
caravel-owrt connect --profile local --full-tunnel    # Ctrl-C / procd stop to tear down

./scripts/livetest-local.sh down         # bring the test server down
```

Packaged install (procd): `uci set pharosvpn.main.{enabled=1,profile=local}` then
`/etc/init.d/pharosvpn enable && /etc/init.d/pharosvpn start`.

## License

Apache-2.0. Contributions under the DCO (`git commit -s`).

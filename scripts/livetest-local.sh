#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 The PharosVPN Authors
#
# Stand up a throwaway AmneziaWG server on the LAN ubuntu-builder VM (kernel
# module via the amnezia PPA — the same recipe node/deploy uses) and write a
# matching .pharos, so the OpenWrt client can be verified end-to-end on the local
# network. Unlike caravel-mac/scripts/livetest.sh (which spins a DigitalOcean
# droplet), this targets a box already on the LAN — free and no cloud teardown.
#
#   scripts/livetest-local.sh up     # install/configure server, write livetest/local.pharos
#   scripts/livetest-local.sh down   # bring the server interface down
#
# Requires: SSH (key auth) to the builder with passwordless sudo.
set -euo pipefail

SRV_USER="${SRV_USER:-ubuntu}"
SRV_IP="${SRV_IP:-192.168.0.205}"
SSH="ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 ${SRV_USER}@${SRV_IP}"
OUTDIR="$(cd "$(dirname "$0")/.." && pwd)/livetest"

# The obfuscation set the server advertises and the client must match exactly.
OBF_JC=4; OBF_JMIN=40; OBF_JMAX=70
OBF_S1=50; OBF_S2=50; OBF_S3=50; OBF_S4=50
OBF_H1=5; OBF_H2=6; OBF_H3=7; OBF_H4=8

up() {
	echo "→ ensuring AmneziaWG is installed on ${SRV_IP}…"
	$SSH 'sudo bash -s' <<'REMOTE'
set -e
export DEBIAN_FRONTEND=noninteractive
if ! command -v awg >/dev/null 2>&1; then
  apt-get update -qq
  apt-get install -y -qq software-properties-common curl ca-certificates >/dev/null
  add-apt-repository -y ppa:amnezia/ppa >/dev/null
  apt-get update -qq
  apt-get install -y -qq "linux-headers-$(uname -r)" amneziawg amneziawg-tools >/dev/null
fi
echo 'net.ipv4.ip_forward=1' >/etc/sysctl.d/99-pharos.conf
sysctl --system >/dev/null
modprobe amneziawg || true
command -v awg >/dev/null && echo "  awg present: $(awg --version 2>/dev/null | head -1 || echo ok)"
REMOTE

	echo "→ generating keys + configuring awg0…"
	# shellcheck disable=SC2086
	read -r SERVER_PRIV SERVER_PUB CLIENT_PRIV CLIENT_PUB < <($SSH '
		sp=$(awg genkey); spub=$(echo "$sp" | awg pubkey)
		cp=$(awg genkey); cpub=$(echo "$cp" | awg pubkey)
		echo "$sp $spub $cp $cpub"')

	$SSH "sudo bash -s" <<REMOTE
set -e
mkdir -p /etc/amnezia/amneziawg
cat >/etc/amnezia/amneziawg/awg0.conf <<CONF
[Interface]
PrivateKey = ${SERVER_PRIV}
Address = 10.86.0.1/24
ListenPort = 443
Jc = ${OBF_JC}
Jmin = ${OBF_JMIN}
Jmax = ${OBF_JMAX}
S1 = ${OBF_S1}
S2 = ${OBF_S2}
S3 = ${OBF_S3}
S4 = ${OBF_S4}
H1 = ${OBF_H1}
H2 = ${OBF_H2}
H3 = ${OBF_H3}
H4 = ${OBF_H4}

[Peer]
PublicKey = ${CLIENT_PUB}
AllowedIPs = 10.86.0.2/32
CONF
awg-quick down awg0 2>/dev/null || true
awg-quick up awg0
EGRESS=\$(ip route show default | awk '{print \$5; exit}')
iptables -t nat -C POSTROUTING -s 10.86.0.0/24 -o "\$EGRESS" -j MASQUERADE 2>/dev/null \
  || iptables -t nat -A POSTROUTING -s 10.86.0.0/24 -o "\$EGRESS" -j MASQUERADE
sysctl -w net.ipv4.ip_forward=1 >/dev/null
echo "  awg0 listening on :\$(awg show awg0 listen-port), peer \$(awg show awg0 peers), egress \$EGRESS"
REMOTE

	mkdir -p "$OUTDIR"
	cat >"$OUTDIR/local.pharos" <<PROFILE
{
  "fmt": "pharos-profile", "v": 1, "enc": "none",
  "payload": {
    "fleet_id": "livetest-local", "user": "livetest", "revision": 1,
    "nodes": [{
      "id": "builder", "name": "ubuntu-builder", "region": "lan",
      "endpoints": ["${SRV_IP}"],
      "protocols": [{"type": "amneziawg", "v": 2, "params": {
        "private_key": "${CLIENT_PRIV}",
        "address": "10.86.0.2/32",
        "public_key": "${SERVER_PUB}",
        "endpoints": [{"ip": "${SRV_IP}", "port_min": 443, "port_max": 443}],
        "allowed_ips": ["0.0.0.0/0"],
        "obfuscation": {"jc": ${OBF_JC}, "jmin": ${OBF_JMIN}, "jmax": ${OBF_JMAX}, "s1": ${OBF_S1}, "s2": ${OBF_S2}, "s3": ${OBF_S3}, "s4": ${OBF_S4}, "h1": ${OBF_H1}, "h2": ${OBF_H2}, "h3": ${OBF_H3}, "h4": ${OBF_H4}}
      }}]
    }]
  }
}
PROFILE

	echo
	echo "✅ server up at ${SRV_IP}, profile → ${OUTDIR}/local.pharos"
}

down() {
	echo "→ bringing awg0 down on ${SRV_IP}…"
	$SSH "sudo awg-quick down awg0 2>/dev/null || true; sudo iptables -t nat -D POSTROUTING -s 10.86.0.0/24 -o \$(ip route show default | awk '{print \$5; exit}') -j MASQUERADE 2>/dev/null || true; echo done"
	rm -f "$OUTDIR/local.pharos"
}

case "${1:-}" in
	up) up ;;
	down) down ;;
	*) echo "usage: $0 {up|down}" >&2; exit 2 ;;
esac

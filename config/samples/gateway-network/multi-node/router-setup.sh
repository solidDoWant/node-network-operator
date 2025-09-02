#!/usr/bin/env bash

set -euo pipefail

echo "Setting up the router pod..."

# Masquerade (SNAT) traffic from the gateway bridge network. Traffic will flow from the client pods to the router pod and then be masqueraded to reach the external network.
apk add iptables
iptables -t nat -I POSTROUTING -s 192.168.50.0/24 -o eth0 -j MASQUERADE

echo "Router pod setup complete, sleep indefinitely..."
tail -f /dev/null

#!/usr/bin/env bash

set -euo pipefail

echo "Installing CNI plugins on the node..."

# Install dependencies
echo "Installing dependencies..."
apt update
apt install --no-install-recommends -y curl ca-certificates

# Download CNI plugins
CNI_VERSION="v1.8.0"
KERNEL="$(uname -s | tr '[:upper:]' '[:lower:]')"
PRETTY_ARCH="$(case "$(uname -m)" in 'x86_64') echo "amd64";; *) uname -m;; esac)"
CNI_URL="https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-${KERNEL}-${PRETTY_ARCH}-${CNI_VERSION}.tgz"
CNI_FILE="/tmp/cni-plugins.tgz"
echo "Downloading CNI plugins from ${CNI_URL}..."
curl -fsSL -o "${CNI_FILE}" "${CNI_URL}"

# Install CNI plugins
CNI_BIN_DIR="/host/opt/cni/bin"
echo "Installing CNI plugins to ${CNI_BIN_DIR}..."
tar -C "${CNI_BIN_DIR}" --exclude="LICENSE" --exclude="README.md" -xzf "${CNI_FILE}"
rm -f "${CNI_FILE}"

# Sleep indefinitely
echo "CNI plugins installed successfully, sleep indefinitely..."
tail -f /dev/null

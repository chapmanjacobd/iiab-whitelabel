#!/usr/bin/env bash
# lib-iiab.sh - Shared utility functions for IIAB demo management scripts
# Source this file in scripts that need common helpers:
#   # shellcheck source=lib-iiab.sh
#   source "${SCRIPT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/..}/scripts/lib-iiab.sh"
#
# Or, if SCRIPT_DIR is already set:
#   source "$SCRIPT_DIR/lib-iiab.sh"
set -euo pipefail

###############################################################################
# Shared network configuration
# These values are used by all scripts that source this library.
###############################################################################
IIAB_BRIDGE="iiab-br0"
IIAB_SUBNET_BASE="10.0.3"
IIAB_GW="10.0.3.1"
# shellcheck disable=SC2034  # IIAB_DEMO_SUBNET is used by democtl (cross-file)
IIAB_DEMO_SUBNET="${IIAB_SUBNET_BASE}.0/24"

###############################################################################
# Root / directory / nginx helpers
###############################################################################

# Ensure the script is running as root (re-execs with sudo if needed)
ensure_root() {
    if [ "$EUID" -ne 0 ]; then
        exec sudo "$0" "$@"
    fi
}

# Create directories idempotently (prints status for each)
ensure_dirs() {
    local dir
    for dir in "$@"; do
        if [ ! -d "$dir" ]; then
            echo "Creating $dir..."
            mkdir -p "$dir"
        else
            echo "$dir already exists"
        fi
    done
}

# Test nginx config and reload; falls back to verbose test on failure
nginx_reload() {
    if nginx -t 2>/dev/null; then
        systemctl reload nginx
        echo "Nginx reloaded successfully"
    else
        echo "Warning: nginx config test failed, not reloading" >&2
        nginx -t >&2
        return 1
    fi
}

# Sanitize a name into a valid nginx server_name / upstream-safe identifier
sanitize_subdomain() {
    local raw="$1"
    local cleaned
    cleaned=$(echo "$raw" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9-')
    cleaned="${cleaned#-}"
    cleaned="${cleaned%-}"
    if [ -z "$cleaned" ]; then
        echo "demo"
    else
        echo "$cleaned"
    fi
}

# Setup iptables rules for container NAT (idempotent)
setup_iptables_nat() {
    local ext_if="${1:?Error: external interface required}"

    # Use iptables-save for idempotency since -C doesn't support wildcards like ve-+
    if ! iptables-save -t nat | grep -q "\-o $ext_if.*MASQUERADE" 2>/dev/null; then
        echo "Adding NAT masquerade for $ext_if..."
        iptables -t nat -A POSTROUTING -o "$ext_if" -j MASQUERADE
    else
        echo "NAT masquerade already configured"
    fi

    # Use -I FORWARD 1 to ensure rules are above any Docker/system rules
    # Match both ve-+ and vb-+ (some systems use vb- for bridged veth)
    if ! iptables-save | grep -q "\-i $ext_if.*-o v[be]-+.*ACCEPT" 2>/dev/null; then
        echo "Adding container forwarding rules..."
        iptables -I FORWARD 1 -i "$ext_if" -o "${IIAB_BRIDGE}" -m state --state RELATED,ESTABLISHED -j ACCEPT
        iptables -I FORWARD 1 -i "${IIAB_BRIDGE}" -o "$ext_if" -j ACCEPT
    else
        echo "Container forwarding rules already configured"
    fi
}

# Add per-container network isolation: block container-to-container traffic
# while allowing access to the host (for nginx reverse proxy) and the internet.
# This prevents a compromised container from attacking peers on the bridge.
#
# FORWARD chain ordering (critical, achieved by reverse-order insertion):
#   1. ACCEPT: container → host (nginx)
#   2. ACCEPT: host → container (health checks)
#   3. DROP:   container → container (isolation)
add_container_isolation() {
    local host_ip="${IIAB_GW}"
    local bridge="${IIAB_BRIDGE}"

    # Use -I FORWARD 1 to ensure rules are above any Docker/system rules.
    # We insert them in reverse order of desired precedence.

    # 3. Block all container-to-container traffic on the bridge
    if ! iptables-save | grep -q "\-i $bridge.*-o $bridge.*DROP" 2>/dev/null; then
        echo "Adding container-to-container isolation rule..."
        iptables -I FORWARD 1 -i "$bridge" -o "$bridge" -j DROP
    fi

    # 2. Allow host to reach container(s) (needed for nginx reverse proxy and health checks)
    if ! iptables-save | grep -q "\-d $IIAB_DEMO_SUBNET.*-o $bridge.*ACCEPT" 2>/dev/null; then
        echo "Adding host-to-container forward rule..."
        iptables -I FORWARD 1 -d "$IIAB_DEMO_SUBNET" -o "$bridge" -j ACCEPT
    fi

    # 1. Allow container(s) to reach the host (nginx reverse proxy)
    if ! iptables-save | grep -q "\-i $bridge.*-d $host_ip.*ACCEPT" 2>/dev/null; then
        echo "Adding container-to-host forward rule..."
        iptables -I FORWARD 1 -i "$bridge" -d "$host_ip" -j ACCEPT
    fi
}

# Remove per-container network isolation rules (cleanup -- rarely needed).
# The isolation rules are global and persistent, so this is only for teardown.
remove_container_isolation() {
    iptables -D FORWARD -i "${IIAB_BRIDGE}" -o "${IIAB_BRIDGE}" -j DROP 2>/dev/null || true
    iptables -D FORWARD -d "${IIAB_DEMO_SUBNET}" -o "${IIAB_BRIDGE}" -j ACCEPT 2>/dev/null || true
    iptables -D FORWARD -i "${IIAB_BRIDGE}" -d "${IIAB_GW}" -j ACCEPT 2>/dev/null || true
}

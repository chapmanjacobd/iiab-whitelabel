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

# Setup nftables rules for container NAT (idempotent)
setup_nftables_nat() {
    local ext_if="${1:?Error: external interface required}"

    # Ensure the table and chains exist
    nft add table inet iiab 2>/dev/null || true
    nft add chain inet iiab postrouting '{ type nat hook postrouting priority srcnat; policy accept; }'
    
    # Add masquerade rule
    nft add rule inet iiab postrouting oifname "$ext_if" masquerade
    echo "Configured nftables NAT masquerade on $ext_if"
}

# Add per-container network isolation: block container-to-container traffic
# while allowing access to the host (for nginx reverse proxy) and the internet.
#
# FORWARD chain ordering (critical):
#   1. inet table (L3): Handles host access and internet NAT
#   2. bridge table (L2): Handles intra-bridge isolation (peer-to-peer)
add_container_isolation() {
    local host_ip="${IIAB_GW}"
    local subnet="${IIAB_DEMO_SUBNET}"
    local bridge="${IIAB_BRIDGE}"

    # 1. L3 (inet) rules for Host/Internet access
    nft add table inet iiab 2>/dev/null || true
    # Priority -1 ensures we run before Docker's rules
    nft add chain inet iiab forward '{ type filter hook forward priority filter - 1; policy accept; }'

    # Clear existing rules in our chain for idempotency if called multiple times
    nft flush chain inet iiab forward

    # A. Allow established/related traffic
    nft add rule inet iiab forward ct state established,related accept

    # B. Allow container -> Host and Internet (NAT)
    nft add rule inet iiab forward iifname "{ ve-*, vb-* }" accept

    # C. Allow host -> container (for reverse proxy and health checks)
    nft add rule inet iiab forward ip daddr "$subnet" oifname "{ ve-*, vb-* }" accept

    # 2. L2 (bridge) rules for intra-bridge isolation
    # This ensures isolation works even if br_netfilter is disabled on the host.
    nft add table bridge iiab 2>/dev/null || true
    nft add chain bridge iiab forward '{ type filter hook forward priority 0; policy accept; }'

    # Flush for idempotency
    nft flush chain bridge iiab forward

    # Block intra-bridge container-to-container traffic
    nft add rule bridge iiab forward iifname "ve-*" oifname "ve-*" drop
    nft add rule bridge iiab forward iifname "vb-*" oifname "vb-*" drop
    nft add rule bridge iiab forward iifname "ve-*" oifname "vb-*" drop
    nft add rule bridge iiab forward iifname "vb-*" oifname "ve-*" drop

    echo "Configured nftables isolation (bridge) and host-access (inet) rules"
}
# Remove all IIAB nftables rules
remove_container_isolation() {
    nft delete table inet iiab 2>/dev/null || true
    nft delete table bridge iiab 2>/dev/null || true
    echo "Removed IIAB nftables configuration"
}

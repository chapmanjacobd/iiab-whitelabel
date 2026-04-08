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
# shellcheck disable=SC2034  # Used by scripts that source this library
IIAB_BRIDGE="iiab-br0"
IIAB_SUBNET_BASE="10.0.3"
# shellcheck disable=SC2034  # Used by scripts that source this library
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

# Ensure the container bridge exists and is configured (idempotent)
setup_bridge() {
    local bridge="${IIAB_BRIDGE}"
    local gw="${IIAB_GW}"
    
    echo "=== Ensuring bridge $bridge is configured ($gw) ==="

    local netdev="/etc/systemd/network/${bridge}.netdev"
    local network="/etc/systemd/network/${bridge}.network"
    local changed=false

    mkdir -p /etc/systemd/network

    # 1. Create .netdev if missing
    if [ ! -f "$netdev" ]; then
        echo "Creating bridge netdev config..."
        cat > "$netdev" << EOF
[NetDev]
Name=${bridge}
Kind=bridge

[Bridge]
DefaultPVID=
VLANFiltering=false
EOF
        changed=true
    fi

    # 2. Create .network if missing
    if [ ! -f "$network" ]; then
        echo "Creating bridge network config..."
        cat > "$network" << EOF
[Match]
Name=${bridge}

[Network]
Address=${gw}/24
IPForward=yes
IPMasquerade=yes
EOF
        changed=true
    fi

    # 3. Restart networkd if config changed or bridge missing
    if $changed || ! ip link show "$bridge" >/dev/null 2>&1; then
        echo "Applying bridge configuration..."
        systemctl restart systemd-networkd
        
        # Wait for bridge to appear
        local count=0
        while ! ip link show "$bridge" >/dev/null 2>&1 && [ $count -lt 10 ]; do
            sleep 0.5
            count=$((count + 1))
        done
    fi

    # Ensure IP is actually assigned if it wasn't by networkd yet
    if ! ip addr show "$bridge" | grep -q "$gw"; then
        echo "Manually assigning IP $gw to $bridge..."
        ip addr add "$gw/24" dev "$bridge" 2>/dev/null || true
        ip link set "$bridge" up
    fi
}

# Setup nftables rules for container NAT (idempotent)
setup_nftables_nat() {
    local ext_if="${1:?Error: external interface required}"

    # Ensure the table and chains exist
    nft add table inet iiab 2>/dev/null || true
    nft add chain inet iiab postrouting '{ type nat hook postrouting priority srcnat; policy accept; }'
    
    # Flush postrouting for idempotency
    nft flush chain inet iiab postrouting

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
    local subnet="${IIAB_DEMO_SUBNET}"
    local bridge="${IIAB_BRIDGE}"

    # Docker sets FORWARD policy DROP; ensure IIAB bridge is allowed (idempotent)
    if iptables-save -t filter 2>/dev/null | grep -q "^:FORWARD DROP"; then
        iptables -C FORWARD -i "$bridge" -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -i "$bridge" -j ACCEPT 2>/dev/null || true
        iptables -C FORWARD -o "$bridge" -j ACCEPT 2>/dev/null || iptables -I FORWARD 2 -o "$bridge" -j ACCEPT 2>/dev/null || true
    fi

    # 1. L3 (inet) rules for Host/Internet access
    nft add table inet iiab 2>/dev/null || true
    
    # Forwarding rules
    nft add chain inet iiab forward '{ type filter hook forward priority filter - 1; policy accept; }'
    nft flush chain inet iiab forward

    # A. Allow established/related traffic
    nft add rule inet iiab forward ct state established,related accept

    # B. Allow container -> Host and Internet (NAT)
    nft add rule inet iiab forward iifname "{ $bridge, ve-*, vb-* }" accept

    # C. Allow host -> container (for reverse proxy and health checks)
    nft add rule inet iiab forward oifname "{ $bridge, ve-*, vb-* }" ip daddr "$subnet" accept

    # Input rules (to allow containers to reach host services like DNS/Nginx)
    nft add chain inet iiab input '{ type filter hook input priority filter - 1; policy accept; }'
    nft flush chain inet iiab input
    nft add rule inet iiab input iifname "{ $bridge, ve-*, vb-* }" accept
    nft add rule inet iiab input ct state established,related accept

    # 2. L2 (bridge) rules for intra-bridge isolation
    # This ensures isolation works even if br_netfilter is disabled on the host.
    nft add table bridge iiab 2>/dev/null || true
    nft add chain bridge iiab forward '{ type filter hook forward priority 0; policy accept; }'
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

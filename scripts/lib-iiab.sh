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

    if ! iptables -t nat -C POSTROUTING -o "$ext_if" -j MASQUERADE 2>/dev/null; then
        echo "Adding NAT masquerade for $ext_if..."
        iptables -t nat -A POSTROUTING -o "$ext_if" -j MASQUERADE
    else
        echo "NAT masquerade already configured"
    fi

    if ! iptables -C FORWARD -i ve-+ -o "$ext_if" -j ACCEPT 2>/dev/null; then
        echo "Adding container forwarding rules..."
        iptables -A FORWARD -i ve-+ -o "$ext_if" -j ACCEPT
        iptables -A FORWARD -i "$ext_if" -o ve-+ -m state --state RELATED,ESTABLISHED -j ACCEPT
    else
        echo "Container forwarding rules already configured"
    fi
}

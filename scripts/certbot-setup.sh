#!/usr/bin/env bash
# certbot-setup.sh - Idempotent Let's Encrypt certificate setup
# Replaces: playbooks/06-certbot.yml
# Usage: sudo bash certbot-setup.sh
set -euo pipefail

echo "=== Setting up Let's Encrypt Certificates ==="

# Ensure root
if [ "$EUID" -ne 0 ]; then
    echo "Error: This script must be run as root" >&2
    exit 1
fi

###############################################################################
# Configuration
###############################################################################
CERTBOT_ROOT="/var/www/certbot"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@iiab.io}"
NGINX_LOG_DIR="/var/log/nginx"

###############################################################################
# 1. Create required directories (idempotent)
###############################################################################
echo ""
echo "=== Creating required directories ==="

for dir in "$CERTBOT_ROOT" "$NGINX_LOG_DIR"; do
    if [ ! -d "$dir" ]; then
        echo "Creating $dir..."
        mkdir -p "$dir"
        chmod 0755 "$dir"
    else
        echo "$dir already exists"
    fi
done

###############################################################################
# 2. Collect active demo domains
###############################################################################
echo ""
echo "=== Collecting active demo domains ==="

STATE_DIR="/var/lib/iiab-demos"
ACTIVE_DIR="$STATE_DIR/active"
DOMAINS=()

# Sanitize subdomain name for nginx/certbot compatibility
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

if [ -d "$ACTIVE_DIR" ]; then
    for demo_dir in "$ACTIVE_DIR"/*/; do
        [ -d "$demo_dir" ] || continue
        demo_name=$(basename "$demo_dir")

        # Read subdomain from config
        if [ -f "$demo_dir/config" ]; then
            # shellcheck source=/dev/null
            source "$demo_dir/config"
            subdomain="$(sanitize_subdomain "${SUBDOMAIN:-$demo_name}")"
        else
            subdomain="$demo_name"
        fi
        
        domain="${subdomain}.iiab.io"
        DOMAINS+=("$domain")
        echo "Found domain: $domain"
    done
fi

if [ ${#DOMAINS[@]} -eq 0 ]; then
    echo "Warning: No active demos found. Add demos first, then run this script." >&2
    echo "  democtl add small"
    exit 0
fi

echo ""
echo "Will obtain certificates for: ${DOMAINS[*]}"

###############################################################################
# 3. Obtain certificates (idempotent)
###############################################################################
echo ""
echo "=== Obtaining Let's Encrypt certificates ==="

for domain in "${DOMAINS[@]}"; do
    CERT_PATH="/etc/letsencrypt/live/$domain/fullchain.pem"
    
    if [ -f "$CERT_PATH" ]; then
        echo "Certificate already exists for $domain"
        continue
    fi
    
    echo "Obtaining certificate for $domain..."
    if certbot certonly \
        --webroot \
        --webroot-path "$CERTBOT_ROOT" \
        --email "$ADMIN_EMAIL" \
        --agree-tos \
        --no-eff-email \
        --non-interactive \
        -d "$domain"; then
        echo "Certificate obtained for $domain"
    else
        echo "Warning: Failed to obtain certificate for $domain" >&2
        echo "  Check that nginx is running and ACME challenges are accessible" >&2
    fi
done

###############################################################################
# 4. Setup certbot renewal timer (idempotent)
###############################################################################
echo ""
echo "=== Setting up certbot renewal ==="

if systemctl is-active --quiet certbot.timer; then
    echo "certbot.timer already running"
else
    echo "Enabling certbot.timer..."
    systemctl enable --now certbot.timer
fi

###############################################################################
# 5. Create certbot renewal hook to reload nginx (idempotent)
###############################################################################
echo ""
echo "=== Creating certbot renewal hook ==="

RENEWAL_HOOK="/etc/letsencrypt/renewal-hooks/post/reload-nginx.sh"

if [ ! -f "$RENEWAL_HOOK" ]; then
    echo "Creating renewal hook..."
    cat > "$RENEWAL_HOOK" << 'EOF'
#!/bin/sh
nginx -t && systemctl reload nginx
EOF
    chmod 0755 "$RENEWAL_HOOK"
else
    echo "Renewal hook already exists"
fi

###############################################################################
# 6. Regenerate nginx config with SSL (idempotent)
###############################################################################
echo ""
echo "=== Regenerating nginx config with SSL ==="

# This assumes nginx-gen.sh is available
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NGINX_GEN="$SCRIPT_DIR/nginx-gen.sh"

if [ -x "$NGINX_GEN" ]; then
    bash "$NGINX_GEN"
else
    echo "Warning: nginx-gen.sh not found or not executable" >&2
    echo "  Run: democtl reload"
fi

###############################################################################
# 7. Test nginx configuration (idempotent)
###############################################################################
echo ""
echo "=== Testing nginx configuration ==="

if nginx -t 2>/dev/null; then
    echo "nginx config test passed"
    systemctl reload nginx
else
    echo "Warning: nginx config test failed, please check manually" >&2
    nginx -t
fi

echo ""
echo "=== Certificate setup complete! ==="
echo "Obtained/renewed certificates for: ${DOMAINS[*]}"
echo ""
echo "Certificates are managed by certbot.timer and will auto-renew."

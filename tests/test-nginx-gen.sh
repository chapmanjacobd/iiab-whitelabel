#!/usr/bin/env bash
# test-nginx-gen.sh - Test nginx configuration generation
# Tests: upstream blocks, server blocks, SSL handling, catch-alls
# shellcheck disable=SC2329
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."
NGINX_GEN="$SCRIPT_DIR/scripts/nginx-gen.sh"
TEST_STATE_DIR="/tmp/iiab-test-nginx-$$"
TEST_NGINX_CONF="/tmp/iiab-test-nginx-$$-nginx.conf"
PASS=0
FAIL=0
TOTAL=0

###############################################################################
# Test helpers
###############################################################################

setup_test_env() {
    # Clean and create isolated test state directory
    rm -rf "$TEST_STATE_DIR"
    mkdir -p "$TEST_STATE_DIR/active"

    # Override state dirs
    export STATE_DIR="$TEST_STATE_DIR"
    export ACTIVE_DIR="$TEST_STATE_DIR/active"

    # Override nginx conf path for testing
    export TEST_NGINX_CONF
}

teardown_test_env() {
    rm -rf "$TEST_STATE_DIR"
    rm -f "$TEST_NGINX_CONF"
}

# Cleanup on exit
trap teardown_test_env EXIT

assert_equals() {
    local expected="$1" actual="$2" msg="${3:-}"
    TOTAL=$((TOTAL + 1))
    if [ "$expected" = "$actual" ]; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (expected='$expected', actual='$actual')"
    fi
}

assert_contains() {
    local haystack="$1" needle="$2" msg="${3:-}"
    TOTAL=$((TOTAL + 1))
    if echo "$haystack" | grep -qF "$needle"; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (expected to contain '$needle')" >&2
        echo "  --- Generated config snippet around failure ---" >&2
        echo "$haystack" | grep -C3 -F "$(echo "$needle" | head -c20)" 2>/dev/null || echo "$haystack" | head -30 >&2
        echo "  --- end snippet ---" >&2
    fi
}

assert_not_contains() {
    local haystack="$1" needle="$2" msg="${3:-}"
    TOTAL=$((TOTAL + 1))
    if ! echo "$haystack" | grep -qF "$needle"; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (should not contain '$needle')"
    fi
}

assert_file_exists() {
    local path="$1" msg="${2:-}"
    TOTAL=$((TOTAL + 1))
    if [ -f "$path" ]; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (file not found: $path)"
    fi
}

###############################################################################
# Source shared library
###############################################################################

LIB_IIAB="$SCRIPT_DIR/scripts/lib-iiab.sh"
# shellcheck source=scripts/lib-iiab.sh disable=SC1091
source "$LIB_IIAB"

###############################################################################
# Tests
###############################################################################

echo "=== Nginx Generation Tests ==="
echo ""

# Helper to create a test demo
create_test_demo() {
    local name="$1" ip="$2" wildcard="${3:-false}" subdomain="${4:-}"
    local demo_dir="$ACTIVE_DIR/$name"
    mkdir -p "$demo_dir"
    
    cat > "$demo_dir/config" << EOF
DEMO_NAME="$name"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE_MODE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=$wildcard
DESCRIPTION="Test demo $name"
EOF
    
    if [ -n "$subdomain" ]; then
        echo "SUBDOMAIN=\"$subdomain\"" >> "$demo_dir/config"
    fi
    
    echo "$ip" > "$demo_dir/ip"
    echo "running" > "$demo_dir/status"
}

# We need to mock the nginx_reload function to avoid actual nginx operations
# We'll create a wrapper script that captures the generated config
create_mock_nginx_gen() {
    local output_conf="$1"
    local lock_file="$TEST_STATE_DIR/.nginx-lock"

    # Ensure lock file exists before the mock tries to open it
    touch "$lock_file"

    # Extract testable code from nginx-gen.sh using marker comment (not line numbers)
    # This finds everything from the NGINX_GEN_TESTABLE_START marker to end of file
    local testable_code
    testable_code=$(sed -n '/^# --- NGINX_GEN_TESTABLE_START ---$/,$ p' "$NGINX_GEN")

    cat > "/tmp/mock-nginx-gen.sh" << EOFMOCK
#!/usr/bin/env bash
# Mock nginx-gen.sh that writes to test output instead of real nginx
set -euo pipefail

# Source lib-iiab.sh from the actual project path
# shellcheck source=scripts/lib-iiab.sh disable=SC1091
source "$SCRIPT_DIR/scripts/lib-iiab.sh"

STATE_DIR="$TEST_STATE_DIR"
ACTIVE_DIR="$TEST_STATE_DIR/active"
NGINX_CONF="$output_conf"
CERTBOT_ROOT="/var/www/certbot"
NGINX_LOCK="$lock_file"
NGINX_LOCK_FD=202

# Mock nginx_reload
nginx_reload() {
    echo "Mock: nginx config test and reload would happen here"
    return 0
}

# Source the actual generation logic (from marker in nginx-gen.sh)
$testable_code
EOFMOCK

    chmod +x "/tmp/mock-nginx-gen.sh"
}

# Test 1: Single demo upstream generation
echo "Test 1: Single demo upstream generation"
setup_test_env
create_test_demo "demo1" "10.0.3.2"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "upstream demo1" "Upstream block created"
assert_contains "$OUTPUT" "server 10.0.3.2:80" "Upstream server points to demo IP"
assert_contains "$OUTPUT" "keepalive 32" "Keepalive configured"

# Test 2: Multiple demos
echo ""
echo "Test 2: Multiple demos generation"
setup_test_env
create_test_demo "demo1" "10.0.3.2"
create_test_demo "demo2" "10.0.3.3"
create_test_demo "demo3" "10.0.3.4"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "upstream demo1" "Demo 1 upstream created"
assert_contains "$OUTPUT" "upstream demo2" "Demo 2 upstream created"
assert_contains "$OUTPUT" "upstream demo3" "Demo 3 upstream created"

assert_contains "$OUTPUT" "server 10.0.3.2:80" "Demo 1 IP correct"
assert_contains "$OUTPUT" "server 10.0.3.3:80" "Demo 2 IP correct"
assert_contains "$OUTPUT" "server 10.0.3.4:80" "Demo 3 IP correct"

# Test 3: Subdomain sanitization in server blocks
echo ""
echo "Test 3: Subdomain sanitization"
setup_test_env
create_test_demo "MyDemo" "10.0.3.2"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "server_name mydemo.iiab.io" "Subdomain sanitized to lowercase"

# Test 4: Custom subdomain
echo ""
echo "Test 4: Custom subdomain"
setup_test_env
create_test_demo "demo-custom" "10.0.3.2" false "custom"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "server_name custom.iiab.io" "Custom subdomain used"

# Test 5: Wildcard demo selection for fallback
echo ""
echo "Test 5: Wildcard demo fallback"
setup_test_env
create_test_demo "small" "10.0.3.2"
create_test_demo "large" "10.0.3.3" true

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "return 302 https://large.iiab.io" "Wildcard demo used as fallback"

# Test 6: Proxy pass configuration
echo ""
echo "Test 6: Proxy pass configuration"
setup_test_env
create_test_demo "proxy-test" "10.0.3.10"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "proxy_pass http://proxy_test" "Proxy pass configured (underscore in upstream name)"
assert_contains "$OUTPUT" "proxy_set_header Host \$host" "Host header forwarded"
assert_contains "$OUTPUT" "proxy_set_header X-Real-IP \$remote_addr" "X-Real-IP header set"
assert_contains "$OUTPUT" "proxy_set_header X-Forwarded-For" "X-Forwarded-For header set"

# Test 7: Demo name with hyphens (upstream name conversion)
echo ""
echo "Test 7: Hyphenated demo name handling"
setup_test_env
create_test_demo "my-demo" "10.0.3.2"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

# Upstream names must not contain hyphens, so they're converted to underscores
assert_contains "$OUTPUT" "upstream my_demo" "Hyphen converted to underscore in upstream name"
assert_contains "$OUTPUT" "server_name my-demo.iiab.io" "Hyphen preserved in server_name"

# Test 8: No demos (empty state)
echo ""
echo "Test 8: No active demos"
setup_test_env

create_mock_nginx_gen "$TEST_NGINX_CONF"
OUTPUT=$(bash "/tmp/mock-nginx-gen.sh" 2>&1 || true)

assert_contains "$OUTPUT" "No active demos" "Empty demos message shown"

# Test 9: ACME challenge location blocks
echo ""
echo "Test 9: ACME challenge configuration"
setup_test_env
create_test_demo "acme-test" "10.0.3.2"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" ".well-known/acme-challenge" "ACME challenge location block present"
assert_contains "$OUTPUT" "/var/www/certbot" "Certbot root configured"

# Test 10: HTTP to HTTPS redirect (when cert exists)
echo ""
echo "Test 10: HTTP to HTTPS redirect logic"
setup_test_env
create_test_demo "ssl-test" "10.0.3.2"

# Create a mock SSL cert directory
mkdir -p "/etc/letsencrypt/live/ssl-test.iiab.io"
touch "/etc/letsencrypt/live/ssl-test.iiab.io/fullchain.pem"
touch "/etc/letsencrypt/live/ssl-test.iiab.io/privkey.pem"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "return 301 https://\$host\$request_uri" "HTTP to HTTPS redirect configured"
assert_contains "$OUTPUT" "listen 443 ssl http2" "HTTPS listener configured"
assert_contains "$OUTPUT" "ssl_certificate /etc/letsencrypt/live/ssl-test.iiab.io/fullchain.pem" "SSL certificate path correct"

# Cleanup mock certs
rm -rf "/etc/letsencrypt/live/ssl-test.iiab.io"

# Test 11: HTTPS server block without certs (disabled)
echo ""
echo "Test 11: HTTPS disabled without certs"
setup_test_env
create_test_demo "no-ssl" "10.0.3.2"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "# HTTPS server for no-ssl.iiab.io (DISABLED" "HTTPS marked as disabled"
assert_contains "$OUTPUT" "democtl certbot" "Certbot suggestion shown"

# Test 12: Upstream name uniqueness
echo ""
echo "Test 12: Upstream name uniqueness with similar names"
setup_test_env
create_test_demo "demo" "10.0.3.2"
create_test_demo "demo-test" "10.0.3.3"
create_test_demo "demo-test-2" "10.0.3.4"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

# Count upstream blocks
upstream_count=$(echo "$OUTPUT" | grep -c "^upstream " || true)
assert_equals "3" "$upstream_count" "Three unique upstream blocks created"

# Test 13: Server block count
echo ""
echo "Test 13: Server block generation"
setup_test_env
create_test_demo "server1" "10.0.3.2"
create_test_demo "server2" "10.0.3.3"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

# Count server blocks (each server { line)
server_count=$(echo "$OUTPUT" | grep -c "^server {" || true)
# Should have: 1 fallback + 2 demo servers = 3
assert_equals "3" "$server_count" "Three server blocks (1 fallback + 2 demos)"

# Test 14: Config file header
echo ""
echo "Test 14: Generated config metadata"
setup_test_env
create_test_demo "header-test" "10.0.3.2"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "Auto-generated by democtl nginx-gen.sh" "Config header present"
assert_contains "$OUTPUT" "DO NOT EDIT" "Warning comment present"

# Test 15: Proxy headers completeness
echo ""
echo "Test 15: Complete proxy headers"
setup_test_env
create_test_demo "headers-test" "10.0.3.2"

create_mock_nginx_gen "$TEST_NGINX_CONF"
bash "/tmp/mock-nginx-gen.sh" 2>&1

OUTPUT=$(cat "$TEST_NGINX_CONF")

assert_contains "$OUTPUT" "proxy_set_header Upgrade \$http_upgrade" "Upgrade header for WebSocket support"
assert_contains "$OUTPUT" 'proxy_set_header Connection "upgrade"' "Connection header for WebSocket"
assert_contains "$OUTPUT" "proxy_http_version 1.1" "HTTP/1.1 for proxy"

# Cleanup
rm -f "/tmp/mock-nginx-gen.sh"

###############################################################################
# Summary
###############################################################################

echo ""
echo "=== Nginx Generation Test Summary ==="
echo "Total: $TOTAL"
echo "Passed: $PASS"
echo "Failed: $FAIL"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo "❌ Some tests failed"
    exit 1
else
    echo "✅ All tests passed"
    exit 0
fi

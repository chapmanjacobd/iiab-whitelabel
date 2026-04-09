#!/usr/bin/env bash
# test-nftables.sh - Test nftables isolation rules and network configuration
# Tests: NAT masquerade, container forwarding, isolation rules, idempotency
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."
LIB_IIAB="$SCRIPT_DIR/scripts/lib-iiab.sh"
PASS=0
FAIL=0
TOTAL=0

###############################################################################
# Test helpers
###############################################################################

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
        echo "  ✗ FAIL: $msg (expected to contain '$needle')"
    fi
}

assert_true() {
    local condition="$1" msg="${2:-}"
    TOTAL=$((TOTAL + 1))
    if eval "$condition"; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (condition failed)"
    fi
}

###############################################################################
# Source shared library
###############################################################################

# shellcheck source=scripts/lib-iiab.sh disable=SC1091
source "$LIB_IIAB"

###############################################################################
# Tests
###############################################################################

echo "=== NFTables Isolation Tests ==="
echo ""

# Test 1: Network constants validation
echo "Test 1: Network constants validation"

assert_equals "iiab-br0" "$IIAB_BRIDGE" "Bridge name configured"
assert_equals "10.0.3" "$IIAB_SUBNET_BASE" "Subnet base configured"
assert_equals "10.0.3.1" "$IIAB_GW" "Gateway IP configured"
assert_equals "10.0.3.0/24" "$IIAB_DEMO_SUBNET" "Demo subnet configured"

# Test 2: sanitize_subdomain function
echo ""
echo "Test 2: Subdomain sanitization"

result1=$(sanitize_subdomain "TestDemo")
result2=$(sanitize_subdomain "test-demo")
result3=$(sanitize_subdomain "TEST123")
result4=$(sanitize_subdomain "-leading")
result5=$(sanitize_subdomain "trailing-")
result6=$(sanitize_subdomain "---")
result7=$(sanitize_subdomain "")

assert_equals "testdemo" "$result1" "Uppercase converted"
assert_equals "test-demo" "$result2" "Hyphen preserved"
assert_equals "test123" "$result3" "Numbers preserved"
assert_equals "leading" "$result4" "Leading hyphen removed"
assert_equals "trailing" "$result5" "Trailing hyphen removed"
assert_equals "-" "$result6" "Only hyphens reduces to single hyphen"
assert_equals "demo" "$result7" "Empty string becomes 'demo'"

# Test 3: ensure_dirs function
echo ""
echo "Test 3: Directory creation"

test_dir1="/tmp/iiab-test-$$-dir1"
test_dir2="/tmp/iiab-test-$$-dir2"

# Cleanup
rm -rf "$test_dir1" "$test_dir2"

# First call should create directories
output1=$(ensure_dirs "$test_dir1" "$test_dir2" 2>&1)
first_run=$?

# Second call should report already exists
output2=$(ensure_dirs "$test_dir1" "$test_dir2" 2>&1)
second_run=$?

# Cleanup
rm -rf "$test_dir1" "$test_dir2"

assert_equals "0" "$first_run" "First run succeeds"
assert_equals "0" "$second_run" "Second run succeeds (idempotent)"
assert_contains "$output1" "Creating" "Directories created on first run"
assert_contains "$output2" "already exists" "Directories reported as existing"

# Test 4: nginx_reload function (mocked)
echo ""
echo "Test 4: Nginx reload function structure"

if type nginx_reload >/dev/null 2>&1; then
    assert_true "true" "nginx_reload function exists"
else
    assert_true "false" "nginx_reload function exists"
fi

# Test 5: setup_nftables_nat function structure
echo ""
echo "Test 5: nftables NAT function structure"

if type setup_nftables_nat >/dev/null 2>&1; then
    assert_true "true" "setup_nftables_nat function exists"

    # Verify it requires an external interface parameter
    set +e
    output=$(setup_nftables_nat 2>&1 || true)
    set -e

    assert_contains "$output" "external interface" "External interface required"
else
    assert_true "false" "setup_nftables_nat function exists"
fi

# Test 6: add_container_isolation function structure
echo ""
echo "Test 6: Container isolation function structure"

if type add_container_isolation >/dev/null 2>&1; then
    assert_true "true" "add_container_isolation function exists"
else
    assert_true "false" "add_container_isolation function exists"
fi

# Test 7: remove_container_isolation function structure
echo ""
echo "Test 7: Container isolation removal function structure"

if type remove_container_isolation >/dev/null 2>&1; then
    assert_true "true" "remove_container_isolation function exists"
else
    assert_true "false" "remove_container_isolation function exists"
fi

# Test 8: ensure_root function
echo ""
echo "Test 8: Root check function"

if type ensure_root >/dev/null 2>&1; then
    assert_true "true" "ensure_root function exists"
else
    assert_true "false" "ensure_root function exists"
fi

# Test 9: Network topology validation
echo ""
echo "Test 9: Network topology consistency"

gw_prefix=$(echo "$IIAB_GW" | cut -d'.' -f1-3)
assert_equals "$IIAB_SUBNET_BASE" "$gw_prefix" "Gateway IP in subnet range"

expected_subnet="${IIAB_SUBNET_BASE}.0/24"
assert_equals "$expected_subnet" "$IIAB_DEMO_SUBNET" "Subnet calculated correctly"

# Test 10: IP address validation patterns
echo ""
echo "Test 10: IP address format validation"

for i in 2 10 100 200 253; do
    ip="${IIAB_SUBNET_BASE}.$i"
    if [[ "$ip" =~ ^10\.0\.3\.[0-9]+$ ]]; then
        assert_true "true" "IP $ip format valid"
    else
        assert_true "false" "IP $ip format valid"
    fi
done

# Test 11: nftables rule patterns -- verify table/chain structure via nft list
echo ""
echo "Test 11: nftables rule design validation"

# Apply isolation rules so we can inspect them
set +e
add_container_isolation > /dev/null 2>&1
set -e

# Check that the inet iiab table and forward chain exist
if nft list table inet iiab 2>/dev/null | grep -qE "type filter hook forward priority (filter - 1|-1)"; then
    assert_true "true" "Forward chain uses priority filter - 1"
else
    assert_true "false" "Forward chain uses priority filter - 1"
fi

# Check that the bridge iiab table exists with forward chain
if nft list table bridge iiab 2>/dev/null | grep -q "type filter hook forward"; then
    assert_true "true" "Bridge isolation table has forward chain"
else
    assert_true "false" "Bridge isolation table has forward chain"
fi

# Verify container-to-container drop rules exist
BRIDGE_RULES=$(nft list chain bridge iiab forward 2>/dev/null || echo "")
if echo "$BRIDGE_RULES" | grep -q "drop"; then
    assert_true "true" "Bridge chain has container-to-container drop rules"
else
    assert_true "false" "Bridge chain has container-to-container drop rules"
fi

# Verify inet forward has allow rules for host and internet
INET_FORWARD=$(nft list chain inet iiab forward 2>/dev/null || echo "")
if echo "$INET_FORWARD" | grep -q "accept"; then
    assert_true "true" "Inet forward chain has accept rules"
else
    assert_true "false" "Inet forward chain has accept rules"
fi

# Test 12: nftables rules reference correct interface patterns
echo ""
echo "Test 12: nftables interface patterns in rules"

# Rules are already applied from Test 11; verify the interface patterns
FORWARD_RULES=$(nft list chain inet iiab forward 2>/dev/null || echo "")
BRIDGE_RULES=$(nft list chain bridge iiab forward 2>/dev/null || echo "")

# Verify container interface patterns (ve-*, vb-*) appear in rules
if echo "$FORWARD_RULES" | grep -qE 'iifname.*ve-\*'; then
    assert_true "true" "Forward rules reference ve-* input interfaces"
else
    assert_true "false" "Forward rules reference ve-* input interfaces"
fi

if echo "$BRIDGE_RULES" | grep -qE 'oifname.*ve-\*.*drop'; then
    assert_true "true" "Bridge rules drop traffic between ve-* interfaces"
else
    assert_true "false" "Bridge rules drop traffic between ve-* interfaces"
fi

###############################################################################
# Summary
###############################################################################

echo ""
echo "=== NFTables Isolation Test Summary ==="
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

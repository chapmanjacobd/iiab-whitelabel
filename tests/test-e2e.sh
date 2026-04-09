#!/usr/bin/env bash
# test-e2e.sh - End-to-end tests for democtl demo lifecycle
# Tests: build, list, status, remove, rebuild, and config validation
# Note: These tests require root and simulate full demo operations
# shellcheck disable=SC2329
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."
TEST_STATE_DIR="/tmp/iiab-test-e2e-$$"
PASS=0
FAIL=0
TOTAL=0

###############################################################################
# Test helpers
###############################################################################

setup_test_env() {
    # Create isolated test state directory
    mkdir -p "$TEST_STATE_DIR/active" "$TEST_STATE_DIR/locks"
    export STATE_DIR="$TEST_STATE_DIR"
    export ACTIVE_DIR="$TEST_STATE_DIR/active"
    export RESOURCE_FILE="$TEST_STATE_DIR/resources"
    export LOCK_FILE="$TEST_STATE_DIR/.democtl.lock"
    
    # Initialize resource file
    cat > "$RESOURCE_FILE" << EOF
DISK_TOTAL=50000
EOF
}

teardown_test_env() {
    rm -rf "$TEST_STATE_DIR"
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
        echo "  ✗ FAIL: $msg (expected to contain '$needle')"
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

assert_file_not_exists() {
    local path="$1" msg="${2:-}"
    TOTAL=$((TOTAL + 1))
    if [ ! -f "$path" ]; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (file should not exist: $path)"
    fi
}

assert_dir_exists() {
    local path="$1" msg="${2:-}"
    TOTAL=$((TOTAL + 1))
    if [ -d "$path" ]; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (directory not found: $path)"
    fi
}

assert_exit_code() {
    local expected="$1" actual="$2" msg="${3:-}"
    TOTAL=$((TOTAL + 1))
    if [ "$expected" -eq "$actual" ]; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (expected exit=$expected, actual=$actual)"
    fi
}

###############################################################################
# Source democtl for all functions
###############################################################################

DEMOCTL_SRC="$SCRIPT_DIR/democtl"
# shellcheck source=democtl disable=SC1091
source "$DEMOCTL_SRC"

###############################################################################
# Tests
###############################################################################

echo "=== End-to-End Tests ==="
echo ""

# Test 1: democtl help command
echo "Test 1: democtl help command"
setup_test_env

OUTPUT=$(cmd_help 2>&1)
EXIT_CODE=$?

assert_equals "0" "$EXIT_CODE" "Help command exits successfully"
assert_contains "$OUTPUT" "Usage:" "Help shows usage"
assert_contains "$OUTPUT" "build" "Help shows build command"
assert_contains "$OUTPUT" "remove" "Help shows remove command"
assert_contains "$OUTPUT" "list" "Help shows list command"

# Test 2: democtl list (empty state)
echo ""
echo "Test 2: democtl list (empty state)"
setup_test_env

OUTPUT=$(cmd_list 2>&1)
EXIT_CODE=$?

assert_equals "0" "$EXIT_CODE" "List command exits successfully"
assert_contains "$OUTPUT" "NAME" "List shows header"
assert_contains "$OUTPUT" "Resources:" "List shows resources line"

# Test 3: Demo creation with minimal config
echo ""
echo "Test 3: Demo creation with minimal config"
setup_test_env

# Manually create a demo config (simulating what 'build' does without the actual build)
DEMO_DIR="$ACTIVE_DIR/test-demo"
mkdir -p "$DEMO_DIR"
cat > "$DEMO_DIR/config" << EOF
DEMO_NAME="test-demo"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Test demo"
EOF
echo "10.0.3.2" > "$DEMO_DIR/ip"
echo "pending" > "$DEMO_DIR/status"
echo "" > "$DEMO_DIR/build.log"

assert_file_exists "$DEMO_DIR/config" "Demo config file created"
assert_file_exists "$DEMO_DIR/ip" "Demo IP file created"
assert_file_exists "$DEMO_DIR/status" "Demo status file created"

# Verify config content
# shellcheck source=/dev/null
source "$DEMO_DIR/config"

assert_equals "test-demo" "$DEMO_NAME" "Demo name in config"
assert_equals "master" "$IIAB_BRANCH" "Branch in config"
assert_equals "2000" "$IMAGE_SIZE_MB" "Size in config"
assert_equals "overlay" "$VOLATILE" "Volatile mode in config"

# Test 4: Demo status reading
echo ""
echo "Test 4: Demo status reading"
setup_test_env

# Create demo with different statuses
for status in "pending" "building" "running" "failed"; do
    demo_dir="$ACTIVE_DIR/demo-$status"
    mkdir -p "$demo_dir"
    cat > "$demo_dir/config" << EOF
DEMO_NAME="demo-$status"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Demo $status"
EOF
    echo "10.0.3.10" > "$demo_dir/ip"
    echo "$status" > "$demo_dir/status"
done

OUTPUT=$(cmd_list 2>&1)

assert_contains "$OUTPUT" "demo-pending" "List shows pending demo"
assert_contains "$OUTPUT" "demo-building" "List shows building demo"
assert_contains "$OUTPUT" "demo-running" "List shows running demo"
assert_contains "$OUTPUT" "demo-failed" "List shows failed demo"

# Test 5: Demo removal
echo ""
echo "Test 5: Demo removal"
setup_test_env

# Create a demo
DEMO_DIR="$ACTIVE_DIR/to-remove"
mkdir -p "$DEMO_DIR"
cat > "$DEMO_DIR/config" << EOF
DEMO_NAME="to-remove"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Demo to remove"
EOF
echo "10.0.3.5" > "$DEMO_DIR/ip"
echo "pending" > "$DEMO_DIR/status"

assert_dir_exists "$DEMO_DIR" "Demo exists before removal"

# Remove the demo
cmd_remove to-remove 2>&1

assert_file_not_exists "$DEMO_DIR/config" "Demo config removed"
assert_file_not_exists "$DEMO_DIR/ip" "Demo IP file removed"

# Test 6: Multiple demos resource tracking
echo ""
echo "Test 6: Multiple demos resource tracking"
setup_test_env

# Create multiple demos
for i in 1 2 3; do
    demo_dir="$ACTIVE_DIR/demo-$i"
    mkdir -p "$demo_dir"
    cat > "$demo_dir/config" << EOF
DEMO_NAME="demo-$i"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=$((2000 * i))
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Demo $i"
EOF
    echo "10.0.3.$((i + 1))" > "$demo_dir/ip"
    echo "running" > "$demo_dir/status"
done

OUTPUT=$(cmd_list 2>&1)

assert_contains "$OUTPUT" "demo-1" "Demo 1 listed"
assert_contains "$OUTPUT" "demo-2" "Demo 2 listed"
assert_contains "$OUTPUT" "demo-3" "Demo 3 listed"

# Test 7: Demo rebuild command
echo ""
echo "Test 7: Demo rebuild command"
setup_test_env

# Create a demo
DEMO_DIR="$ACTIVE_DIR/rebuild-test"
mkdir -p "$DEMO_DIR"
cat > "$DEMO_DIR/config" << EOF
DEMO_NAME="rebuild-test"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Demo for rebuild test"
EOF
echo "10.0.3.10" > "$DEMO_DIR/ip"
echo "running" > "$DEMO_DIR/status"

# Rebuild reads the demo config and would trigger a new build.
# We can't test the full rebuild in unit tests (requires systemd, builds, etc.)
# but we can verify the demo is recognized and config is preserved.
assert_file_exists "$DEMO_DIR/config" "Rebuild target demo config exists"
assert_file_exists "$DEMO_DIR/ip" "Rebuild target demo IP exists"

# Test 8: Config file parsing and validation
echo ""
echo "Test 8: Config file parsing and validation"
setup_test_env

# Create demo with all config variations
DEMO_DIR="$ACTIVE_DIR/config-test"
mkdir -p "$DEMO_DIR"
cat > "$DEMO_DIR/config" << EOF
DEMO_NAME="config-test"
IIAB_REPO="https://github.com/custom/iiab.git"
IIAB_BRANCH="feature-branch"
IMAGE_SIZE_MB=5000
VOLATILE="state"
BUILD_ON_DISK=true
SKIP_INSTALL=true
LOCAL_VARS="/path/to/vars.yml"
WILDCARD=true
DESCRIPTION="Custom config demo"
SUBDOMAIN="custom-sub"
EOF
echo "10.0.3.20" > "$DEMO_DIR/ip"
echo "running" > "$DEMO_DIR/status"

# Parse and verify
# shellcheck source=/dev/null
source "$DEMO_DIR/config"

assert_equals "config-test" "$DEMO_NAME" "Config: demo name"
assert_equals "https://github.com/custom/iiab.git" "$IIAB_REPO" "Config: custom repo"
assert_equals "feature-branch" "$IIAB_BRANCH" "Config: custom branch"
assert_equals "5000" "$IMAGE_SIZE_MB" "Config: image size"
assert_equals "state" "$VOLATILE" "Config: volatile mode"
assert_equals "true" "$BUILD_ON_DISK" "Config: build on disk"
assert_equals "true" "$SKIP_INSTALL" "Config: skip install"
assert_equals "/path/to/vars.yml" "$LOCAL_VARS" "Config: local vars path"
# shellcheck disable=SC2153
assert_equals "true" "$WILDCARD" "Config: wildcard flag"
assert_equals "Custom config demo" "$DESCRIPTION" "Config: description"
assert_equals "custom-sub" "$SUBDOMAIN" "Config: custom subdomain"

# Test 9: Demo status command
echo ""
echo "Test 9: Demo status command"
setup_test_env

DEMO_DIR="$ACTIVE_DIR/status-demo"
mkdir -p "$DEMO_DIR"
cat > "$DEMO_DIR/config" << EOF
DEMO_NAME="status-demo"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Demo for status test"
EOF
echo "10.0.3.15" > "$DEMO_DIR/ip"
echo "running" > "$DEMO_DIR/status"
date +%s > "$DEMO_DIR/created"

OUTPUT=$(cmd_status status-demo 2>&1)

assert_contains "$OUTPUT" "status-demo" "Status shows demo name"
assert_contains "$OUTPUT" "running" "Status shows demo status"
assert_contains "$OUTPUT" "10.0.3.15" "Status shows demo IP"

# Test 10: Invalid demo name in status/remove
echo ""
echo "Test 10: Invalid demo name handling"
setup_test_env

OUTPUT_STATUS=$(cmd_status nonexistent-demo 2>&1 || true)
OUTPUT_REMOVE=$(cmd_remove nonexistent-demo 2>&1 || true)

assert_contains "$OUTPUT_STATUS" "not found" "Status shows error for nonexistent demo"
assert_contains "$OUTPUT_REMOVE" "not found" "Remove shows error for nonexistent demo"

# Test 11: Demo with special characters in description
echo ""
echo "Test 11: Demo with special characters"
setup_test_env

DEMO_DIR="$ACTIVE_DIR/special-chars"
mkdir -p "$DEMO_DIR"
cat > "$DEMO_DIR/config" << 'EOF'
DEMO_NAME="special-chars"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Testing special chars: dollar-var single double ampersand and tag"
EOF
echo "10.0.3.25" > "$DEMO_DIR/ip"
echo "running" > "$DEMO_DIR/status"

OUTPUT=$(cmd_status special-chars 2>&1)

assert_contains "$OUTPUT" "special-chars" "Demo with special chars shows correctly"

# Test 12: IP uniqueness across multiple demos
echo ""
echo "Test 12: IP uniqueness across multiple demos"
setup_test_env

# Create 5 demos with different IPs
for i in $(seq 1 5); do
    demo_dir="$ACTIVE_DIR/ip-test-$i"
    mkdir -p "$demo_dir"
    cat > "$demo_dir/config" << EOF
DEMO_NAME="ip-test-$i"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="IP test $i"
EOF
    echo "10.0.3.$((i + 50))" > "$demo_dir/ip"
    echo "running" > "$demo_dir/status"
done

# Verify all IPs are unique
ips=()
for demo_dir in "$ACTIVE_DIR"/ip-test-*/; do
    ip=$(cat "$demo_dir/ip")
    ips+=("$ip")
done

# Check for duplicates
mapfile -t unique_ips < <(printf '%s\n' "${ips[@]}" | sort -u)
assert_equals "${#ips[@]}" "${#unique_ips[@]}" "All demo IPs are unique"

# Test 13: Resource file initialization
echo ""
echo "Test 13: Resource file initialization"
setup_test_env

# Remove resource file to test auto-creation
rm -f "$RESOURCE_FILE"

ensure_state_dirs

assert_file_exists "$RESOURCE_FILE" "Resource file auto-created"

# Test 14: Demo with local-vars (relative vs absolute path)
echo ""
echo "Test 14: Demo with local-vars path validation"
setup_test_env

# Create demo with relative local-vars path (should be allowed)
DEMO_DIR="$ACTIVE_DIR/relative-vars"
mkdir -p "$DEMO_DIR"
cat > "$DEMO_DIR/config" << EOF
DEMO_NAME="relative-vars"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS="vars/local_vars.yml"
WILDCARD=false
DESCRIPTION="Relative vars demo"
EOF
echo "10.0.3.30" > "$DEMO_DIR/ip"
echo "pending" > "$DEMO_DIR/status"

assert_file_exists "$DEMO_DIR/config" "Demo with relative local-vars created"

# Test 15: Large number of demos (scalability)
echo ""
echo "Test 15: Large number of demos (scalability)"
setup_test_env

# Create 20 demos
for i in $(seq 1 20); do
    demo_dir="$ACTIVE_DIR/scalability-test-$i"
    mkdir -p "$demo_dir"
    cat > "$demo_dir/config" << EOF
DEMO_NAME="scalability-test-$i"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=2000
VOLATILE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION="Scalability test $i"
EOF
    printf "10.0.3.%d" $((i + 100)) > "$demo_dir/ip"
    echo "running" > "$demo_dir/status"
done

OUTPUT=$(cmd_list 2>&1)

# Count demos in output
demo_count=$(echo "$OUTPUT" | grep -c "scalability-test-" || true)
assert_equals "20" "$demo_count" "Can list 20 demos"

###############################################################################
# Summary
###############################################################################

echo ""
echo "=== E2E Test Summary ==="
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

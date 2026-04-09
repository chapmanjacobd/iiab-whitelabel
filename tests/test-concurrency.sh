#!/usr/bin/env bash
# test-concurrency.sh - Test concurrent democtl operations and locking behavior
# Tests: flock-based locking, IP allocation, resource management, stale lock cleanup
# shellcheck disable=SC2329
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."
TEST_STATE_DIR="/tmp/iiab-test-concurrency-$$"
PASS=0
FAIL=0
TOTAL=0

###############################################################################
# Test helpers
###############################################################################

setup_test_env() {
    # Clean and create isolated test state directory
    rm -rf "$TEST_STATE_DIR"
    mkdir -p "$TEST_STATE_DIR/active" "$TEST_STATE_DIR/locks"
    export STATE_DIR="$TEST_STATE_DIR"
    export ACTIVE_DIR="$TEST_STATE_DIR/active"
    export RESOURCE_FILE="$TEST_STATE_DIR/resources"
    export LOCK_FILE="$TEST_STATE_DIR/.democtl.lock"
    export DISK_ALLOCATED=0

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
# Source democtl for all functions (locking, resource management, etc.)
###############################################################################

DEMOCTL_SRC="$SCRIPT_DIR/democtl"
# shellcheck source=democtl disable=SC1091
source "$DEMOCTL_SRC"

###############################################################################
# Tests
###############################################################################

echo "=== Concurrency Tests ==="
echo ""

# Test 1: Lock acquisition and release
echo "Test 1: Lock acquisition and release"
setup_test_env
ensure_state_dirs

acquire_lock 0
LOCK_ACQUIRED=$?
release_lock

assert_equals "0" "$LOCK_ACQUIRED" "Lock acquired successfully"
assert_file_exists "$LOCK_FILE" "Lock file created"

# Test 2: Concurrent lock rejection (non-blocking)
echo ""
echo "Test 2: Concurrent lock rejection (non-blocking)"
setup_test_env
ensure_state_dirs

# Acquire lock in the main shell
acquire_lock 0
FIRST_LOCK=$?

# Try to acquire lock in a SEPARATE bash process (simulates concurrent process)
# Use flock command directly on the same file to avoid FD inheritance issues
SECOND_LOCK=$(flock -n "$LOCK_FILE" -c "echo 0" 2>/dev/null || echo 1)

release_lock

assert_equals "0" "$FIRST_LOCK" "First lock acquired"
assert_equals "1" "$SECOND_LOCK" "Second lock rejected (concurrent access blocked)"

# Test 3: Stale lock cleanup (dead process)
echo ""
echo "Test 3: Stale lock cleanup (dead process)"
setup_test_env
ensure_state_dirs

# Create a stale lock with a fake PID (non-existent process)
echo "999999" > "$LOCK_FILE.pid"

# Try to acquire lock (should clean up stale lock)
acquire_lock 0 2>/dev/null
STALE_CLEANUP=$?

release_lock

assert_equals "0" "$STALE_CLEANUP" "Stale lock cleaned up successfully"

# Test 4: IP allocation uniqueness
echo ""
echo "Test 4: IP allocation uniqueness"
setup_test_env
ensure_state_dirs

# Allocate IPs sequentially, writing mock demo dirs between each allocation
# (next_ip scans for ip files on disk, so we must write them before next call)
allocate_and_register_ip() {
    local ip
    ip=$(next_ip)
    local name
    name="demo-$(echo "$ip" | tr '.' '-')"
    mkdir -p "$ACTIVE_DIR/$name"
    echo "$ip" > "$ACTIVE_DIR/$name/ip"
    cat > "$ACTIVE_DIR/$name/config" << EOF
DEMO_NAME="$name"
IMAGE_SIZE_MB=2000
EOF
    echo "$ip"
}

IP1=$(allocate_and_register_ip)
IP2=$(allocate_and_register_ip)
IP3=$(allocate_and_register_ip)
IP4=$(allocate_and_register_ip)
IP5=$(allocate_and_register_ip)

IP6=$(allocate_and_register_ip)
IP7=$(allocate_and_register_ip)
IP8=$(allocate_and_register_ip)
IP9=$(allocate_and_register_ip)
IP10=$(allocate_and_register_ip)

assert_equals "10.0.3.2" "$IP1" "First IP is 10.0.3.2"
assert_equals "10.0.3.3" "$IP2" "Second IP is 10.0.3.3"
assert_equals "10.0.3.4" "$IP3" "Third IP is 10.0.3.4"
assert_equals "10.0.3.5" "$IP4" "Fourth IP is 10.0.3.5"
assert_equals "10.0.3.6" "$IP5" "Fifth IP is 10.0.3.6"

assert_equals "10.0.3.7" "$IP6" "Sixth IP is 10.0.3.7"
assert_equals "10.0.3.8" "$IP7" "Seventh IP is 10.0.3.8"
assert_equals "10.0.3.9" "$IP8" "Eighth IP is 10.0.3.9"
assert_equals "10.0.3.10" "$IP9" "Ninth IP is 10.0.3.10"
assert_equals "10.0.3.11" "$IP10" "Tenth IP is 10.0.3.11"

# Test 5: IP pool exhaustion handling
echo ""
echo "Test 5: IP pool exhaustion handling (simulation)"
setup_test_env
ensure_state_dirs

# Mock all IPs as used (simulate 253 demos, exhausting the pool)
for i in $(seq 2 254); do
    ip="10.0.3.$i"
    mkdir -p "$ACTIVE_DIR/demo-$i"
    echo "$ip" > "$ACTIVE_DIR/demo-$i/ip"
    cat > "$ACTIVE_DIR/demo-$i/config" << EOF
DEMO_NAME="demo-$i"
IMAGE_SIZE_MB=2000
EOF
done

# Try to allocate one more IP (should fail)
# Use subshell to avoid set -e killing the test script when next_ip returns 1
IP_EXHAUSTED=$(next_ip 2>/dev/null && echo 0 || echo 1)

assert_equals "1" "$IP_EXHAUSTED" "IP exhaustion detected correctly"

# Test 6: Demo name validation
echo ""
echo "Test 6: Demo name validation"
setup_test_env

# Valid names
_validate_name() {
    local name="$1"
    if [[ ! "$name" =~ ^[a-zA-Z0-9_-]+$ ]]; then
        return 1
    fi
    if [[ ${#name} -gt 64 ]]; then
        return 1
    fi
    return 0
}

VALID1=$(_validate_name "valid-name" && echo 0 || echo 1)
VALID2=$(_validate_name "valid_name" && echo 0 || echo 1)
VALID3=$(_validate_name "valid123" && echo 0 || echo 1)

# Invalid names
INVALID1=$(_validate_name "invalid/name" && echo 0 || echo 1)
INVALID2=$(_validate_name "invalid.name" && echo 0 || echo 1)
INVALID3=$(_validate_name "invalid name" && echo 0 || echo 1)

# Long name (> 64 chars)
LONG_NAME=$(_validate_name "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" && echo 0 || echo 1)

assert_equals "0" "$VALID1" "Name with hyphen valid"
assert_equals "0" "$VALID2" "Name with underscore valid"
assert_equals "0" "$VALID3" "Name with numbers valid"
assert_equals "1" "$INVALID1" "Name with slash rejected"
assert_equals "1" "$INVALID2" "Name with dot rejected"
assert_equals "1" "$INVALID3" "Name with space rejected"
assert_equals "1" "$LONG_NAME" "Name > 64 chars rejected"

# Test 7: Concurrent IP allocation under lock
echo ""
echo "Test 7: Concurrent IP allocation simulation"
setup_test_env
ensure_state_dirs

# Simulate concurrent allocation with lock protection
allocate_ip_with_lock() {
    acquire_lock 0 || return 1
    local ip
    ip=$(next_ip)
    local name
    name="demo-$(echo "$ip" | tr '.' '-')"
    mkdir -p "$ACTIVE_DIR/$name"
    echo "$ip" > "$ACTIVE_DIR/$name/ip"
    cat > "$ACTIVE_DIR/$name/config" << EOF
DEMO_NAME="$name"
IMAGE_SIZE_MB=2000
EOF
    release_lock
    echo "$ip"
}

# Allocate first IP
IP_A=$(allocate_ip_with_lock)
# Allocate second IP
IP_B=$(allocate_ip_with_lock)
# Allocate third IP
IP_C=$(allocate_ip_with_lock)

assert_equals "10.0.3.2" "$IP_A" "First concurrent IP allocated"
assert_equals "10.0.3.3" "$IP_B" "Second concurrent IP allocated"
assert_equals "10.0.3.4" "$IP_C" "Third concurrent IP allocated"

# Test 8: Lock file cleanup after release
echo ""
echo "Test 8: Lock file cleanup after release"
setup_test_env
ensure_state_dirs

acquire_lock 0
release_lock

# Lock file should still exist (for next acquisition), but lock should be released
# The actual lock is the file descriptor, not the file
assert_file_exists "$LOCK_FILE" "Lock file persists after release (expected)"

# Test 9: Sanitize subdomain function
echo ""
echo "Test 9: Subdomain sanitization"
SUBDOMAIN1=$(sanitize_subdomain "MyDemo-Test")
SUBDOMAIN2=$(sanitize_subdomain "MY.DEMO.TEST")
SUBDOMAIN3=$(sanitize_subdomain "demo_test-123")
SUBDOMAIN4=$(sanitize_subdomain "-demo-")
SUBDOMAIN5=$(sanitize_subdomain "!!!")

assert_equals "mydemo-test" "$SUBDOMAIN1" "Uppercase converted to lowercase"
assert_equals "mydemotest" "$SUBDOMAIN2" "Dots removed"
assert_equals "demotest-123" "$SUBDOMAIN3" "Underscores removed, hyphens preserved"
assert_equals "demo" "$SUBDOMAIN4" "Leading/trailing hyphens removed"
assert_equals "demo" "$SUBDOMAIN5" "Invalid chars fallback to 'demo'"

# Test 10: ensure_state_dirs idempotency
echo ""
echo "Test 10: ensure_state_dirs idempotency"
setup_test_env

ensure_state_dirs
FIRST_RUN=$?
ensure_state_dirs
SECOND_RUN=$?

assert_equals "0" "$FIRST_RUN" "First run creates directories"
assert_equals "0" "$SECOND_RUN" "Second run succeeds (idempotent)"

assert_file_exists "$STATE_DIR/resources" "Resource file created"

###############################################################################
# Summary
###############################################################################

echo ""
echo "=== Concurrency Test Summary ==="
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

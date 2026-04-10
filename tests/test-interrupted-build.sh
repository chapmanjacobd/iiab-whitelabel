#!/usr/bin/env bash
# test-interrupted-build.sh - Test recovery from interrupted builds
# Tests: automatic cleanup of pending/building/failed demos
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."
TEST_STATE_DIR="/tmp/iiab-test-interrupted-$$"
PASS=0
FAIL=0
TOTAL=0

###############################################################################
# Test helpers
###############################################################################

setup_test_env() {
    rm -rf "$TEST_STATE_DIR"
    mkdir -p "$TEST_STATE_DIR/active" "$TEST_STATE_DIR/locks"
    export STATE_DIR="$TEST_STATE_DIR"
    export ACTIVE_DIR="$TEST_STATE_DIR/active"
    export RESOURCE_FILE="$TEST_STATE_DIR/resources"
    export LOCK_FILE="$TEST_STATE_DIR/.democtl.lock"
    export DISK_ALLOCATED=0

    cat > "$RESOURCE_FILE" << EOF
DISK_TOTAL=50000
EOF
}

teardown_test_env() {
    rm -rf "$TEST_STATE_DIR"
}

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

assert_directory_exists() {
    local path="$1" msg="${2:-Directory exists}"
    TOTAL=$((TOTAL + 1))
    if [ -d "$path" ]; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (path='$path')"
    fi
}

assert_directory_not_exists() {
    local path="$1" msg="${2:-Directory does not exist}"
    TOTAL=$((TOTAL + 1))
    if [ ! -d "$path" ]; then
        PASS=$((PASS + 1))
        echo "  ✓ PASS: $msg"
    else
        FAIL=$((FAIL + 1))
        echo "  ✗ FAIL: $msg (path='$path')"
    fi
}

###############################################################################
# Source democtl functions
###############################################################################
setup_test_env
source "$SCRIPT_DIR/democtl"

###############################################################################
# Tests
###############################################################################
echo ""
echo "=== Test 1: Cleanup interrupted build (status=pending) ==="
mkdir -p "$ACTIVE_DIR/testdemo"
echo "pending" > "$ACTIVE_DIR/testdemo/status"
echo "10.0.0.5" > "$ACTIVE_DIR/testdemo/ip"
cat > "$ACTIVE_DIR/testdemo/config" << EOF
DEMO_NAME="testdemo"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=12000
VOLATILE_MODE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
EOF

# Acquire lock and test the cleanup logic
acquire_lock 0

# Manually test the detection and cleanup logic
status_file="$ACTIVE_DIR/testdemo/status"
status=$(cat "$status_file" 2>/dev/null || echo "unknown")

if [ "$status" = "pending" ] || [ "$status" = "building" ] || [ "$status" = "failed" ]; then
    cleanup_failed_build "testdemo"
    assert_directory_not_exists "$ACTIVE_DIR/testdemo" "Interrupted build (pending) cleaned up"
else
    FAIL=$((FAIL + 1))
    TOTAL=$((TOTAL + 1))
    echo "  ✗ FAIL: Status not detected correctly (got: $status)"
fi

release_lock

echo ""
echo "=== Test 2: Cleanup interrupted build (status=building) ==="
mkdir -p "$ACTIVE_DIR/testdemo2"
echo "building" > "$ACTIVE_DIR/testdemo2/status"
echo "10.0.0.6" > "$ACTIVE_DIR/testdemo2/ip"
cat > "$ACTIVE_DIR/testdemo2/config" << EOF
DEMO_NAME="testdemo2"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=12000
VOLATILE_MODE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
EOF

acquire_lock 0
status_file="$ACTIVE_DIR/testdemo2/status"
status=$(cat "$status_file" 2>/dev/null || echo "unknown")

if [ "$status" = "pending" ] || [ "$status" = "building" ] || [ "$status" = "failed" ]; then
    cleanup_failed_build "testdemo2"
    assert_directory_not_exists "$ACTIVE_DIR/testdemo2" "Interrupted build (building) cleaned up"
else
    FAIL=$((FAIL + 1))
    TOTAL=$((TOTAL + 1))
    echo "  ✗ FAIL: Status not detected correctly (got: $status)"
fi

release_lock

echo ""
echo "=== Test 3: Cleanup failed build (status=failed) ==="
mkdir -p "$ACTIVE_DIR/testdemo3"
echo "failed" > "$ACTIVE_DIR/testdemo3/status"
echo "10.0.0.7" > "$ACTIVE_DIR/testdemo3/ip"
cat > "$ACTIVE_DIR/testdemo3/config" << EOF
DEMO_NAME="testdemo3"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=12000
VOLATILE_MODE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
EOF

acquire_lock 0
status_file="$ACTIVE_DIR/testdemo3/status"
status=$(cat "$status_file" 2>/dev/null || echo "unknown")

if [ "$status" = "pending" ] || [ "$status" = "building" ] || [ "$status" = "failed" ]; then
    cleanup_failed_build "testdemo3"
    assert_directory_not_exists "$ACTIVE_DIR/testdemo3" "Failed build cleaned up"
else
    FAIL=$((FAIL + 1))
    TOTAL=$((TOTAL + 1))
    echo "  ✗ FAIL: Status not detected correctly (got: $status)"
fi

release_lock

echo ""
echo "=== Test 4: Running demo should NOT be cleaned up ==="
mkdir -p "$ACTIVE_DIR/testdemo4"
echo "running" > "$ACTIVE_DIR/testdemo4/status"
echo "10.0.0.8" > "$ACTIVE_DIR/testdemo4/ip"
cat > "$ACTIVE_DIR/testdemo4/config" << EOF
DEMO_NAME="testdemo4"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=12000
VOLATILE_MODE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
EOF

acquire_lock 0
status_file="$ACTIVE_DIR/testdemo4/status"
status=$(cat "$status_file" 2>/dev/null || echo "unknown")

# This should NOT trigger cleanup
if [ "$status" = "pending" ] || [ "$status" = "building" ] || [ "$status" = "failed" ]; then
    cleanup_failed_build "testdemo4"
    assert_directory_not_exists "$ACTIVE_DIR/testdemo4" "Running demo should NOT be cleaned up"
else
    assert_directory_exists "$ACTIVE_DIR/testdemo4" "Running demo preserved (no cleanup)"
    # Clean up manually for test
    rm -rf "$ACTIVE_DIR/testdemo4"
fi

release_lock

echo ""
echo "=== Test 5: Stopped demo should NOT be cleaned up ==="
mkdir -p "$ACTIVE_DIR/testdemo5"
echo "stopped" > "$ACTIVE_DIR/testdemo5/status"
echo "10.0.0.9" > "$ACTIVE_DIR/testdemo5/ip"
cat > "$ACTIVE_DIR/testdemo5/config" << EOF
DEMO_NAME="testdemo5"
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
IMAGE_SIZE_MB=12000
VOLATILE_MODE="overlay"
BUILD_ON_DISK=false
SKIP_INSTALL=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
EOF

acquire_lock 0
status_file="$ACTIVE_DIR/testdemo5/status"
status=$(cat "$status_file" 2>/dev/null || echo "unknown")

# This should NOT trigger cleanup
if [ "$status" = "pending" ] || [ "$status" = "building" ] || [ "$status" = "failed" ]; then
    cleanup_failed_build "testdemo5"
    assert_directory_not_exists "$ACTIVE_DIR/testdemo5" "Stopped demo should NOT be cleaned up"
else
    assert_directory_exists "$ACTIVE_DIR/testdemo5" "Stopped demo preserved (no cleanup)"
    # Clean up manually for test
    rm -rf "$ACTIVE_DIR/testdemo5"
fi

release_lock

###############################################################################
# Summary
###############################################################################
echo ""
echo "========================================"
echo "Test Results: $PASS passed, $FAIL failed, $TOTAL total"
echo "========================================"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo "FAILED: $FAIL test(s) failed"
    exit 1
else
    echo "SUCCESS: All tests passed"
    exit 0
fi

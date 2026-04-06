#!/usr/bin/env bash
# demos.sh - Default demo server configuration
# Apply with: democtl apply demos.sh
#
# Each "demo add" line declares a demo. This file is both valid shell
# (source it and the demo function collects the entries) and human-readable
# documentation of what the server should run.

# Global defaults (override per-demo with --flag)
#   --repo          IIAB git repository
#   --branch        Git branch, tag, or ref (refs/pull/NNNN/head for PRs)
#   --volatile      no | yes | state  (default: state)
#   --ram-image     Load image into host tmpfs (default: true)
#   --size          Disk image size in MB
#   --local-vars    Path to IIAB local_vars.yml override
#   --fallback      Mark as fallback for unknown subdomains

demo add small \
  --edition small \
  --branch master \
  --size 12000 \
  --volatile state \
  --ram-image \
  --local-vars vars/local_vars_small.yml

demo add medium \
  --edition medium \
  --branch master \
  --size 20000 \
  --volatile state \
  --ram-image \
  --local-vars vars/local_vars_medium.yml

demo add large \
  --edition large \
  --branch master \
  --size 30000 \
  --volatile state \
  --ram-image \
  --local-vars vars/local_vars_large.yml \
  --fallback

# Example: test a pull request
# demo add pr3612 \
#   --edition large \
#   --repo https://github.com/iiab/iiab.git \
#   --branch refs/pull/3612/head \
#   --size 30000 \
#   --volatile yes \
#   --ram-image \
#   --local-vars vars/local_vars_large.yml \
#   --description "Testing PR #3612"

#!/usr/bin/env bash
# demos.sh - Default demo server configuration
# Apply with: democtl apply demos.sh
#
# Each "demo add" line declares a demo. This file is both valid shell
# (source it and the demo function collects the entries) and human-readable
# documentation of what the server should run.
#
# --local-vars paths are RELATIVE TO THE IIAB REPO that gets cloned into each
# container during build. For example, "vars/local_vars_small.yml" means the
# file must exist at that path inside the upstream IIAB repository
# (e.g., https://github.com/iiab/iiab/tree/master/vars/local_vars_small.yml).
# If the file doesn't exist in the repo at the specified branch/ref, the build
# will fail. Check the IIAB repo for available files before setting this.

# Global defaults (override per-demo with --flag)
#   --repo          IIAB git repository
#   --branch        Git branch, tag, or ref (refs/pull/NNNN/head for PRs)
#   --volatile      no | yes | state  (default: state)
#   --ram-image     Load image into host tmpfs (default: true)
#   --size          Disk image size in MB
#   --local-vars    Path to IIAB local_vars.yml (relative to cloned IIAB repo in container)
#   --fallback      Mark as fallback for unknown subdomains

demo add small \
  --branch master \
  --size 12000 \
  --volatile state \
  --ram-image \
  --local-vars vars/local_vars_small.yml

demo add medium \
  --branch master \
  --size 20000 \
  --volatile state \
  --ram-image \
  --local-vars vars/local_vars_medium.yml

demo add large \
  --branch master \
  --size 30000 \
  --volatile state \
  --ram-image \
  --fallback \
  --local-vars vars/local_vars_large.yml

# Example: test a pull request
# demo add pr3612 \
#   --branch refs/pull/3612/head \
#   --size 30000 \
#   --volatile yes \
#   --ram-image \
#   --local-vars vars/local_vars_large.yml \
#   --description "Testing PR #3612"

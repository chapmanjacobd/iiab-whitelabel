# Internet-in-a-Box (IIAB) Demos

Automated infrastructure for deploying IIAB editions as subdomain-routed containers.

This system manages the full lifecycle of IIAB demo instances on a Debian 13 host. It uses `systemd-nspawn` for isolation, `nginx` for dynamic routing of `*.iiab.io` subdomains, and `certbot` for automated TLS.

## Quick Start

```bash
sudo make small-medium-large
```

## The `democtl` CLI

The `democtl` tool is the primary interface for managing demos.

### Core Commands

| Command                     | Description                                    |
| --------------------------- | ---------------------------------------------- |
| `build <name> [flags]`      | Build a new demo                               |
| `delete <name ... [--all]`  | Stop and delete demo(s)                        |
| `list`                      | Show all demos and resource usage              |
| `status <name>`             | Detailed status of a demo                      |
| `start <name ... [--all]`   | Start stopped demo(s)                          |
| `stop <name ... [--all]`    | Stop a running demo(s)                         |
| `restart <name ... [--all]` | Restart running demo(s)                        |
| `settle [timeout]`          | Wait until all demos reach a settled state     |
| `logs <name>`               | Show build log or container journal            |
| `shell <name>`              | Open a shell in a running container            |
| `cleanup [--all]`           | Clean up failed builds and orphaned subvolumes |
| `rebuild <name ... [--all]` | Delete and re-build demo(s)                    |
| `reload`                    | Regenerate nginx config from active demos      |
| `reconcile`                 | Fix resource counter drift and check ghost IPs |

### Build Flags

| Flag            | Default                     | Description                                          |
| --------------- | --------------------------- | ---------------------------------------------------- |
| `--repo`        | `github.com/iiab/iiab.git`  | Source repository for IIAB.                          |
| `--branch`      | `master`                    | Git ref (branch, tag, or PR head).                   |
| `--description` | _(none)_                    | Human-readable description.                          |
| `--local-vars`  | `vars/local_vars_small.yml` | Path to IIAB configuration variables.                |
| `--size`        | 15000                       | Virtual disk size in MB.                             |
| `--fg`          | _(off)_                     | Build in foreground instead of background.           |
| `--start`       | _(off)_                     | Start the demo after build succeeds.                 |
| `--cleanup`     | _(off)_                     | Delete failed build snapshots immediately on failure |
| `--base`        | _(none)_                    | Build on top of an existing base subvolume.          |
| `--wildcard`    | _(off)_                     | Use as wildcard for unknown subdomains.              |

## Technical Architecture

### Storage

All builds use a single **btrfs file** with copy-on-write (CoW) snapshots:

| Location                        | Backend     | Use                            |
| ------------------------------- | ----------- | ------------------------------ |
| `/run/iiab-demos/storage.btrfs` | tmpfs (RAM) | Default: fast builds in memory |
| `/var/iiab-demos/storage.btrfs` | disk        | Use `--disk` for large builds  |

The Debian base is stored once as a read-only subvolume. Each build is a CoW snapshot -- instant and sharing all unmodified blocks with the base. Final builds are read-only subvolumes symlinked from `/var/lib/machines/<name>` for systemd-nspawn discovery.

### Runtime Persistence

| `--volatile=` Mode  | Rootfs        | Temporary           | Persistent |
| ------------------- | ------------- | ------------------- | ---------- |
| `no`                | direct        | none                | `/`        |
| `state`             | partial tmpfs | `/etc`, `/usr`      | `/var`     |
| `overlay` (default) | overlayfs     | `/` (overlay lower) | none       |
| `yes`               | tmpfs         | `/`                 | none       |

### Network & Routing

- Internal: Containers receive unique IPs from `10.0.3.x`
- External: `democtl reload` dynamically maps subdomains to container IPs via Nginx templates
- Isolation: nftables rules block container-to-container traffic while allowing internet access

## Development & Troubleshooting

### Testing Pull Requests

Test any IIAB PR by pointing `democtl` to the specific git ref:

```bash
democtl build pr123 --branch refs/pull/123/head --description "Testing PR #123"
```

Then visit https://**pr123**.iiab.io/home/

### Logs

- Build: `democtl logs <name>` or `/var/lib/iiab-demos/active/<name>/build.log`
- Runtime: `journalctl -u systemd-nspawn@<name>.service`

### Failed Builds

When a build fails, the snapshot is preserved for inspection:

```bash
sudo democtl status <name>         # Shows status=failed
sudo democtl logs <name>           # Review build log
sudo democtl shell <name>          # Check inside container

# Inspect the failed container
sudo systemd-nspawn -q -D /run/iiab-demos/storage/builds/<name> --boot

# Clean up when done
sudo democtl cleanup --all         # Remove all failed demos and orphaned subvolumes
sudo democtl cleanup --dry-run     # Preview what would be cleaned up
```

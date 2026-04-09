# Internet-in-a-Box (IIAB) Demos

Automated infrastructure for deploying IIAB editions as subdomain-routed containers.

This system manages the full lifecycle of IIAB demo instances on a Debian 13 host. It uses `systemd-nspawn` for isolation, `nginx` for dynamic routing of `*.iiab.io` subdomains, and `certbot` for automated TLS.

## Quick Start

```bash
sudo make install
```

## The `democtl` CLI

The `democtl` tool is the primary interface for managing demos.

### Core Commands

| Command                     | Description                                |
| --------------------------- | ------------------------------------------ |
| `build <name> [flags]`      | Build a new demo                           |
| `remove <name> [name ...]`  | Stop and delete demo(s)                    |
| `rebuild <name> [name ...]` | Remove and re-build demo(s)                |
| `list`                      | Show all demos and resource usage          |
| `status <name>`             | Detailed status for a demo                 |
| `logs <name>`               | Show build log or container journal        |
| `shell <name>`              | Open a shell in a running container        |
| `start <name>`              | Start a stopped demo                       |
| `stop <name>`               | Stop a running demo                        |
| `restart <name>`            | Restart a running demo                     |
| `reload`                    | Regenerate nginx config from active demos  |
| `settle [timeout]`          | Wait until all demos reach a settled state |

### Build Flags

| Flag           | Default                     | Description                               |
| -------------- | --------------------------- | ----------------------------------------- |
| `--repo`       | `github.com/iiab/iiab.git`  | Source repository for IIAB.               |
| `--branch`     | `master`                    | Git ref (branch, tag, or PR head).        |
| `--local-vars` | `vars/local_vars_small.yml` | Path to IIAB configuration variables.     |
| `--size`       | 15000                       | Virtual disk size in MB.                  |
| `--start`      | _(off)_                     | Start the demo after build completes      |
| `--fg`         | _(off)_                     | Build in foreground instead of background |

## Technical Architecture

### Storage

All builds use a single **btrfs file** with copy-on-write (CoW) snapshots:

| Location                        | File         | Use                                    |
| ------------------------------- | ------------ | -------------------------------------- |
| `/run/iiab-demos/storage.btrfs` | tmpfs-backed | Default: fast builds in RAM            |
| `/var/iiab-demos/storage.btrfs` | disk-backed  | Use `--build-on-disk` for large builds |

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
- External: `nginx-gen.sh` dynamically maps subdomains to container IPs
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

### nginx returns 502

```bash
sudo democtl list              # Verify container is running
sudo democtl shell <name>      # Check inside container
```

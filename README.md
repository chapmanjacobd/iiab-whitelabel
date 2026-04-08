# Internet-in-a-Box (IIAB) Demos

Automated infrastructure for deploying IIAB editions as subdomain-routed containers.

This system manages the full lifecycle of IIAB demo instances on a Debian 13 host. It uses `systemd-nspawn` for isolation, `nginx` for dynamic routing of `*.iiab.io` subdomains, and `certbot` for automated TLS.


## Quick Start

```
sudo make install
```

## The `democtl` CLI

The `democtl` tool is the primary interface for managing demos.

### Core Commands
- `add <name> [flags]` -- Build and start a new demo (runs in background).
- `remove <name>` -- Stop, delete, and free all resources.
- `list` / `status <name>` -- Monitor active demos and their build logs.
- `shell <name>` -- Drop directly into a running container.
- `rebuild <name>` -- Refresh a demo while preserving its configuration.
- `reload` -- Manually regenerate Nginx routing from active demos.

### Important Flags
| Flag | Default | Description |
|---|---|---|
| `--repo` | `github.com/iiab/iiab.git` | Source repository for IIAB. |
| `--branch` | `master` | Git ref (branch, tag, or PR head). |
| `--local-vars` | `vars/local_vars_small.yml` | Path to IIAB configuration variables. |
| `--size` | 15000 | Virtual disk size in MB. |

## Technical Architecture

### Storage & Persistence

There are three independent layers of storage handling:

1. Building Storage: Where the image is built.
   - Default (RAM): Builds occur in `/run/iiab-ramfs/` (tmpfs) for maximum speed.
   - Disk Override: Use `--build-on-disk` if host RAM is constrained.

2. Runtime Storage: Where the final image lives.
   - Default (RAM): The final `.raw` image is kept in RAM. Zero disk I/O during execution.
   - Disk Override: Use `--disk-backed` to move the final image to `/var/lib/machines/`.

3. Runtime Persistence: How changes inside the container are handled.

| Mode | Rootfs | ro mounts | rw mounts | Persists across restarts? | Requires bootable /usr? |
|---|---|---|---|---|---|
| `--volatile no` | persistent | none | / (entire root) | Yes | No |
| `--volatile overlay` (Default) | overlay (tmpfs upper) | none (overlay upper is rw) | / (overlay) | No | No |
| `--volatile state` | volatile (tmpfs) | /etc, /usr | /var | /var only | Yes |
| `--volatile yes` | volatile (tmpfs) | none | / (entire root, tmpfs) | Nothing | Yes |

- `no`: The root filesystem is persistent. All changes written to the underlying image survive restarts.
- `overlay`: An overlayfs is placed on top of the rootfs with a tmpfs upper layer. The image stays read-only on disk, but all changes (including `/var`) are discarded when the container is stopped. Works with any rootfs.
- `state`: The system creates a volatile overlay for `/etc` and `/usr`, but `/var` is mounted from a persistent location. Requires a bootable system that can function with only `/usr` in RAM.
- `yes`: The entire root filesystem is volatile in RAM. Requires a bootable system that can function with only `/usr`. Everything resets on every boot.

### Network & Routing
- Internal: Containers receive unique IPs from an internal pool (`10.0.3.x`).
- External: `scripts/nginx-gen.sh` dynamically maps subdomains to container IPs and manages ACME challenge paths for Certbot.



## Development & Troubleshooting

### Testing Pull Requests
Test any IIAB PR by pointing `democtl` to the specific git ref:
```bash
democtl add pr123 --branch refs/pull/123/head --description "Testing PR #123"
```

Then go to https://**pr123**.iiab.io/home/

### Resource Management
`democtl` tracks RAM and disk allocation. Use `democtl list` to see current usage. If a build fails due to memory constraints, use `democtl ramfs cleanup` to clear stale images from tmpfs.

### Logs
- Build: `/var/lib/iiab-demos/active/<name>/build.log` (or `democtl logs <name>`).
- Runtime: `journalctl -u systemd-nspawn@<name>.service`.

### Not enough RAM
```bash
democtl list              # See allocations
democtl remove <name>     # Free resources
democtl ramfs status      # Check tmpfs usage
democtl ramfs cleanup     # Free all RAM
```

### nginx returns 502
```bash
democtl list              # Verify container is running
democtl shell <name>      # Check inside container
```

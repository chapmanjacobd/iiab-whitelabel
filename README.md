# Internet-in-a-Box (IIAB) Demos

Automated infrastructure for deploying IIAB editions as subdomain-routed containers.

This system manages the full lifecycle of IIAB demo instances on a Debian 13 host. It uses `systemd-nspawn` for isolation, `nginx` for dynamic routing of `*.iiab.io` subdomains, and `certbot` for automated TLS.

---

## Quick Start

1. Initialize Host: `sudo democtl init` (Installs packages, configures bridge and Nginx).
2. Deploy Demos: `make small medium large` (Adds standard IIAB configurations).
3. Secure: `make certbot` (Obtains wildcard-ready SSL certificates).

> Pro-tip: Run `sudo make install` to execute all three steps in one shot.

---

## The `democtl` CLI

The `democtl` tool is the primary interface for managing demos.

### Core Commands
- `add <name> [flags]` — Build and start a new demo (runs in background).
- `remove <name>` — Stop, delete, and free all resources.
- `list` / `status <name>` — Monitor active demos and their build logs.
- `shell <name>` — Drop directly into a running container.
- `rebuild <name>` — Refresh a demo while preserving its configuration.
- `reload` — Manually regenerate Nginx routing from active demos.

### Important Flags
| Flag | Default | Description |
|---|---|---|
| `--repo` | `github.com/iiab/iiab.git` | Source repository for IIAB. |
| `--branch` | `master` | Git ref (branch, tag, or PR head). |
| `--local-vars` | `vars/local_vars_small.yml` | Path to IIAB configuration variables. |
| `--size` | 15000 | Virtual disk size in MB. |
| `--disk-backed` | `false` | Store final image on disk instead of RAM (tmpfs). |
| `--build-on-disk`| `false` | Perform build on disk instead of RAM (tmpfs). |
| `--volatile` | `state` | Resilience: `no` (persistent), `state` (reset /var), `yes` (stateless). |

---

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
   - `--volatile state` (Default): The OS is read-only; `/var` is an overlay that resets on every boot.
   - `--volatile no`: All changes are persistent to the underlying image.
   - `--volatile yes`: The entire container is stateless; everything resets on reboot.

### Network & Routing
- Internal: Containers receive unique IPs from an internal pool (`10.0.3.x`).
- External: `scripts/nginx-gen.sh` dynamically maps subdomains to container IPs and manages ACME challenge paths for Certbot.

---

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

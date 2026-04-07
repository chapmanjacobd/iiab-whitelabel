# iiab-whitelabel

Demo server infrastructure for Internet-in-a-Box (IIAB) with subdomain-based container routing.

## Architecture

- **Host**: Debian 13 (amd64) KVM server
- **Containers**: systemd-nspawn containers running different IIAB editions
- **Reverse Proxy**: nginx routes `*.iiab.io` subdomains to containers (dynamically generated)
- **TLS**: Let's Encrypt certificates per subdomain via certbot
- **CLI**: `democtl` manages the entire lifecycle

## Build Model

**All builds happen in RAM (tmpfs) by default** — zero disk I/O during the build.

```
Base image (disk, shared ~500MB)
        ↓
    cp to tmpfs (build workspace)
        ↓
    grow → loop → mount → clone IIAB → install → shrink
        ↓
   ┌──────┴──────┐
   │ --ram-image │  (default)
   │   → RAM     │  final image in /run/iiab-ramfs/
   └─────────────┘

   ┌──────────────┐
   │ (no flag)    │  final image copied to /var/lib/machines/
   │   → disk     │  tmpfs cleaned up
   └──────────────┘
```

After shrinking, the image is either kept in RAM or copied to persistent disk.
Use `--build-on-disk` to override and build on disk instead of RAM.

## Quick Start

### Prerequisites

- Debian 13 (amd64) host with root access
- Wildcard DNS `*.iiab.io` → server IP

### 1. Initialize the host

```bash
sudo democtl init
```

Installs packages, configures bridge networking, sets up nginx skeleton.

### 2. Deploy demos

```bash
# Add all three standard demos
make small medium large
```

### 3. Get SSL certs

```bash
make certbot
```

> **Tip:** Steps 1–3 can be done in one shot with `make install` (requires root).

## democtl CLI

```
democtl init                          Bootstrap host (packages, network, nginx)
democtl add <name> [flags]            Add single demo (background build by default)
democtl remove <name>                 Stop + delete demo + free resources
democtl rebuild <name>                Remove + add (preserves config)
democtl list                          Show all demos
democtl status <name>                 Detailed status + build log
democtl logs <name>                   Build log or container journal
democtl shell <name>                  Open shell in container
democtl reload                        Regenerate nginx from active demos
democtl ramfs <load|unload|status>    Manage tmpfs images
democtl reconcile                     Fix resource counter drift
```

### Add flags

| Flag | Default | Description |
|---|---|---|
| `--repo` | `github.com/iiab/iiab.git` | IIAB git repository |
| `--branch` | `master` | Git branch/tag/ref |
| `--size` | 15000 | Image size in MB |
| `--volatile` | `state` | `no` / `yes` / `state` |
| `--ram-image` | true | Keep final image in host RAM |
| `--no-ram-image` | — | Copy final image to disk |
| `--build-on-disk` | — | Build on disk instead of RAM (override default) |
| `--local-vars` | `vars/local_vars_small.yml` | Path to custom IIAB local_vars.yml |
| `--fallback` | false | Use as fallback for unknown subdomains |
| `--description` | — | Human-readable description |
| `--fg` | false | Build in foreground |

### Examples

```bash
# Standard RAM demo
democtl add small --local-vars vars/local_vars_small.yml --size 12000

# Production: persistent on disk
democtl add production \
  --local-vars vars/local_vars_large.yml \
  --volatile no \
  --no-ram-image

# Build on disk (e.g., low-RAM environment)
democtl add small --local-vars vars/local_vars_small.yml --build-on-disk
```

## Makefile

The Makefile provides convenience targets for initial setup and the three standard demos:

```bash
make install             # Full setup: init → add demos → obtain SSL certs
make init                # Host bootstrap only
make small medium large  # Add standard demos
make status              # Show status (or: make status NAME=small)
make stop                # Stop all running demos
make clean               # Remove everything
make test                # Run syntax/lint checks
```

For everything else, use `democtl` directly:

```bash
democtl list                     # List all demos
democtl logs small               # View build log for a demo
democtl reload                   # Regenerate nginx config
democtl certbot                  # Obtain/renew Let's Encrypt certs
democtl ramfs status             # Check RAM usage
democtl ramfs cleanup            # Free all RAM
democtl reconcile                # Fix resource counter drift
democtl shell small              # Open shell in container
democtl add <name> [flags]       # Add a custom demo
democtl remove <name>            # Remove a demo
democtl rebuild <name>           # Remove and re-add a demo
```

### Custom demos

To add a demo with a custom repo or branch (e.g. testing a pull request), call `democtl add` directly:

```bash
# Test a pull request
democtl add pr3612 \
  --repo https://github.com/iiab/iiab.git \
  --branch refs/pull/3612/head \
  --local-vars vars/local_vars_large.yml \
  --volatile yes \
  --description "Testing PR #3612"
```

Or from a custom fork:

```bash
democtl add myfork \
  --repo https://github.com/myorg/iiab.git \
  --branch feature-branch \
  --local-vars vars/local_vars_small.yml
```

## Directory Structure

```
iiab-whitelabel/
├── democtl                    # Main CLI
├── Makefile                   # CLI wrapper with convenience targets
├── README.md
└── scripts/
    ├── build-container.sh     # Build IIAB (all mount/loop/shrink logic inlined)
    ├── container-service.sh   # Create .nspawn systemd config
    ├── host-setup.sh          # Host provisioning
    ├── certbot-setup.sh       # SSL certificate setup
    ├── ramfs-setup.sh         # Manage tmpfs demo images
    └── nginx-gen.sh           # Dynamic nginx from active demos
```

The `--local-vars` path is **relative to the IIAB repository** cloned into each container during build — e.g., `vars/local_vars_small.yml` must exist at that path inside the IIAB repo at the specified `--branch`/`--repo`.

## Deployment Modes

Two independent toggles:

| `volatile` | `ram-image` | Behavior | Runtime |
|---|---|---|---|
| `no` | `no` | Persistent on disk | Disk-backed |
| `yes` | `no` | Clean boot, image on disk | Disk, stateless |
| `state` | `no` | /var overlay, /usr read-only | Disk, /var resets |
| `no` | `yes` | Persistent in RAM | RAM-backed |
| `yes` | `yes` | Clean boot from RAM | RAM, stateless |
| `state` | `yes` | /var overlay, from RAM | RAM, /var resets |

**Default**: `volatile: state`, `ram-image: true` — OS immutable in RAM, `/var` resets each boot.

## Resource Management

`democtl add` checks resources **before** starting a build:

- **RAM**: All demos build in tmpfs — verifies ≥ size + 512MB available RAM (for container runtime)
- **Disk (non-ram-image only)**: Verifies ≥ size free on `/var/lib/machines` for the final image
- **Disk (ram-image)**: Only needs ~1GB for the shared base image (one-time download)

Insufficient resources print what's needed vs available and abort cleanly. Allocations are tracked per-demo and freed on `remove`.

```bash
democtl list    # Shows RAM allocation per demo
```

## How It Works

### Adding a demo

1. `democtl add` parses args, runs resource pre-flight checks
2. Assigns next free IP from the subnet pool (`10.0.3.2`, `.3`, `.4`...)
3. Writes config to `/var/lib/iiab-demos/active/<name>/`
4. Forks `build-container.sh` in background (returns immediately)
5. Background: copies base image to tmpfs → grows + mounts → clones IIAB → installs → shrinks → registers
6. On success: starts container via systemd-nspawn, regenerates nginx

### nginx generation

`scripts/nginx-gen.sh` reads all active demos from `/var/lib/iiab-demos/active/*/config` and generates:
- One `upstream` + `server` block per demo
- ACME challenge locations on port 80 for all subdomains
- Fallback server for unknown `*.iiab.io` (uses the demo marked `--fallback`)

Called automatically after each demo builds successfully, or via `democtl reload`.

### PR / arbitrary branch safety

- `--branch` can be any git ref: branch, tag, `refs/pull/NNNN/head`, commit hash
- Git only fetches from the explicitly configured `--repo` URL
- Each build runs in an isolated nspawn container with its own network namespace

## Troubleshooting

### Build failed
```bash
democtl status <name>     # Check status
democtl logs <name>       # View build log
tail -f /var/lib/iiab-demos/active/<name>/build.log
```

### Not enough RAM
```bash
democtl list              # See allocations
democtl remove <name>     # Free resources
democtl ramfs status      # Check tmpfs usage
democtl ramfs cleanup     # Free all RAM
```

### Container won't start
```bash
journalctl -u systemd-nspawn@<name>.service
cat /etc/systemd/nspawn/<name>.nspawn
```

### nginx returns 502
```bash
democtl list              # Verify container is running
democtl shell <name>      # Check inside container
```

## License

Same as IIAB: MIT License

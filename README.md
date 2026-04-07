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
# Apply all demos from demos.sh
make deploy

# Or add individually
make small
make large
```

### 3. Check status

```bash
make list
make status NAME=small
```

### 4. Get SSL certs

```bash
make certbot
```

## democtl CLI

```
democtl init                          Bootstrap host (packages, network, nginx)
democtl apply [demos.sh]              Ensure all demos in config are running
democtl add <name> [flags]            Add single demo (background build by default)
democtl remove <name>                 Stop + delete demo + free resources
democtl rebuild <name>                Remove + add (preserves config)
democtl list                          Show all demos
democtl status <name>                 Detailed status + build log
democtl logs <name>                   Build log or container journal
democtl shell <name>                  Open shell in container
democtl reload                        Regenerate nginx from active demos
democtl certbot                       Obtain/renew Let's Encrypt certs
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
| `--local-vars` | `vars/local_vars_<name>.yml` | Path to IIAB local_vars.yml |
| `--fallback` | false | Use as fallback for unknown subdomains |
| `--description` | — | Human-readable description |
| `--fg` | false | Build in foreground |

### Examples

```bash
# Standard RAM demo
democtl add small --local-vars vars/local_vars_small.yml --size 12000

# Test a pull request
democtl add pr3612 \
  --local-vars vars/local_vars_large.yml \
  --branch refs/pull/3612/head \
  --volatile yes \
  --description "Testing PR #3612"

# Production: persistent on disk
democtl add production \
  --local-vars vars/local_vars_large.yml \
  --volatile no \
  --no-ram-image

# Build on disk (e.g., low-RAM environment)
democtl add small --local-vars vars/local_vars_small.yml --build-on-disk
```

## Makefile shortcuts

The `small`, `medium`, and `large` targets add individual demos. `deploy` reads `demos.sh`:

```bash
make small medium large    # Add all three demos
make deploy                # Apply demos.sh (add missing, remove extras, reload nginx)
make list                  # List all demos
make status                # Show status of all demos
make logs NAME=small       # View logs for a demo
make ramfs-status          # Check RAM usage
make stop                  # Stop all demos
make clean                 # Remove everything
```

## Directory Structure

```
iiab-whitelabel/
├── democtl                    # Main CLI
├── demos.sh                   # Default demo config (for `democtl apply`)
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

## Makefile shortcuts

```bash
make init             # Bootstrap host
make deploy           # Apply all demos (add missing, remove extras, reload nginx)
make small medium large  # Add individual demos
make list             # List all demos
make status           # Show status of all demos
make logs NAME=small  # View logs for a demo
make ramfs-status     # Check RAM usage
make stop             # Stop all demos
make clean            # Remove everything
```

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

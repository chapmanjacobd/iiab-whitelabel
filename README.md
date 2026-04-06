# iiab-whitelabel

Demo server infrastructure for Internet-in-a-Box (IIAB) with subdomain-based container routing.

## Architecture

- **Host**: Debian 13 (amd64) KVM server
- **Containers**: systemd-nspawn containers running different IIAB editions
- **Reverse Proxy**: nginx routes `*.iiab.io` subdomains to containers (dynamically generated)
- **TLS**: Let's Encrypt certificates per subdomain via certbot
- **CLI**: `democtl` manages the entire lifecycle — no hardcoded playbooks

## Quick Start

### Prerequisites

- Debian 13 (amd64) host with root access
- Wildcard DNS `*.iiab.io` → server IP

### 1. Initialize the host

```bash
sudo bash democtl init
```

Installs packages, configures bridge networking, sets up nginx skeleton.

### 2. Deploy demos

```bash
# Apply all demos from demos.sh
sudo bash democtl apply demos.sh

# Or add individually
sudo bash democtl add small
sudo bash democtl add large
```

`apply` reads `demos.sh`, adds any missing demos, removes any extras, and regenerates nginx.

### 3. Check status

```bash
sudo bash democtl list
sudo bash democtl status small
```

### 4. Get SSL certs

```bash
sudo bash democtl certbot
```

## democtl CLI

```
democtl init                          Bootstrap host (packages, network, nginx)
democtl apply [demos.sh]              Ensure all demos in config are running
democtl add <name> [flags]            Add single demo (--bg default, returns immediately)
democtl remove <name>                 Stop + delete demo + free resources
democtl rebuild <name>                Remove + add
democtl list                          Show all demos
democtl status <name>                 Detailed status + build log
democtl logs <name>                   Build log or container journal
democtl shell <name>                  machinectl shell
democtl reload                        Regenerate nginx from active demos
democtl certbot                       Obtain/renew Let's Encrypt certs
democtl ramfs <load|unload|status>    Manage tmpfs images
```

### Add flags

| Flag | Default | Description |
|---|---|---|
| `--edition` | *name* | IIAB edition: small, medium, large |
| `--repo` | `github.com/iiab/iiab.git` | IIAB git repository |
| `--branch` | `master` | Git branch/tag/ref |
| `--size` | 15000 | Image size in MB |
| `--volatile` | `state` | `no` / `yes` / `state` |
| `--ram-image` | true | Load image into host tmpfs |
| `--no-ram-image` | — | Keep image on disk |
| `--local-vars` | `vars/local_vars_<ed>.yml` | Path to IIAB local_vars.yml |
| `--fallback` | false | Use as fallback for unknown subdomains |
| `--description` | — | Human-readable description |
| `--fg` | false | Build in foreground |

### Examples

```bash
# Standard demo
democtl add small --edition small --size 12000

# Test a pull request (isolated, safe — git only fetches from configured repo)
democtl add pr3612 \
  --edition large \
  --branch refs/pull/3612/head \
  --volatile yes \
  --description "Testing PR #3612"

# Test a feature branch
democtl add new-maps \
  --branch feature/new-maps \
  --volatile state

# Production-grade: persistent on disk
democtl add production \
  --edition large \
  --volatile no \
  --no-ram-image
```

## demos.sh — Default configuration

```bash
# demos.sh - declarative demo definitions
demo add small \
  --edition small --branch master --size 12000 \
  --volatile state --ram-image \
  --local-vars vars/local_vars_small.yml

demo add medium \
  --edition medium --branch master --size 20000 \
  --volatile state --ram-image \
  --local-vars vars/local_vars_medium.yml

demo add large \
  --edition large --branch master --size 30000 \
  --volatile state --ram-image --fallback \
  --local-vars vars/local_vars_large.yml
```

Run `democtl apply demos.sh` to ensure these are all running. Add or remove demos in the file, then re-apply.

## Directory Structure

```
iiab-whitelabel/
├── democtl                    # Main CLI
├── demos.sh                   # Default demo config
├── Makefile                   # Thin wrapper around democtl
├── README.md
├── .gitignore
├── playbooks/
│   ├── 01-host-setup.yml      # Host provisioning (called by democtl init)
│   └── 06-certbot.yml         # SSL certs (called by democtl certbot)
├── scripts/
│   ├── build-container.sh     # Build IIAB inside nspawn (arbitrary repo/branch)
│   ├── container-service.sh   # Create .nspawn systemd config
│   ├── ramfs-setup.sh         # Manage tmpfs image loading
│   └── nginx-gen.sh           # Dynamic nginx from active demos
└── vars/
    ├── local_vars_small.yml
    ├── local_vars_medium.yml
    └── local_vars_large.yml
```

## Deployment Modes

Two independent toggles:

| `volatile` | `ram_image` | Behavior | Disk I/O | Speed |
|---|---|---|---|---|
| `no` | `no` | Persistent on disk | Writes | Normal |
| `yes` | `no` | Clean boot, image on disk | Read-only | Normal |
| `state` | `no` | /var overlay, /usr read-only | Read-only | Normal |
| `no` | `yes` | Persistent in RAM | No (after copy) | Fast |
| `yes` | `yes` | Clean boot from RAM | None | **Fastest** |
| `state` | `yes` | /var overlay from RAM | None | Fast |

**Default**: `volatile: state, ram_image: true` — OS immutable in RAM, `/var` resets each boot.

## Resource Management

`democtl add` checks resources **before** starting a build:

- **Disk**: If `--no-ram-image`, verifies ≥ size + 2GB free on `/var/lib/machines`
- **RAM**: If `--ram-image`, verifies ≥ size + 512MB available RAM

If resources are insufficient, it prints what's needed vs available and aborts cleanly. Allocations are tracked per-demo and freed on `democtl remove`.

```bash
democtl list    # Shows RAM allocation per demo
```

## How It Works

### Adding a demo

1. `democtl add` parses args, runs resource pre-flight checks
2. Assigns next free IP from the subnet pool (`10.0.3.2`, `.3`, `.4`...)
3. Writes config to `/var/lib/iiab-demos/active/<name>/`
4. Forks `build-container.sh` in background (returns immediately)
5. Background: clones IIAB → installs in nspawn → shrinks image → imports with machinectl
6. On success: registers container, starts it, regenerates nginx

### nginx generation

`scripts/nginx-gen.sh` reads all active demos from `/var/lib/iiab-demos/active/*/config` and generates:
- One `upstream` + `server` block per demo
- ACME challenge locations on port 80 for all subdomains
- Fallback server for unknown `*.iiab.io` (uses the demo marked `--fallback`)

Called automatically after each demo builds successfully, or via `democtl reload`.

### PR / arbitrary branch safety

- `--branch` can be any git ref: branch, tag, `refs/pull/NNNN/head`, commit hash
- Git only fetches from the explicitly configured `--repo` URL
- The `--risky` IIAB installer flag is controlled by the host admin
- Each build runs in an isolated nspawn container with its own network namespace

## Makefile shortcuts

```bash
make init             # Bootstrap host
make deploy           # Apply demos.sh
make list             # List all demos
make add-small        # Add small demo
make rebuild-large    # Rebuild large demo
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
machinectl shell <name> ip addr
```

## License

Same as IIAB: MIT License

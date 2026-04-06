# iiab-whitelabel

Demo server infrastructure for Internet-in-a-Box (IIAB) with subdomain-based container routing.

## Architecture

- **Host**: Debian 13 (amd64) KVM server at `104.255.228.224`
- **Containers**: 3x systemd-nspawn containers running different IIAB editions
- **Reverse Proxy**: nginx on the host routes `*.iiab.io` subdomains to containers

### Subdomain Routing

| Subdomain | Container | IIAB Edition | Description |
|---|---|---|---|
| `small.iiab.io` | `small` | Small | Core services only (Kiwix, Kolibri, Calibre-Web, Admin Console) |
| `medium.iiab.io` | `medium` | Medium | Small + Nextcloud, WordPress, Sugarizer, Transmission |
| `large.iiab.io` | `large` | Large | Full install (Gitea, JupyterHub, MediaWiki, Moodle, etc.) |
| `*.iiab.io` (other) | — | — | Redirects to `large.iiab.io` |

## Quick Start

### Prerequisites

- Debian 13 (amd64) host with root access
- Wildcard DNS `*.iiab.io` → server IP
- `ansible`, `systemd-container`, `nginx` installed

### Setup Host

```bash
ansible-playbook -i hosts/inventory.yml playbooks/01-host-setup.yml
```

### Build Container Images

```bash
# Build all three editions
make build-all

# Or build individually
make build-small
make build-medium
make build-large
```

### Deploy Containers

```bash
ansible-playbook -i hosts/inventory.yml playbooks/05-deploy-containers.yml
```

### Verify

```bash
# Check container status
machinectl list

# Check nginx proxy
curl -H "Host: small.iiab.io" http://104.255.228.224/
curl -H "Host: large.iiab.io" http://104.255.228.224/
```

## Directory Structure

```
iiab-whitelabel/
├── README.md
├── Makefile
├── hosts/
│   └── inventory.yml
├── playbooks/
│   ├── 01-host-setup.yml
│   ├── 02-build-small.yml
│   ├── 03-build-medium.yml
│   ├── 04-build-large.yml
│   └── 05-deploy-containers.yml
├── vars/
│   ├── containers.yml
│   ├── local_vars_small.yml
│   ├── local_vars_medium.yml
│   └── local_vars_large.yml
├── nginx/
│   └── iiab-demo.conf
└── scripts/
    ├── build-container.sh
    └── container-service.sh
```

## Container Networking

Containers communicate via a private bridge network (`10.0.3.0/24`):

| Container | IP | Port |
|---|---|---|
| small | 10.0.3.10 | 80 |
| medium | 10.0.3.20 | 80 |
| large | 10.0.3.30 | 80 |

## Rebuilding

To update an IIAB edition with the latest code:

```bash
make rebuild-small    # Destroy + rebuild small container
make rebuild-medium
make rebuild-large
```

## License

Same as IIAB: MIT License

.PHONY: help setup build-all deploy-all \
        build-small build-medium build-large \
        rebuild-small rebuild-medium rebuild-large \
        status stop shell logs clean

# Default target
help:
	@echo "IIAB Whitelabel Demo Server"
	@echo ""
	@echo "Setup:"
	@echo "  setup           Configure host (nginx, networking, nspawn)"
	@echo ""
	@echo "Build:"
	@echo "  build-small     Build small IIAB container image"
	@echo "  build-medium    Build medium IIAB container image"
	@echo "  build-large     Build large IIAB container image"
	@echo "  build-all       Build all three container images"
	@echo ""
	@echo "Deploy:"
	@echo "  deploy-all      Import and start all containers"
	@echo ""
	@echo "Rebuild (destroy + build):"
	@echo "  rebuild-small   Destroy and rebuild small container"
	@echo "  rebuild-medium  Destroy and rebuild medium container"
	@echo "  rebuild-large   Destroy and rebuild large container"
	@echo ""
	@echo "Operations:"
	@echo "  status          Show running containers"
	@echo "  stop            Stop all containers"
	@echo "  shell-small     Get shell into small container"
	@echo "  shell-medium    Get shell into medium container"
	@echo "  shell-large     Get shell into large container"
	@echo "  logs-small      Show small container journal logs"
	@echo "  logs-medium     Show medium container journal logs"
	@echo "  logs-large      Show large container journal logs"
	@echo ""
	@echo "Cleanup:"
	@echo "  clean           Remove all container images and services"

# Host setup
setup:
	ansible-playbook -i hosts/inventory.yml playbooks/01-host-setup.yml

# Build containers
build-small:
	@mkdir -p scripts
	bash scripts/build-container.sh small

build-medium:
	bash scripts/build-container.sh medium

build-large:
	bash scripts/build-container.sh large

build-all: build-small build-medium build-large

# Deploy
deploy-all:
	ansible-playbook -i hosts/inventory.yml playbooks/05-deploy-containers.yml

# Rebuild (destroy first)
rebuild-small:
	-machinectl terminate iiab-small 2>/dev/null || true
	-machinectl remove iiab-small 2>/dev/null || true
	-rm -f /var/lib/machines/iiab-small.raw
	-rm -f /etc/systemd/nspawn/iiab-small.nspawn
	bash scripts/build-container.sh small

rebuild-medium:
	-machinectl terminate iiab-medium 2>/dev/null || true
	-machinectl remove iiab-medium 2>/dev/null || true
	-rm -f /var/lib/machines/iiab-medium.raw
	-rm -f /etc/systemd/nspawn/iiab-medium.nspawn
	bash scripts/build-container.sh medium

rebuild-large:
	-machinectl terminate iiab-large 2>/dev/null || true
	-machinectl remove iiab-large 2>/dev/null || true
	-rm -f /var/lib/machines/iiab-large.raw
	-rm -f /etc/systemd/nspawn/iiab-large.nspawn
	bash scripts/build-container.sh large

# Operations
status:
	@echo "=== Running Containers ==="
	@machinectl list
	@echo ""
	@echo "=== Container Images ==="
	@ls -lh /var/lib/machines/*.raw 2>/dev/null || echo "No container images found"
	@echo ""
	@echo "=== nginx Status ==="
	@systemctl is-active nginx

stop:
	machinectl terminate iiab-small
	machinectl terminate iiab-medium
	machinectl terminate iiab-large

shell-small:
	machinectl shell iiab-small

shell-medium:
	machinectl shell iiab-medium

shell-large:
	machinectl shell iiab-large

logs-small:
	machinectl status iiab-small

logs-medium:
	machinectl status iiab-medium

logs-large:
	machinectl status iiab-large

# Cleanup
clean:
	-machinectl terminate iiab-small iiab-medium iiab-large 2>/dev/null || true
	-machinectl remove iiab-small iiab-medium iiab-large 2>/dev/null || true
	-rm -f /var/lib/machines/iiab-*.raw
	-rm -f /etc/systemd/nspawn/iiab-*.nspawn
	-rm -rf /etc/systemd/system/systemd-nspawn@iiab-*.service.d
	-systemctl daemon-reload
	@echo "All container images and configurations removed"

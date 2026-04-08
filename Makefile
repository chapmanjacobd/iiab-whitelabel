# IIAB Whitelabel Demo Server
# CLI wrapper with convenience targets

.PHONY: help init install small medium large status test stop clean

# Default target
help:
	bash democtl help

# Full one-time setup: init → add demos → wait for builds → obtain SSL certs
install:
	bash democtl init
	make small medium large
	bash democtl settle
	bash scripts/certbot-setup.sh

# Host bootstrap (packages, network, nginx)
init:
	bash democtl init

# Convenience targets — add a single demo
small:
	bash democtl add small \
		--size 12000 \
		--local-vars vars/local_vars_small.yml

medium:
	bash democtl add medium \
		--size 20000 \
		--local-vars vars/local_vars_medium.yml

large:
	bash democtl add large \
		--size 30000 \
		--wildcard \
		--local-vars vars/local_vars_large.yml

# Status of all demos (or specify a name with NAME=)
status:
	@if [ -n "$(NAME)" ]; then \
		bash democtl status "$(NAME)"; \
	else \
		bash democtl list; \
	fi

# Testing
test:
	@echo "Checking syntax..."
	bash -n democtl
	for f in scripts/*.sh; do bash -n "$$f"; done
	@echo "Running shellcheck..."
	shellcheck democtl scripts/*.sh
	@echo "Testing help..."
	bash democtl help >/dev/null
	@echo "All local tests passed."

# Stop all running demos
stop:
	@for dir in /var/lib/iiab-demos/active/*/; do \
		[ -d "$$dir" ] || continue; \
		name=$$(basename "$$dir"); \
		echo "Stopping $$name..."; \
		machinectl terminate "$$name" 2>/dev/null || true; \
	done

# Full cleanup
clean:
	bash democtl remove small 2>/dev/null || true
	bash democtl remove medium 2>/dev/null || true
	bash democtl remove large 2>/dev/null || true
	bash democtl ramfs cleanup 2>/dev/null || true
	@echo "All demos removed."

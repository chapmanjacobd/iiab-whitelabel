# IIAB Whitelabel Demo Server
# CLI wrapper with convenience targets

.PHONY: help init list status logs reload certbot stop clean reconcile test \
        small medium large

# Default target
help:
	bash democtl help

# Host bootstrap
init:
	bash democtl init

# Convenience targets — add a single demo
small:
	bash democtl add small \
		--branch master \
		--size 12000 \
		--volatile state \
		--ram-image \
		--local-vars vars/local_vars_small.yml

medium:
	bash democtl add medium \
		--branch master \
		--size 20000 \
		--volatile state \
		--ram-image \
		--local-vars vars/local_vars_medium.yml

large:
	bash democtl add large \
		--branch master \
		--size 30000 \
		--volatile state \
		--ram-image \
		--fallback \
		--local-vars vars/local_vars_large.yml

# List all demos
list:
	bash democtl list

# Status of all demos (or specify a name with NAME=)
status:
	@if [ -n "$(NAME)" ]; then \
		bash democtl status "$(NAME)"; \
	else \
		bash democtl list; \
	fi

# Logs for a specific demo (make logs NAME=small)
logs:
	bash democtl logs $(or $(NAME),)

# Operations
reload:
	bash democtl reload

certbot:
	bash democtl certbot

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

# RAMFS management (direct passthrough)
ramfs-load:
	bash democtl ramfs load

ramfs-unload:
	bash democtl ramfs unload

ramfs-status:
	bash democtl ramfs status

ramfs-cleanup:
	bash democtl ramfs cleanup

# Reconcile resource counters
reconcile:
	bash democtl reconcile

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

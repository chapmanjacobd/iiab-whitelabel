# IIAB Whitelabel Demo Server
# Thin wrapper around democtl CLI

.PHONY: help init deploy list status apply \
        add-small add-medium add-large \
        remove-small remove-medium remove-large \
        rebuild-small rebuild-medium rebuild-large \
        ramfs-load ramfs-unload ramfs-status ramfs-cleanup \
        reload certbot stop clean reconcile

# Default target
help:
	bash democtl help

# Host bootstrap
init:
	bash democtl init

# Apply default config
deploy:
	bash democtl apply demos.sh

# List all demos
list:
	bash democtl list

# Status of all demos (or specify a name)
status:
	@if [ -n "$(filter-out status,$(MAKECMDGOALS))" ]; then \
		bash democtl status "$(filter-out status,$(MAKECMDGOALS))"; \
	else \
		bash democtl list; \
	fi

# Convenience targets for the three default demos
add-small:
	bash democtl add small

add-medium:
	bash democtl add medium

add-large:
	bash democtl add large

remove-small:
	bash democtl remove small

remove-medium:
	bash democtl remove medium

remove-large:
	bash democtl remove large

rebuild-small:
	bash democtl rebuild small

rebuild-medium:
	bash democtl rebuild medium

rebuild-large:
	bash democtl rebuild large

# Operations
shell-small:
	bash democtl shell small

shell-medium:
	bash democtl shell medium

shell-large:
	bash democtl shell large

logs:
	bash democtl logs $(or $(NAME),)

# Infrastructure
reload:
	bash democtl reload

certbot:
	bash democtl certbot

# RAMFS management
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

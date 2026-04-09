# IIAB Whitelabel Demo Server
# CLI wrapper with convenience targets

.PHONY: help init install small medium large status test test-concurrency test-e2e test-nginx test-all stop clean

# Default target
help:
	bash democtl help

# Full one-time setup: init → build demos → wait for builds → start → obtain SSL certs
install:
	bash democtl init
	make small medium large
	bash democtl settle
	bash democtl start small medium large
	bash scripts/certbot-setup.sh

# Host bootstrap (packages, network, nginx)
init:
	bash democtl init

# Convenience targets -- build a single demo
small:
	bash democtl build small \
		--size 12000 \
		--local-vars vars/local_vars_small.yml

medium:
	bash democtl build medium \
		--size 20000 \
		--local-vars vars/local_vars_medium.yml

large:
	bash democtl build large \
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

# Individual test targets
test-concurrency:
	@echo "Running concurrency tests..."
	bash tests/test-concurrency.sh

test-e2e:
	@echo "Running e2e tests..."
	bash tests/test-e2e.sh

test-nginx:
	@echo "Running nginx generation tests..."
	bash tests/test-nginx-gen.sh

# Run all tests
test-all: test
	@echo ""
	@echo "=== Running all test suites ==="
	bash tests/test-concurrency.sh
	bash tests/test-e2e.sh
	bash tests/test-nginx-gen.sh
	@echo ""
	@echo "✅ All test suites completed successfully."

# Stop all running demos
stop:
	@for dir in /var/lib/iiab-demos/active/*/; do \
		[ -d "$$dir" ] || continue; \
		name=$$(basename "$$dir"); \
		echo "Stopping $$name..."; \
		bash democtl stop "$$name" 2>/dev/null || true; \
	done

# Full cleanup
clean:
	bash democtl remove small 2>/dev/null || true
	bash democtl remove medium 2>/dev/null || true
	bash democtl remove large 2>/dev/null || true
	@echo "All demos removed."

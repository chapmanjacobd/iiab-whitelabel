# Variables
BINARY=democtl
DESTDIR?=/usr/local/bin

# Phony targets
.PHONY: help build install clean \
        test integration \
        fmt lint deps \
        init small medium large small-medium-large \
        status logs shell \
        start stop restart \
        rebuild reload reconcile cleanup

# Default target
build:
	go build -o $(BINARY) .

all: fmt lint build test

help:
	@echo "Build & Install:"
	@echo "  build              Build the $(BINARY) binary"
	@echo "  install            Install $(BINARY) to $(DESTDIR)"
	@echo "  clean              Remove build artifacts"
	@echo ""
	@echo "Development:"
	@echo "  test               Run all unit tests"
	@echo "  integration        Run all integration tests (requires root)"
	@echo "  fmt                Format code"
	@echo "  lint               Run linter with auto-fix"
	@echo "  deps               Install dev tooling"
	@echo ""
	@echo "Runtime (requires root):"
	@echo "  init               Initialize host for IIAB demos"
	@echo "  small              Build small demo"
	@echo "  medium             Build medium demo (on top of small)"
	@echo "  large              Build large demo (on top of medium)"
	@echo "  small-medium-large Build all demos in sequence"
	@echo "  status [NAME=]     Show demo status (or list all)"
	@echo "  logs [NAME=]       Show demo logs"
	@echo "  shell NAME=        Open a shell in a running container"
	@echo "  start/stop/restart [NAME=] Manage demos (default: --all)"
	@echo "  rebuild [NAME=]    Delete and re-build demo(s)"
	@echo "  reload             Regenerate nginx config"
	@echo "  reconcile          Fix resource counter drift"
	@echo "  cleanup            Clean up failed builds and orphans"

# Build & Install
install: build
	sudo install -m 0755 $(BINARY) $(DESTDIR)/$(BINARY)

clean:
	rm -f $(BINARY)

# Development
test:
	go test ./internal/... ./cmd/... -count=1

integration:
	sudo go test ./tests/... -count=1 -v

fmt:
	golangci-lint fmt
	go fix ./...

lint:
	golangci-lint run --fix ./...

deps:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install gotest.tools/gotestsum@latest

# Runtime targets
init: build
	sudo ./$(BINARY) init

small: build
	sudo ./$(BINARY) build small --size 12000 --local-vars vars/local_vars_small.yml

medium: build
	sudo ./$(BINARY) build medium --base small --size 8000 --local-vars vars/local_vars_medium.yml

large: build
	sudo ./$(BINARY) build large --base medium --size 10000 --wildcard --local-vars vars/local_vars_large.yml

small-medium-large: init
	make small
	sudo ./$(BINARY) settle
	make medium
	sudo ./$(BINARY) settle
	make large
	sudo ./$(BINARY) settle
	sudo ./$(BINARY) start small medium large

status: build
	@if [ -n "$(NAME)" ]; then \
		sudo ./$(BINARY) status "$(NAME)"; \
	else \
		sudo ./$(BINARY) list; \
	fi

logs: build
	@if [ -n "$(NAME)" ]; then \
		sudo ./$(BINARY) logs "$(NAME)"; \
	else \
		sudo ./$(BINARY) logs; \
	fi

shell: build
	@if [ -n "$(NAME)" ]; then \
		sudo ./$(BINARY) shell "$(NAME)"; \
	else \
		echo "Error: NAME=<demo-name> is required for shell"; \
		exit 1; \
	fi

start: build
	@if [ -n "$(NAME)" ]; then \
		sudo ./$(BINARY) start "$(NAME)"; \
	else \
		sudo ./$(BINARY) start --all; \
	fi

stop: build
	@if [ -n "$(NAME)" ]; then \
		sudo ./$(BINARY) stop "$(NAME)"; \
	else \
		sudo ./$(BINARY) stop --all; \
	fi

restart: build
	@if [ -n "$(NAME)" ]; then \
		sudo ./$(BINARY) restart "$(NAME)"; \
	else \
		sudo ./$(BINARY) restart --all; \
	fi

rebuild: build
	@if [ -n "$(NAME)" ]; then \
		sudo ./$(BINARY) rebuild "$(NAME)"; \
	else \
		sudo ./$(BINARY) rebuild --all; \
	fi

reload: build
	sudo ./$(BINARY) reload

reconcile: build
	sudo ./$(BINARY) reconcile

uninstall: build
	sudo ./$(BINARY) cleanup --all
	sudo rm -f ./$(BINARY)

.PHONY: all
all: build

.PHONY: help
help: ## Display this help.
	@echo "Usage:"
	@echo "  make <target>"
	@echo ""
	@echo "Targets:"
	@echo "  build    Build manager binary"
	@echo "  run      Run manager from host"
	@echo "  fmt      Run go fmt against code"
	@echo "  vet      Run go vet against code"
	@echo "  help     Display this help"

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: build
build: fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: fmt vet ## Run manager from host.
	go run ./cmd/main.go -redis-addr="100.75.179.29:6379"

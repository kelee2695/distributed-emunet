.PHONY: all build clean test docker-build docker-push help controller linkserver node-agent emu-cni debug-cni emunet-gen

BINARY_DIR := bin
GO := go
GOFLAGS := -v
LDFLAGS := -s -w

VERSION := latest
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

REGISTRY := 100.75.179.29:5000

LDFLAGS += -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)

# Controller-gen tool setup
LOCALBIN ?= $(shell pwd)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.16.0

# Path variables
CONTROLLER_DIR := master/controller
LINKSERVER_DIR := master/linkserver
NODE_AGENT_DIR := node/emunet-node-agent
EMU_CNI_DIR := emu-cni
DEBUG_CNI_DIR := debug-cni
EMUNET_GEN_DIR := emunet-gen
DEPLOY_DIR := deploy

CONTROLLER_CMD := $(CONTROLLER_DIR)/cmd/main.go
LINKSERVER_CMD := $(LINKSERVER_DIR)/cmd/main.go
NODE_AGENT_CMD := $(NODE_AGENT_DIR)/cmd/main.go
EMU_CNI_CMD := $(EMU_CNI_DIR)/cmd/main.go
DEBUG_CNI_CMD := $(DEBUG_CNI_DIR)/main.go
EMUNET_GEN_CMD := $(EMUNET_GEN_DIR)/main.go

CONTROLLER_TEST := $(CONTROLLER_DIR)/internal/controller/...
LINKSERVER_TEST := $(LINKSERVER_DIR)/internal/api/...
NODE_AGENT_TEST := $(NODE_AGENT_DIR)/internal/api/... $(NODE_AGENT_DIR)/pkg/...

EMU_CNI_EBPF := $(EMU_CNI_DIR)/tools/ebpf/ebpf-tc/
CONTROLLER_BOILERPLATE := ./$(CONTROLLER_DIR)/hack/boilerplate.go.txt
CONTROLLER_PKG := ./$(CONTROLLER_DIR)/...

DEPLOY_CRDS := ./$(DEPLOY_DIR)/crds
DEPLOY_CONTROLLER := ./$(DEPLOY_DIR)/controller
DEPLOY_LINKSERVER := ./$(DEPLOY_DIR)/linkserver
DEPLOY_NODE_AGENT := ./$(DEPLOY_DIR)/emunet-node-agent

all: build

help:
	@echo "EmuNet - Distributed Network Simulation for Kubernetes"
	@echo ""
	@echo "Usage:"
	@echo "  make all              - Build all components"
	@echo "  make build            - Build all components"
	@echo "  make controller       - Build controller"
	@echo "  make linkserver       - Build linkserver"
	@echo "  make node-agent       - Build emunet-node-agent"
	@echo "  make emu-cni          - Build emu-cni"
	@echo "  make debug-cni        - Build debug-cni"
	@echo "  make emunet-gen       - Build emunet-gen"
	@echo ""
	@echo "  make test             - Run all tests"
	@echo "  make test-controller  - Run controller tests"
	@echo "  make test-linkserver  - Run linkserver tests"
	@echo "  make test-node-agent  - Run node-agent tests"
	@echo ""
	@echo "  make clean            - Clean build artifacts"
	@echo "  make tidy             - Tidy go modules"
	@echo "  make fmt              - Format code"
	@echo "  make lint             - Run linter"
	@echo "  make vet              - Run go vet"
	@echo "  make generate         - Generate code"
	@echo "  make manifests        - Generate CRD manifests"
	@echo "  make controller-gen   - Download controller-gen tool"
	@echo ""
	@echo "  make docker-build      - Build all Docker images"
	@echo "  make docker-push       - Push all Docker images"
	@echo ""
	@echo "  make deploy           - Deploy all components to Kubernetes"
	@echo "  make deploy-controller - Deploy controller to Kubernetes"
	@echo "  make deploy-linkserver - Deploy linkserver to Kubernetes"
	@echo "  make deploy-node-agent - Deploy node-agent to Kubernetes"
	@echo ""
	@echo "  make clean-registry   - Clear all emunet images from registry"

build: controller linkserver node-agent emu-cni debug-cni emunet-gen

controller:
	@echo "Building controller..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/controller $(CONTROLLER_CMD)

linkserver:
	@echo "Building linkserver..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/linkserver $(LINKSERVER_CMD)

node-agent:
	@echo "Building emunet-node-agent..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/emunet-node-agent $(NODE_AGENT_CMD)

emu-cni:
	@echo "Building emu-cni..."
	@mkdir -p $(BINARY_DIR)
	cd $(EMU_CNI_DIR) && go generate ./tools/ebpf/ebpf-tc/
	$(GO) build -ldflags "-X main.LogPath=/tmp/emu-cni.log" -o $(BINARY_DIR)/emu-cni $(EMU_CNI_CMD)

debug-cni:
	@echo "Building debug-cni..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/debug-cni $(DEBUG_CNI_CMD)

emunet-gen:
	@echo "Building emunet-gen..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/emunet-gen $(EMUNET_GEN_CMD)

test: test-controller test-linkserver test-node-agent

test-controller:
	@echo "Running controller tests..."
	$(GO) test -v $(CONTROLLER_TEST)

test-linkserver:
	@echo "Running linkserver tests..."
	$(GO) test -v $(LINKSERVER_TEST)

test-node-agent:
	@echo "Running node-agent tests..."
	$(GO) test -v $(NODE_AGENT_TEST)

clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BINARY_DIR)

tidy:
	@echo "Tidying go modules..."
	$(GO) mod tidy

fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

lint:
	@echo "Running linter..."
	golangci-lint run

vet:
	@echo "Running go vet..."
	$(GO) vet ./...

generate: controller-gen
	@echo "Generating code..."
	$(CONTROLLER_GEN) object:headerFile="$(CONTROLLER_BOILERPLATE)" paths="$(CONTROLLER_PKG)"

manifests: controller-gen
	@echo "Generating CRD manifests..."
	$(CONTROLLER_GEN) crd paths="$(CONTROLLER_PKG)" output:crd:artifacts:config=$(DEPLOY_CRDS)

controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	@[ -f "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)" ] && [ "$$(readlink -- "$(CONTROLLER_GEN)" 2>/dev/null)" = "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)" ] || { \
		package=sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION) ;\
		echo "Downloading $${package}" ;\
		rm -f "$(CONTROLLER_GEN)" ;\
		GOBIN="$(LOCALBIN)" go install $${package} ;\
		mv "$(LOCALBIN)/controller-gen" "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)" ;\
	} ;\
	ln -sf "$$(realpath "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)")" "$(CONTROLLER_GEN)"

$(LOCALBIN):
	@mkdir -p $(LOCALBIN)

docker-build: docker-build-controller docker-build-linkserver docker-build-node-agent

docker-build-controller:
	@echo "Building controller Docker image..."
	docker build -t $(REGISTRY)/emunet-controller:$(VERSION) -f $(DEPLOY_CONTROLLER)/Dockerfile .

docker-build-linkserver:
	@echo "Building linkserver Docker image..."
	docker build -t $(REGISTRY)/emunet-linkserver:$(VERSION) -f $(DEPLOY_LINKSERVER)/Dockerfile .

docker-build-node-agent:
	@echo "Building node-agent Docker image..."
	docker build -t $(REGISTRY)/emunet-node-agent:$(VERSION) -f $(DEPLOY_NODE_AGENT)/Dockerfile .

docker-push: docker-push-controller docker-push-linkserver docker-push-node-agent

docker-push-controller:
	docker push $(REGISTRY)/emunet-controller:$(VERSION)

docker-push-linkserver:
	docker push $(REGISTRY)/emunet-linkserver:$(VERSION)

docker-push-node-agent:
	docker push $(REGISTRY)/emunet-node-agent:$(VERSION)

deploy: deploy-controller deploy-linkserver deploy-node-agent

deploy-controller:
	@echo "Deploying controller to Kubernetes..."
	kubectl apply -f $(DEPLOY_CRDS)/
	kubectl apply -f $(DEPLOY_CONTROLLER)/

deploy-linkserver:
	@echo "Deploying linkserver to Kubernetes..."
	kubectl apply -f $(DEPLOY_LINKSERVER)/

deploy-node-agent:
	@echo "Deploying node-agent to Kubernetes..."
	kubectl apply -f $(DEPLOY_NODE_AGENT)/

clean-registry:
	@echo "======================================"
	@echo "Clearing all images from registry"
	@echo "Registry container: private-registry"
	@echo "======================================"
	@if ! docker ps | grep -q "private-registry"; then \
		echo "Error: Registry container 'private-registry' is not running"; \
		exit 1; \
	fi
	@MOUNT_PATH=$$(docker inspect private-registry --format='{{range .Mounts}}{{if eq .Destination "/var/lib/registry"}}{{.Source}}{{end}}{{end}}'); \
	if [ -z "$$MOUNT_PATH" ]; then \
		echo "Error: Cannot find registry data mount path"; \
		exit 1; \
	fi; \
	echo "Registry data path: $$MOUNT_PATH"; \
	echo ""; \
	echo "Stopping registry container..."; \
	docker stop private-registry; \
	echo "Deleting registry data..."; \
	rm -rf "$$MOUNT_PATH/docker/registry/v2/repositories/emunet"; \
	echo "Starting registry container..."; \
	docker start private-registry; \
	echo ""; \
	echo "======================================"; \
	echo "Cleanup completed!"; \
	echo "======================================"

install-deps:
	@echo "Installing dependencies..."
	$(GO) mod download

update-deps:
	@echo "Updating dependencies..."
	$(GO) mod upgrade

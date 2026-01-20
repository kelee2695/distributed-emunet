# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'.
.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" crd paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

##@ Build

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.19.0

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	@[ -f "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)" ] && [ "$$(readlink -- "$(CONTROLLER_GEN)" 2>/dev/null)" = "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)" ] || { \
		package=sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION) ;\
		echo "Downloading $${package}" ;\
		rm -f "$(CONTROLLER_GEN)" ;\
		GOBIN="$(LOCALBIN)" go install $${package} ;\
		mv "$(LOCALBIN)/controller-gen" "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)" ;\
	} ;\
	ln -sf "$$(realpath "$(CONTROLLER_GEN)-$(CONTROLLER_TOOLS_VERSION)")" "$(CONTROLLER_GEN)"

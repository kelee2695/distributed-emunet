CNI_DIR := .
CNI_BIN_NAME := debug-cni
CNI_BIN := ./bin/$(CNI_BIN_NAME)

.PHONY: all
all: build

.PHONY: build
build:
	@echo "Building $(CNI_BIN_NAME)..."
	@mkdir -p bin
	go build -o $(CNI_BIN) $(CNI_DIR)/main.go
	@echo "Build complete: $(CNI_BIN)"

.PHONY: clean
clean:
	@echo "Cleaning..."
	rm -rf bin
	@echo "Clean complete"

.PHONY: run
run: build
	@echo "Running $(CNI_BIN_NAME)..."
	$(CNI_BIN)

.PHONY: install
install: build
	@echo "Installing $(CNI_BIN_NAME) to /opt/cni/bin/..."
	sudo mkdir -p /opt/cni/bin
	sudo cp $(CNI_BIN) /opt/cni/bin/$(CNI_BIN_NAME)
	@echo "Install complete: /opt/cni/bin/$(CNI_BIN_NAME)"

.PHONY: uninstall
uninstall:
	@echo "Uninstalling $(CNI_BIN_NAME) from /opt/cni/bin/..."
	sudo rm -f /opt/cni/bin/$(CNI_BIN_NAME)
	@echo "Uninstall complete"

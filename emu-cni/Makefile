CNI_DIR := ./cmd/emu-cni
CNI_BIN := ./bin/EMU_CNI
LOG_PATH ?= /home/node01/EMU_CNI/result.log

.PHONY: emu-cni
emu-cni:
# 	go generate ./tools/ebpf/ebpf-xdp/
	go generate ./tools/ebpf/ebpf-tc/	
	go build -ldflags "-X main.LogPath=$(LOG_PATH)" -o $(CNI_BIN) $(CNI_DIR)/main.go

# 定义清理规则
.PHONY: clean
clean:
	rm -f $(CNI_BIN)

.PHONY: run
run:
	$(CNI_BIN)

.PHONY: install
install:
	sudo cp $(CNI_BIN) /opt/cni/bin/

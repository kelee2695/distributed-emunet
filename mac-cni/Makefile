CNI_DIR := .
CNI_BIN := ./bin/MAC_CNI

# RemoteIP 对端物理IP地址，可通过环境变量 REMOTE_IP 设置
REMOTE_IP ?= 192.168.1.104

.PHONY: mac-cni
mac-cni:
	go build -ldflags "-X 'MAC_CNI/cmd.RemoteIP=$(REMOTE_IP)'" -o $(CNI_BIN) $(CNI_DIR)/main.go

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

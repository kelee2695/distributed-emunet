# Distributed EmuNet

一个在Kubernetes集群上实现Pod间网络仿真的分布式系统。通过eBPF技术实现高性能的网络链路参数控制，支持带宽限制、延迟、丢包率、抖动等网络仿真功能。

## 项目概述

EmuNet是一个专为Kubernetes环境设计的网络仿真平台，可以在Pod之间模拟各种网络条件，用于测试和验证分布式系统在网络异常情况下的表现。系统采用分布式架构，通过eBPF实现高性能的数据包处理，支持大规模Pod网络仿真。

## 核心特性

- **高性能网络仿真**：基于eBPF技术，在内核态实现网络参数控制，性能损耗极小
- **分布式架构**：支持大规模集群，每个节点独立处理本地Pod的网络仿真
- **灵活的参数配置**：支持带宽限制、延迟、丢包率、抖动等多种网络参数
- **Kubernetes原生集成**：通过CRD定义网络仿真资源，与K8s生态系统无缝集成
- **实时状态同步**：基于Redis的分布式状态管理，支持实时查询和监控
- **异步任务处理**：高并发任务队列，支持大规模链路参数配置

## 系统架构

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kubernetes Cluster                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────────┐         ┌──────────────────┐             │
│  │   Master Node    │         │   Worker Node    │             │
│  ├──────────────────┤         ├──────────────────┤             │
│  │                  │         │                  │             │
│  │  ┌────────────┐  │         │  ┌────────────┐  │             │
│  │  │ Controller │  │         │  │ Node Agent │  │             │
│  │  └────────────┘  │         │  └────────────┘  │             │
│  │         │         │         │         │         │             │
│  │  ┌────────────┐  │         │  ┌────────────┐  │             │
│  │  │ LinkServer │◄─┼─────────┼──┤            │  │             │
│  │  └────────────┘  │         │  └────────────┘  │             │
│  │         │         │         │         │         │             │
│  └─────────┼─────────┘         └─────────┼─────────┘             │
│            │                             │                       │
│            │         ┌─────────────────┐ │                       │
│            └────────►│      Redis      │◄┘                       │
│                      └─────────────────┘                         │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Worker Node Detail                     │   │
│  ├──────────────────────────────────────────────────────────┤   │
│  │                                                          │   │
│  │  ┌──────────┐         ┌──────────┐                     │   │
│  │  │   Pod    │◄────────┤ veth pair│                     │   │
│  │  └──────────┘         └──────────┘                     │   │
│  │         ▲                      │                         │   │
│  │         │                      ▼                         │   │
│  │  ┌──────────┐         ┌──────────┐                     │   │
│  │  │ kube-ovn │         │ emu-cni  │                     │   │
│  │  └──────────┘         └──────────┘                     │   │
│  │                              │                           │   │
│  │                              ▼                           │   │
│  │                      ┌──────────────┐                    │   │
│  │                      │   eBPF TC    │                    │   │
│  │                      └──────────────┘                    │   │
│  │                                                          │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## 组件说明

### Node端组件

#### 1. emu-cni（网络仿真CNI插件）
- **功能**：作为CNI插件链的一部分，在Pod创建时被调用
- **职责**：
  - 将eBPF程序附加到Pod的veth接口上
  - 收集Pod的网络信息（MAC地址、ifindex）
  - 将Pod信息上报给本地的node-agent
- **技术**：基于CNI标准接口，使用eBPF TC实现网络控制

#### 2. emunet-node-agent（节点代理）
- **功能**：在每个节点上运行的守护进程
- **职责**：
  - 提供HTTP API接口（默认端口12345）
  - 维护本地Pod信息存储
  - 接收来自emu-cni的Pod信息注册请求
  - 接收来自master linkserver的链路参数配置请求
  - 操作eBPF map来应用网络仿真参数
  - 将Pod信息写入Redis
- **技术**：Go HTTP服务器，sync.Map实现高性能并发存储

#### 3. debug-cni（调试CNI插件）
- **功能**：用于调试目的的简单CNI插件
- **用途**：开发测试阶段使用

### Master端组件

#### 1. controller（Kubernetes控制器）
- **功能**：实现自定义CRD：EmuNet
- **职责**：
  - 监控EmuNet资源的变化
  - 负责创建和管理Pod
  - 从Redis读取Agent上报的Pod网络信息
  - 更新Kubernetes EmuNet资源状态
  - 将完整Pod信息（IP+MAC）写入Redis缓存
- **技术**：Kubebuilder框架，Controller-Runtime

#### 2. linkserver（REST API服务器）
- **功能**：提供外部API接口用于配置网络仿真参数
- **职责**：
  - 提供RESTful API接口
  - 从Redis查询Pod网络信息
  - 通过异步任务队列处理链路参数配置
  - 向node-agent发送链路参数配置请求
- **技术**：Gorilla Mux路由器，异步Worker池

### 数据存储

#### Redis
- **功能**：中央数据存储和缓存
- **存储内容**：
  - Pod网络信息（MAC地址、ifindex、IP等）
  - EmuNet资源状态
  - Pod索引信息
- **数据结构**：
  - `agent:network:{podName}` - Agent上报的Pod网络信息
  - `pod_lookup:{podName}` - 合并后的完整Pod信息
  - `emunet:{namespace}:{name}` - EmuNet资源状态
  - `emunet:{namespace}:{name}:pods` - Pod名称索引集合

## 工作流程

### 1. Pod创建流程

```
Kubelet
  │
  ├─► kube-ovn CNI (配置基础网络)
  │
  └─► emu-cni CNI
        │
        ├─► 附加eBPF程序到veth接口
        ├─► 收集Pod网络信息（MAC、ifindex）
        └─► 上报信息到本地node-agent
              │
              └─► 写入Redis (agent:network:{podName})
```

### 2. 状态同步流程

```
Controller
  │
  ├─► 监控EmuNet CRD资源
  ├─► 创建/更新Pod
  │
  ├─► 从Redis读取Agent上报的Pod网络信息
  │   └─► GetAgentNetworkInfo(podName)
  │
  ├─► 更新Kubernetes EmuNet资源状态
  │
  └─► 写入Redis缓存
        ├─► emunet:{namespace}:{name}
        └─► pod_lookup:{podName} (完整信息)
```

### 3. 链路参数配置流程

```
用户请求
  │
  └─► LinkServer API (POST /api/v1/ebpf/entry/by-pods)
        │
        ├─► 从Redis查询Pod信息
        │   └─► GetPodInfoDirectly(pod1, pod2)
        │
        ├─► 构造链路参数配置任务
        │
        ├─► 加入异步任务队列
        │
        └─► Worker处理任务
              │
              ├─► 向目标节点node-agent发送HTTP请求
              │   └─► POST http://{nodeIP}:12345/api/ebpf/entry
              │
              └─► Node-agent更新eBPF map
                    └─► 应用网络仿真参数
```

## 部署前要求

### 环境要求
- Kubernetes集群（v1.20+）
- 已部署kube-ovn CNI插件
- Redis服务（v5.0+）
- 节点支持eBPF（Linux内核4.10+）
- Go 1.19+
- Docker/Containerd

### 权限要求
- 集群管理员权限（用于部署CRD和RBAC）
- 节点root权限（用于安装CNI插件和运行eBPF程序）

## 快速开始

### 1. 构建镜像

```bash
# 构建Node端镜像
cd node/deamonset-deploy
docker build -t emunet-node:v1.0 .

# 构建Controller镜像
cd ../master/controller
make docker-build IMG=emunet-controller:v1.0

# 构建LinkServer镜像
cd ../linkserver
make docker-build IMG=emunet-linkserver:v1.0
```

### 2. 部署Redis

```bash
# 部署Redis（如果还没有）
kubectl apply -f redis-deployment.yaml
```

### 3. 部署Node端组件

```bash
# 部署DaemonSet（在每个节点上运行）
kubectl apply -f node/deamonset-deploy/emunet-ebpf-agent.yaml
```

### 4. 部署Master端组件

```bash
# 部署Controller
kubectl apply -f master/master-deploy/controller/emunet-controller.yaml

# 部署LinkServer
kubectl apply -f master/master-deploy/linkserver/emunet-linkserver.yaml
```

### 5. 部署CRD

```bash
# 部署EmuNet CRD
kubectl apply -f master/controller/config/crd/bases/emunet.emunet.io_emunets.yaml
```

### 6. 创建EmuNet资源

```bash
# 创建示例EmuNet资源
kubectl apply -f master/controller/config/samples/emunet_v1_emunet.yaml
```

## 使用示例

### 1. 创建EmuNet资源

```yaml
apiVersion: emunet.emunet.io/v1
kind: EmuNet
metadata:
  name: emunet-example
  namespace: default
spec:
  totalReplicas: 20
  selector:
    matchLabels:
      app: emunet-pod
  imageGroups:
    - image: nginx:alpine
      replicas: 18
    - image: k8s.gcr.io/pause:3.2
      replicas: 2
```

### 2. 配置链路参数

```bash
# 配置两个Pod之间的网络仿真参数
curl -X POST http://<linkserver-ip>:30082/api/v1/ebpf/entry/by-pods \
  -H "Content-Type: application/json" \
  -d '{
    "pod1": "emunet-example-group0-0",
    "pod2": "emunet-example-group0-1",
    "throttleRateBps": 1000000,
    "delay": 100000,
    "lossRate": 10,
    "jitter": 50000
  }'
```

### 3. 查询Pod状态

```bash
# 查询EmuNet资源状态
kubectl get emunet emunet-example -o yaml

# 查询Pod列表
curl http://<linkserver-ip>:30082/api/v1/emunets/default/emunet-example/pods
```

### 4. 删除链路参数

```bash
# 删除两个Pod之间的网络仿真参数
curl -X DELETE http://<linkserver-ip>:30082/api/v1/ebpf/entry/by-pods \
  -H "Content-Type: application/json" \
  -d '{
    "pod1": "emunet-example-group0-0",
    "pod2": "emunet-example-group0-1"
  }'
```

## API文档

### LinkServer API

#### 健康检查
```
GET /api/v1/health
```

#### 创建链路参数
```
POST /api/v1/ebpf/entry/by-pods
Content-Type: application/json

{
  "pod1": "pod-name-1",
  "pod2": "pod-name-2",
  "throttleRateBps": 1000000,  // 带宽限制（字节/秒）
  "delay": 100000,              // 延迟（微秒）
  "lossRate": 10,               // 丢包率（千分比）
  "jitter": 50000               // 抖动（微秒）
}
```

#### 删除链路参数
```
DELETE /api/v1/ebpf/entry/by-pods
Content-Type: application/json

{
  "pod1": "pod-name-1",
  "pod2": "pod-name-2"
}
```

#### 查询Pod列表
```
GET /api/v1/emunets/{namespace}/{name}/pods
```

### Node Agent API

#### 添加Pod信息
```
POST /api/podinfo/add
Content-Type: application/json

{
  "podName": "pod-name",
  "ifindex": 123,
  "srcMac": "aa:bb:cc:dd:ee:ff"
}
```

#### 删除Pod信息
```
DELETE /api/podinfo/{podName}
```

#### 配置eBPF条目
```
POST /api/ebpf/entry
Content-Type: application/json

{
  "ifindex": 123,
  "srcMac": "aa:bb:cc:dd:ee:ff",
  "throttleRateBps": 1000000,
  "delay": 100000,
  "lossRate": 10,
  "jitter": 50000
}
```

#### 删除eBPF条目
```
DELETE /api/ebpf/entry
Content-Type: application/json

{
  "ifindex": 123,
  "srcMac": "aa:bb:cc:dd:ee:ff"
}
```

## 配置说明

### Controller配置

```yaml
args:
  - --leader-elect
  - --health-probe-bind-address=:8081
  - --metrics-bind-address=:8443
  - --redis-addr=<redis-address>:6379
```

### LinkServer配置

```yaml
args:
  - --api-bind-address=:8082
  - --redis-addr=<redis-address>:6379
```

### Node Agent配置

```yaml
args:
  - -redis-addr=<redis-address>:6379
```

## 性能优化

### 1. eBPF优化
- 使用eBPF map进行快速查找
- 在内核态处理数据包，减少用户态/内核态切换
- 使用TC（Traffic Control）进行流量控制

### 2. Redis优化
- 使用Pipeline批量操作
- 合理设置TTL防止内存泄漏
- 使用索引集合加速查询

### 3. 异步处理
- LinkServer使用异步任务队列
- 1000个并发Worker处理配置请求
- 任务队列缓冲区大小：1,000,000

### 4. 连接池优化
- HTTP客户端连接池复用
- Redis连接池优化
- 合理设置超时时间

## 故障排查

### 1. Pod无法启动
```bash
# 检查CNI插件是否正确安装
ls -la /opt/cni/bin/emu-cni

# 检查CNI配置文件
cat /etc/cni/net.d/01-kube-ovn.conflist

# 查看kubelet日志
journalctl -u kubelet -f
```

### 2. Node Agent无法连接Redis
```bash
# 检查Node Agent日志
kubectl logs -n kube-system -l name=emunet-ebpf-agent -c node-agent

# 检查Redis连接
kubectl exec -it <redis-pod> -- redis-cli ping
```

### 3. 链路参数配置失败
```bash
# 检查LinkServer日志
kubectl logs -n kube-system -l app=emunet-linkserver

# 检查Node Agent日志
kubectl logs -n kube-system -l name=emunet-ebpf-agent -c node-agent

# 检查eBPF map
bpftool map show
```

### 4. 网络仿真不生效
```bash
# 检查eBPF程序是否附加
tc qdisc show dev <veth-interface>

# 检查eBPF map内容
bpftool map dump name MAC_HANDLE_EMU

# 检查网络接口
ip link show
```

## 开发指南

### 本地开发

```bash
# 构建emu-cni
cd node/emu-cni
LOG_PATH=/tmp/emu-cni.log make emu-cni

# 运行node-agent
cd node/emunet-node-agent
make run

# 运行controller
cd master/controller
make run

# 运行linkserver
cd master/linkserver
make run
```

### 代码结构

```
distributed-emunet/
├── node/                    # Node端组件
│   ├── emu-cni/            # 网络仿真CNI插件
│   ├── emunet-node-agent/  # 节点代理
│   ├── debug-cni/          # 调试CNI插件
│   └── deamonset-deploy/   # DaemonSet部署文件
├── master/                 # Master端组件
│   ├── controller/         # Kubernetes控制器
│   ├── linkserver/         # REST API服务器
│   └── master-deploy/      # Master部署文件
├── config/                 # 配置文件
└── images/                 # 镜像构建文件
```

## 贡献指南

欢迎提交Issue和Pull Request！

## 许可证

Apache License 2.0

## 联系方式

如有问题，请提交Issue或联系维护者。

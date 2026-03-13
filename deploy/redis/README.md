# EmuNet Redis 部署指南

## 概述

本目录包含将 Redis 部署到 Kubernetes 集群中的配置文件。Redis 作为 EmuNet 系统的核心组件，用于：
- 存储 Pod 网络信息
- 实现分布式状态同步
- 提供控制总线（Control Bus）消息传递

## 部署文件

### 1. emunet-redis-deployment.yaml
Redis Deployment 配置，包含：
- 使用私有镜像仓库：`100.75.179.29:5000/redis:latest`
- 部署在 kube-system 命名空间
- 调度到 Master 节点
- 配置资源限制和健康检查
- 使用 emptyDir 进行数据持久化

### 2. emunet-redis-service.yaml
Redis Service 配置，包含：
- ClusterIP 类型服务
- 暴露 6379 端口
- 通过 `emunet-redis.kube-system.svc.cluster.local:6379` 访问

## 部署步骤

### 1. 部署 Redis
```bash
# 部署 Redis Deployment
kubectl apply -f deploy/redis/emunet-redis-deployment.yaml

# 部署 Redis Service
kubectl apply -f deploy/redis/emunet-redis-service.yaml
```

### 2. 验证 Redis 部署
```bash
# 检查 Pod 状态
kubectl get pods -n kube-system -l app=emunet-redis

# 检查 Service 状态
kubectl get svc -n kube-system emunet-redis

# 查看 Redis 日志
kubectl logs -n kube-system -l app=emunet-redis
```

### 3. 测试 Redis 连接
```bash
# 进入 Redis Pod 测试
kubectl exec -n kube-system -it $(kubectl get pods -n kube-system -l app=emunet-redis -o jsonpath='{.items[0].metadata.name}') -- redis-cli ping

# 应该返回: PONG
```

### 4. 部署其他 EmuNet 组件
Redis 部署成功后，可以部署其他组件：

```bash
# 部署 Controller
kubectl apply -f deploy/controller/emunet-controller.yaml

# 部署 LinkServer
kubectl apply -f deploy/linkserver/emunet-linkserver.yaml

# 部署 Node Agent
kubectl apply -f deploy/emunet-node-agent/emunet-ebpf-agent.yaml
```

## 配置说明

### Redis 连接地址
所有组件已更新为使用 Kubernetes 内部服务地址：
- **Service 名称**: `emunet-redis`
- **命名空间**: `kube-system`
- **完整地址**: `emunet-redis.kube-system:6379`

### 资源配置
- **CPU 限制**: 500m
- **内存限制**: 512Mi
- **CPU 请求**: 100m
- **内存请求**: 128Mi

### 健康检查
- **Liveness Probe**: 每 10 秒检查一次，初始延迟 30 秒
- **Readiness Probe**: 每 5 秒检查一次，初始延迟 5 秒

## 注意事项

1. **数据持久化**: 当前使用 emptyDir，Pod 重启后数据会丢失。如需持久化，建议使用 PVC

2. **高可用**: 当前为单副本部署。生产环境建议使用 Redis Sentinel 或 Redis Cluster

3. **安全性**: 当前未配置密码和 TLS。生产环境建议启用认证

4. **监控**: 建议配置 Redis Exporter 进行监控

## 升级说明

如果从外部 Redis 迁移到集群内 Redis：

1. 先部署新的 Redis
2. 验证新 Redis 可用
3. 逐个重启其他组件（Controller、LinkServer、Node Agent）
4. 确认所有组件正常连接新 Redis
5. 停止外部 Redis 服务

## 故障排查

### Redis Pod 无法启动
```bash
# 查看 Pod 状态
kubectl describe pod -n kube-system -l app=emunet-redis

# 查看日志
kubectl logs -n kube-system -l app=emunet-redis
```

### 组件无法连接 Redis
```bash
# 检查 Service
kubectl get svc -n kube-system emunet-redis

# 检查 Endpoint
kubectl get endpoints -n kube-system emunet-redis

# 测试连接
kubectl run -it --rm debug --image=redis:7-alpine --restart=Never -- redis-cli -h emunet-redis.kube-system ping
```

## 扩展配置

### 使用 PVC 持久化数据
如果需要数据持久化，可以修改 Deployment 添加 PVC：

```yaml
volumes:
  - name: redis-data
    persistentVolumeClaim:
      claimName: emunet-redis-pvc
```

### 配置 Redis 密码
如需启用密码认证，需要：
1. 创建 Secret 存储密码
2. 在 Deployment 中挂载 Secret
3. 修改所有组件的连接参数添加密码

### 配置 Redis Sentinel
生产环境建议使用 Redis Sentinel 实现高可用：
- 部署 3 个 Redis 实例
- 部署 3 个 Sentinel 实例
- 修改组件连接地址为 Sentinel 地址

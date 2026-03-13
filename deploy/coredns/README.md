# CoreDNS 部署指南

## 概述

本目录包含 CoreDNS 的 Kubernetes 部署配置文件。CoreDNS 是 Kubernetes 集群的 DNS 服务，负责：
- 服务发现：将服务名解析为 ClusterIP
- 反向解析：将 IP 解析为服务名
- 健康检查和监控

## 部署文件

### 1. coredns-configmap.yaml
CoreDNS 配置文件，包含：
- DNS 解析规则
- Kubernetes 集群内服务发现
- 缓存和负载均衡配置

### 2. coredns-deployment.yaml
CoreDNS Deployment 配置，包含：
- 使用私有镜像仓库：`100.75.179.29:5000/coredns:1.10.1`
- 2 个副本，高可用
- 资源限制和健康检查

### 3. coredns-service.yaml
CoreDNS Service 配置，包含：
- 暴露 53 端口（UDP/TCP）
- 暴露 9153 端口（监控指标）

### 4. coredns-rbac.yaml
RBAC 权限配置，包含：
- ServiceAccount
- ClusterRole
- ClusterRoleBinding

## 部署步骤

### 方法 1：使用 Makefile（推荐）

```bash
# 部署 CoreDNS
make deploy-coredns

# 验证部署
make verify-coredns
```

### 方法 2：手动部署

```bash
# 1. 部署 RBAC
kubectl apply -f deploy/coredns/coredns-rbac.yaml

# 2. 部署 ConfigMap
kubectl apply -f deploy/coredns/coredns-configmap.yaml

# 3. 部署 Deployment
kubectl apply -f deploy/coredns/coredns-deployment.yaml

# 4. 部署 Service
kubectl apply -f deploy/coredns/coredns-service.yaml
```

## 验证部署

### 1. 检查 Pod 状态
```bash
kubectl get pods -n kube-system -l k8s-app=kube-dns
```

预期输出：
```
NAME                       READY   STATUS    RESTARTS   AGE
coredns-5c6b6d6d7b-abc123   1/1     Running   0          1m
coredns-5c6b6d6d7b-def456   1/1     Running   0          1m
```

### 2. 检查 Service 状态
```bash
kubectl get svc -n kube-system kube-dns
```

预期输出：
```
NAME       TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)                  AGE
kube-dns   ClusterIP   10.0.0.10    <none>        53/UDP,53/TCP,9153/TCP   1m
```

### 3. 测试 DNS 解析
```bash
# 创建测试 Pod
kubectl run -it --rm debug --image=busybox --restart=Never -- nslookup emunet-redis.kube-system

# 或者使用 dig
kubectl run -it --rm debug --image=nicolaka/netshoot --restart=Never -- dig emunet-redis.kube-system
```

预期输出：
```
Server:    10.0.0.10
Address 1: 10.0.0.10

Name:      emunet-redis.kube-system
Address 1: 10.0.0.173
```

### 4. 测试服务名解析
```bash
# 测试 Redis 服务
kubectl run -it --rm debug --image=busybox --restart=Never -- nslookup emunet-redis.kube-system.svc.cluster.local

# 测试 LinkServer 服务
kubectl run -it --rm debug --image=busybox --restart=Never -- nslookup emunet-linkserver-svc.kube-system
```

## 配置说明

### DNS 域名格式

Kubernetes 中的服务可以通过以下格式访问：

1. **短格式（同一命名空间）**
   ```
   emunet-redis
   ```

2. **标准格式（跨命名空间）**
   ```
   emunet-redis.kube-system
   ```

3. **完整格式**
   ```
   emunet-redis.kube-system.svc.cluster.local
   ```

### CoreDNS 配置说明

```yaml
.:53 {
    errors                    # 错误日志
    health { lameduck 5s }   # 健康检查
    ready                     # 就绪检查
    kubernetes cluster.local {   # Kubernetes 服务发现
        pods insecure
        fallthrough in-addr.arpa ip6.arpa
        ttl 30
    }
    prometheus :9153          # Prometheus 监控
    forward . /etc/resolv.conf { # 转发到上游 DNS
        max_concurrent 1000
    }
    cache 30                 # 缓存 30 秒
    loop                      # 检测循环
    reload                    # 自动重载配置
    loadbalance               # 负载均衡
}
```

## 镜像信息

- **镜像名称**: `100.75.179.29:5000/coredns:1.10.1`
- **镜像大小**: 约 50MB
- **资源限制**:
  - CPU: 170m
  - 内存: 170Mi
- **资源请求**:
  - CPU: 100m
  - 内存: 70Mi

## 故障排查

### Pod 无法启动
```bash
# 查看 Pod 状态
kubectl describe pod -n kube-system -l k8s-app=kube-dns

# 查看日志
kubectl logs -n kube-system -l k8s-app=kube-dns
```

### DNS 解析失败
```bash
# 检查 Service 是否正常
kubectl get svc -n kube-system kube-dns
kubectl get endpoints -n kube-system kube-dns

# 检查 Pod 是否就绪
kubectl get pods -n kube-system -l k8s-app=kube-dns

# 测试 DNS 连接
kubectl run -it --rm debug --image=busybox --restart=Never -- nslookup kubernetes.default
```

### 查看详细日志
```bash
# 查看 CoreDNS 日志
kubectl logs -n kube-system -l k8s-app=kube-dns --tail=100

# 实时查看日志
kubectl logs -n kube-system -l k8s-app=kube-dns -f
```

## 卸载

```bash
# 使用 Makefile
make undeploy-coredns

# 或手动删除
kubectl delete -f deploy/coredns/coredns-rbac.yaml
kubectl delete -f deploy/coredns/coredns-configmap.yaml
kubectl delete -f deploy/coredns/coredns-deployment.yaml
kubectl delete -f deploy/coredns/coredns-service.yaml
```

## 注意事项

1. **部署顺序**: 必须先部署 RBAC，再部署其他资源
2. **资源限制**: 根据集群规模调整资源限制
3. **副本数**: 生产环境建议至少 2 个副本
4. **监控**: 建议配置 Prometheus 监控 CoreDNS 指标
5. **上游 DNS**: 确保 `/etc/resolv.conf` 配置了正确的上游 DNS

## 相关资源

- [CoreDNS 官方文档](https://coredns.io/manual/toc/)
- [Kubernetes DNS 文档](https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/)
- [CoreDNS GitHub](https://github.com/coredns/coredns)

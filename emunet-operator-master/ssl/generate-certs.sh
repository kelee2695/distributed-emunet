#!/bin/bash

set -e

SSL_DIR="/home/lrh/emunet-operator/ssl"
K8S_SSL_DIR="/etc/kubernetes/ssl"

mkdir -p "$SSL_DIR"

echo "=== 复制CA证书 ==="
cp "$K8S_SSL_DIR/ca.pem" "$SSL_DIR/ca.crt"
cp "$K8S_SSL_DIR/ca-key.pem" "$SSL_DIR/ca.key"

echo "=== 为emunet-operator创建私钥 ==="
openssl genrsa -out "$SSL_DIR/emunet-operator.key" 2048

echo "=== 为emunet-operator创建证书签名请求(CSR) ==="
cat > "$SSL_DIR/emunet-operator-csr.conf" <<EOF
[req]
default_bits = 2048
prompt = no
default_md = sha256
distinguished_name = dn
req_extensions = v3_req

[dn]
C = CN
ST = Beijing
L = Beijing
O = emunet-operator
OU = Kubernetes
CN = emunet-operator

[v3_req]
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth, clientAuth
basicConstraints = critical, CA:FALSE
subjectAltName = @alt_names

[alt_names]
DNS.1 = emunet-operator
DNS.2 = emunet-operator.default
DNS.3 = emunet-operator.default.svc
DNS.4 = emunet-operator.default.svc.cluster.local
DNS.5 = localhost
IP.1 = 127.0.0.1
EOF

openssl req -new -key "$SSL_DIR/emunet-operator.key" \
  -out "$SSL_DIR/emunet-operator.csr" \
  -config "$SSL_DIR/emunet-operator-csr.conf"

echo "=== 使用CA签名emunet-operator证书（10年有效期） ==="
openssl x509 -req -in "$SSL_DIR/emunet-operator.csr" \
  -CA "$SSL_DIR/ca.crt" \
  -CAkey "$SSL_DIR/ca.key" \
  -CAcreateserial \
  -out "$SSL_DIR/emunet-operator.crt" \
  -days 3650 \
  -extensions v3_req \
  -extfile "$SSL_DIR/emunet-operator-csr.conf"

echo "=== 创建kubeconfig文件 ==="
cat > "$SSL_DIR/emunet-operator.kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: $(cat "$SSL_DIR/ca.crt" | base64 -w 0)
    server: https://192.168.1.101:6443
  name: kubernetes
contexts:
- context:
    cluster: kubernetes
    user: emunet-operator
  name: emunet-operator-context
current-context: emunet-operator-context
users:
- name: emunet-operator
  user:
    client-certificate-data: $(cat "$SSL_DIR/emunet-operator.crt" | base64 -w 0)
    client-key-data: $(cat "$SSL_DIR/emunet-operator.key" | base64 -w 0)
EOF

echo "=== 创建RBAC配置（最小必要权限） ==="
cat > "$SSL_DIR/emunet-operator-rbac.yaml" <<'EOF'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: emunet-operator
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: emunet-operator-role
rules:
- apiGroups:
  - emunet.emunet.io
  resources:
  - emunets
  - emunets/status
  - emunets/finalizers
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
  - delete
- apiGroups:
  - ""
  resources:
  - pods
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
  - delete
- apiGroups:
  - ""
  resources:
  - events
  verbs:
  - create
  - patch
- apiGroups:
  - coordination.k8s.io
  resources:
  - leases
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
  - delete
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: emunet-operator-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: emunet-operator-role
subjects:
- kind: User
  name: emunet-operator
  apiGroup: rbac.authorization.k8s.io
EOF

echo "=== 清理临时文件 ==="
rm -f "$SSL_DIR/emunet-operator.csr" "$SSL_DIR/emunet-operator-csr.conf"

echo "=== 验证证书 ==="
echo "证书主题信息:"
openssl x509 -in "$SSL_DIR/emunet-operator.crt" -text -noout | grep -A5 "Subject:"
echo ""
echo "证书有效期:"
openssl x509 -in "$SSL_DIR/emunet-operator.crt" -text -noout | grep -A2 "Validity"

echo ""
echo "=== 文件生成完成 ==="
echo ""
echo "生成的文件:"
echo "  $SSL_DIR/ca.crt                      - CA证书"
echo "  $SSL_DIR/ca.key                      - CA私钥"
echo "  $SSL_DIR/emunet-operator.crt         - Operator证书（O=emunet-operator）"
echo "  $SSL_DIR/emunet-operator.key         - Operator私钥"
echo "  $SSL_DIR/emunet-operator.kubeconfig  - Kubeconfig文件"
echo "  $SSL_DIR/emunet-operator-rbac.yaml   - RBAC配置"
echo ""
echo "使用说明:"
echo "  1. 应用RBAC配置:"
echo "     kubectl apply -f $SSL_DIR/emunet-operator-rbac.yaml"
echo ""
echo "  2. 使用kubeconfig:"
echo "     export KUBECONFIG=$SSL_DIR/emunet-operator.kubeconfig"
echo "     kubectl get pods --all-namespaces"
echo ""
echo "权限说明:"
echo "  - 证书O字段: emunet-operator（自定义组）"
echo "  - EmuNet资源权限: 完全CRUD权限"
echo "  - Pod资源权限: 完全CRUD权限"
echo "  - Event资源权限: 创建和更新权限"
echo "  - Lease资源权限: 用于leader election"
echo "  - 权限范围: 所有命名空间"
echo "  - 证书有效期: 10年"

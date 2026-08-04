#!/bin/bash
set -e

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "========================================="
echo "  EmuNet Deployment Installer"
echo "========================================="

echo ""
echo "[1/6] Installing CRDs..."
kubectl apply -f "${DEPLOY_DIR}/crds/"

echo ""
echo "[2/6] Installing CoreDNS RBAC..."
kubectl apply -f "${DEPLOY_DIR}/coredns/coredns-rbac.yaml"

echo ""
echo "[3/6] Installing CoreDNS ConfigMap..."
kubectl apply -f "${DEPLOY_DIR}/coredns/coredns-configmap.yaml"

echo ""
echo "[4/6] Installing Redis..."
kubectl apply -f "${DEPLOY_DIR}/redis/"

echo ""
echo "[5/6] Installing LinkServer..."
kubectl apply -f "${DEPLOY_DIR}/linkserver/"

echo ""
echo "[6/6] Installing Controller..."
kubectl apply -f "${DEPLOY_DIR}/controller/"

echo ""
echo "[7/6] Installing eBPF Agent..."
kubectl apply -f "${DEPLOY_DIR}/emunet-node-agent/emunet-ebpf-agent.yaml"

echo ""
echo "[8/6] Installing CoreDNS Service and Deployment..."
kubectl apply -f "${DEPLOY_DIR}/coredns/coredns-service.yaml"
kubectl apply -f "${DEPLOY_DIR}/coredns/coredns-deployment.yaml"

echo ""
echo "========================================="
echo "  Installation completed successfully!"
echo "========================================="
echo ""
echo "Verify deployment:"
echo "  kubectl get pods -n kube-system"
echo "  kubectl get emunet -A"

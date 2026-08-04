#!/bin/bash
set -e

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "========================================="
echo "  EmuNet Deployment Cleaner"
echo "========================================="

echo ""
echo "[1/8] Deleting eBPF Agent..."
kubectl delete -f "${DEPLOY_DIR}/emunet-node-agent/emunet-ebpf-agent.yaml" --ignore-not-found

echo ""
echo "[2/8] Deleting Controller..."
kubectl delete -f "${DEPLOY_DIR}/controller/" --ignore-not-found

echo ""
echo "[3/8] Deleting LinkServer..."
kubectl delete -f "${DEPLOY_DIR}/linkserver/" --ignore-not-found

echo ""
echo "[4/8] Deleting Redis..."
kubectl delete -f "${DEPLOY_DIR}/redis/" --ignore-not-found

echo ""
echo "[5/8] Deleting CoreDNS Deployment..."
kubectl delete -f "${DEPLOY_DIR}/coredns/coredns-deployment.yaml" --ignore-not-found

echo ""
echo "[6/8] Deleting CoreDNS Service..."
kubectl delete -f "${DEPLOY_DIR}/coredns/coredns-service.yaml" --ignore-not-found

echo ""
echo "[7/8] Deleting CoreDNS ConfigMap..."
kubectl delete -f "${DEPLOY_DIR}/coredns/coredns-configmap.yaml" --ignore-not-found

echo ""
echo "[8/8] Deleting CoreDNS RBAC..."
kubectl delete -f "${DEPLOY_DIR}/coredns/coredns-rbac.yaml" --ignore-not-found

echo ""
echo "[9/8] Deleting CRDs..."
kubectl delete -f "${DEPLOY_DIR}/crds/" --ignore-not-found

echo ""
echo "========================================="
echo "  Cleanup completed successfully!"
echo "========================================="
echo ""
echo "Verify cleanup:"
echo "  kubectl get pods -n kube-system"
echo "  kubectl get emunet -A"

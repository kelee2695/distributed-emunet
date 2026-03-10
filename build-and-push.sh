#!/bin/bash

set -e

REGISTRY="100.75.179.29:5000"
VERSION="${VERSION:-latest}"
PROJECT_NAME="emunet"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1" >&2
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1" >&2
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

check_docker() {
    if ! docker info > /dev/null 2>&1; then
        log_error "Docker daemon is not running"
        exit 1
    fi
    log_info "Docker is running"
}

check_registry() {
    log_info "Checking registry accessibility..."
    if ! curl -s "http://${REGISTRY}/v2/" > /dev/null 2>&1; then
        log_warn "Registry ${REGISTRY} may not be accessible"
    else
        log_info "Registry ${REGISTRY} is accessible"
    fi
}

build_image() {
    local name=$1
    local dockerfile_path=$2
    local context_path=$3

    log_info "Building image: ${name}"
    
    local image_name="${REGISTRY}/${PROJECT_NAME}/${name}:${VERSION}"
    
    docker build -t "${image_name}" \
        -f "${dockerfile_path}" \
        "${context_path}" > /dev/null
    
    log_info "Successfully built: ${image_name}"
    echo "${image_name}"
}

push_image() {
    local image_name=$1
    
    log_info "Pushing image: ${image_name}"
    docker push "${image_name}" > /dev/null
    log_info "Successfully pushed: ${image_name}"
}

build_and_push() {
    local name=$1
    local dockerfile_path=$2
    local context_path=$3
    
    local image_name=$(build_image "${name}" "${dockerfile_path}" "${context_path}")
    push_image "${image_name}"
}

main() {
    log_info "Starting Docker image build and push process"
    log_info "Registry: ${REGISTRY}"
    log_info "Version: ${VERSION}"
    echo ""

    check_docker
    check_registry
    echo ""

    PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

    log_info "Project root: ${PROJECT_ROOT}"
    echo ""

    log_info "========================================"
    log_info "Building LinkServer"
    log_info "========================================"
    build_and_push \
        "linkserver" \
        "${PROJECT_ROOT}/master/master-deploy/linkserver/Dockerfile" \
        "${PROJECT_ROOT}/master/linkserver"
    echo ""

    log_info "========================================"
    log_info "Building Controller"
    log_info "========================================"
    build_and_push \
        "controller" \
        "${PROJECT_ROOT}/master/master-deploy/controller/Dockerfile" \
        "${PROJECT_ROOT}/master/controller"
    echo ""

    log_info "========================================"
    log_info "Building Node Agent"
    log_info "========================================"
    build_and_push \
        "node-agent" \
        "${PROJECT_ROOT}/node/deamonset-deploy/Dockerfile" \
        "${PROJECT_ROOT}/node"
    echo ""

    log_info "========================================"
    log_info "All images built and pushed successfully!"
    log_info "========================================"
    echo ""
    log_info "Images pushed to ${REGISTRY}:"
    echo "  - ${REGISTRY}/${PROJECT_NAME}/linkserver:${VERSION}"
    echo "  - ${REGISTRY}/${PROJECT_NAME}/controller:${VERSION}"
    echo "  - ${REGISTRY}/${PROJECT_NAME}/node-agent:${VERSION}"
}

main "$@"

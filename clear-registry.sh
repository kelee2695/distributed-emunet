#!/bin/bash

REGISTRY_CONTAINER="private-registry"
REGISTRY_DATA_DIR="/var/lib/registry"

echo "======================================"
echo "Clearing all images from registry"
echo "Registry container: $REGISTRY_CONTAINER"
echo "======================================"

# 检查容器是否运行
if ! docker ps | grep -q "$REGISTRY_CONTAINER"; then
    echo "Error: Registry container '$REGISTRY_CONTAINER' is not running"
    exit 1
fi

# 获取registry的数据卷挂载路径
MOUNT_PATH=$(docker inspect "$REGISTRY_CONTAINER" --format='{{range .Mounts}}{{if eq .Destination "/var/lib/registry"}}{{.Source}}{{end}}{{end}}')

if [ -z "$MOUNT_PATH" ]; then
    echo "Error: Cannot find registry data mount path"
    exit 1
fi

echo "Registry data path: $MOUNT_PATH"
echo ""

# 确认删除
read -p "Are you sure you want to delete ALL images? (yes/no): " CONFIRM
if [ "$CONFIRM" != "yes" ]; then
    echo "Operation cancelled"
    exit 0
fi

echo ""
echo "======================================"
echo "Deleting images..."
echo "======================================"

# 停止registry容器
echo "Stopping registry container..."
docker stop "$REGISTRY_CONTAINER"

# 删除registry数据
echo "Deleting registry data..."
rm -rf "$MOUNT_PATH/docker/registry/v2/repositories/emunet"

# 重新启动registry容器
echo "Starting registry container..."
docker start "$REGISTRY_CONTAINER"

echo ""
echo "======================================"
echo "Cleanup completed!"
echo "======================================"

# 验证
echo ""
echo "Verifying cleanup..."
sleep 2

# 检查是否还有emunet镜像
if docker exec "$REGISTRY_CONTAINER" ls /var/lib/registry/docker/registry/v2/repositories/emunet 2>/dev/null; then
    echo "⚠  Some images may still exist"
else
    echo "✓ All images cleared successfully!"
fi

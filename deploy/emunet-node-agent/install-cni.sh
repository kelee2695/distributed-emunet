#!/bin/sh
set -e

# 1. 物理拷贝可执行文件（总是执行）
echo "Starting CNI binary installation..."
echo "Checking if /opt/cni/bin directory exists..."
ls -la /opt/cni/bin/ || echo "Directory does not exist yet"

echo "Copying emu-cni binary..."
cp /emu-cni /opt/cni/bin/emu-cni
chmod +x /opt/cni/bin/emu-cni

echo "Verifying binary was copied..."
ls -la /opt/cni/bin/emu-cni

echo "emu-cni binary copied successfully"

# 2. 更新 CNI 配置文件
CONF="/etc/cni/net.d/01-kube-ovn.conflist"
echo "Checking CNI config file: $CONF"
if [ -f "$CONF" ]; then
  echo "CNI config file exists. Contents:"
  cat "$CONF"
  echo ""
  echo "Checking for emu-cni in config..."
  # 检查配置文件中是否已存在 emu-cni
  if grep -q "\"type\":[[:space:]]*\"emu-cni\"" "$CONF"; then
      echo "emu-cni config already exists in CNI config, skipping config update."
  else
      echo "Appending emu-cni config with precise positioning..."
      
      # 修复逻辑：
      # N; 使得 sed 能够看到下一行。
      # 只有当下一行是 plugins 数组的结束符 ] 时，才在当前行末尾 } 加逗号。
      # 这完美避开了 capabilities 内部的大括号。
      sed -i 'N; s/}[[:space:]]*\n[[:space:]]*\]/},\n    \]/; P; D' "$CONF"
      
      # 在 ] 之前插入新块
      sed -i '/\]/i \        {\n            "type": "emu-cni"\n        }' "$CONF"
      
      # 最后修正：确保最后一个插件块后面没有多余逗号（防止 JSON 严格模式报错）
      # 匹配： }, 换行 ]  替换为： } 换行 ]
      sed -i 'N; s/},[[:space:]]*\n[[:space:]]*\]/}\n    \]/; P; D' "$CONF"
      
      echo "Successfully updated CNI config."
  fi
else
  echo "CNI config file not found: $CONF"
fi

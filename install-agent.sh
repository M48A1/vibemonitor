#!/usr/bin/env bash
set -e

SERVER=""
TOKEN=""
GITHUB_USER=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --server|-s)
      SERVER="$2"
      shift 2
      ;;
    --token|-t)
      TOKEN="$2"
      shift 2
      ;;
    --github-user|-u)
      GITHUB_USER="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [ -z "$SERVER" ] || [ -z "$TOKEN" ]; then
  echo "❌ 缺少必要参数！"
  echo "使用说明:"
  echo "  curl -fsSL https://raw.githubusercontent.com/<你的用户名>/vibemonitor/main/install-agent.sh | bash -s -- --server <主控地址> --token <通信密钥> --github-user <你的用户名>"
  exit 1
fi

echo "🚀 开始安装 VibeMonitor Agent..."

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)
    BIN_NAME="vibemonitor-agent"
    ;;
  aarch64|arm64)
    BIN_NAME="vibemonitor-agent-arm64"
    ;;
  *)
    echo "❌ 暂不支持当前 CPU 架构: $ARCH"
    exit 1
    ;;
esac

INSTALL_DIR="/usr/local/bin"

if [ -n "$GITHUB_USER" ]; then
  DOWNLOAD_URL="https://github.com/${GITHUB_USER}/vibemonitor/releases/latest/download/${BIN_NAME}"
  echo "📥 正在从 GitHub 下载 Agent: ${DOWNLOAD_URL}"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$DOWNLOAD_URL" -o "${INSTALL_DIR}/vibemonitor-agent" || true
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "${INSTALL_DIR}/vibemonitor-agent" "$DOWNLOAD_URL" || true
  fi
fi

if [ ! -f "${INSTALL_DIR}/vibemonitor-agent" ]; then
  echo "⚠️ 未在 ${INSTALL_DIR}/ 找到 vibemonitor-agent 可执行文件。"
  echo "👉 请确认您已在 GitHub 发布 Releases，或手动将编译好的二进制文件复制到 ${INSTALL_DIR}/vibemonitor-agent"
  exit 1
fi

chmod +x "${INSTALL_DIR}/vibemonitor-agent"

echo "⚙️ 正在配置 Systemd 开机自启动服务..."
cat << SERVICE_EOF > /etc/systemd/system/vibemonitor-agent.service
[Unit]
Description=VibeMonitor Agent
After=network.target

[Service]
Type=simple
Environment="VIBEMONITOR_SERVER=${SERVER}"
Environment="VIBEMONITOR_TOKEN=${TOKEN}"
Environment="VIBEMONITOR_INTERVAL=2"
ExecStart=${INSTALL_DIR}/vibemonitor-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SERVICE_EOF

systemctl daemon-reload
systemctl enable --now vibemonitor-agent

echo "✅ VibeMonitor Agent 安装并启动成功！"
echo "📡 已连接主控: ${SERVER}"
echo "🔍 查看运行状态: systemctl status vibemonitor-agent"

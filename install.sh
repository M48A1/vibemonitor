#!/usr/bin/env bash
#=============================================================================
#  ⚡ VibeMonitor Linux VPS One-Line Installer
#  GitHub: https://github.com/m48a1/vibemonitor
#=============================================================================

set -e

RED="\033[31m"
GREEN="\033[32m"
YELLOW="\033[33m"
BLUE="\033[36m"
PLAIN="\033[0m"

GITHUB_REPO="m48a1/vibemonitor"
INSTALL_BIN="/usr/local/bin/vibemonitor"
CONFIG_DIR="/etc/vibemonitor"
SERVER_SERVICE="vibemonitor-server"
AGENT_SERVICE="vibemonitor-agent"

info() {
    echo -e "${BLUE}[INFO]${PLAIN} $1"
}

success() {
    echo -e "${GREEN}[SUCCESS]${PLAIN} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${PLAIN} $1"
}

error() {
    echo -e "${RED}[ERROR]${PLAIN} $1"
    exit 1
}

check_root() {
    if [ "$(id -u)" != "0" ]; then
        error "Please run this script as root (e.g. sudo bash $0)."
    fi
}

detect_arch() {
    if [ "$(uname -s)" != "Linux" ]; then
        error "Only Linux x86-64 is supported."
    fi
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)
            SYSTEM_ARCH="amd64"
            ;;
        *)
            error "Only x86-64 (Intel/AMD 64-bit) is supported. Detected: $arch"
            ;;
    esac
    info "Detected CPU Architecture: ${GREEN}${SYSTEM_ARCH}${PLAIN}"
}

check_dependencies() {
    for cmd in curl tar systemctl; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            info "Installing required dependency: $cmd..."
            if command -v apt-get >/dev/null 2>&1; then
                apt-get update -y && apt-get install -y "$cmd"
            elif command -v yum >/dev/null 2>&1; then
                yum install -y "$cmd"
            elif command -v dnf >/dev/null 2>&1; then
                dnf install -y "$cmd"
            elif command -v apk >/dev/null 2>&1; then
                apk add --no-cache "$cmd"
            else
                error "Command '$cmd' is missing and package manager is not supported. Please install it manually."
            fi
        fi
    done
}

download_binary() {
    detect_arch
    check_dependencies

    mkdir -p "$CONFIG_DIR"
    local tmp_dir
    tmp_dir=$(mktemp -d)
    trap 'rm -rf "$tmp_dir"' EXIT

    info "Fetching latest release binary for ${SYSTEM_ARCH} from GitHub..."

    # Direct official GitHub download URLs
    local tar_url="https://github.com/${GITHUB_REPO}/releases/latest/download/vibemonitor-linux-${SYSTEM_ARCH}.tar.gz"
    local raw_bin_url="https://github.com/${GITHUB_REPO}/releases/latest/download/vibemonitor-linux-${SYSTEM_ARCH}"

    local download_success=0

    # Try downloading tar.gz archive first
    if curl -fsSL -o "${tmp_dir}/vibemonitor.tar.gz" "$tar_url" 2>/dev/null; then
        if tar -xzf "${tmp_dir}/vibemonitor.tar.gz" -C "$tmp_dir" 2>/dev/null && [ -f "${tmp_dir}/vibemonitor" ]; then
            cp "${tmp_dir}/vibemonitor" "$INSTALL_BIN"
            download_success=1
        fi
    fi

    # Fallback to direct binary if tar.gz is not available
    if [ "$download_success" -eq 0 ]; then
        if curl -fsSL -o "${tmp_dir}/vibemonitor" "$raw_bin_url" 2>/dev/null; then
            cp "${tmp_dir}/vibemonitor" "$INSTALL_BIN"
            download_success=1
        fi
    fi

    if [ "$download_success" -eq 0 ]; then
        error "Failed to download vibemonitor binary from GitHub (${GITHUB_REPO}). Please check your network connection to GitHub or verify if a release exists."
    fi

    chmod +x "$INSTALL_BIN"
    success "VibeMonitor binary installed to ${INSTALL_BIN}."
}

install_server() {
    local listen_port="${1:-1314}"
    local admin_password="$2"

    check_root
    download_binary

    info "Configuring VibeMonitor Master Server..."

    local exec_cmd="${INSTALL_BIN} server --listen 0.0.0.0:${listen_port} --data ${CONFIG_DIR}/vibemonitor-data.json"
    if [ -n "$admin_password" ]; then
        exec_cmd="${exec_cmd} --admin-password ${admin_password}"
    fi

    cat > "/etc/systemd/system/${SERVER_SERVICE}.service" <<EOF
[Unit]
Description=VibeMonitor Master Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${CONFIG_DIR}
ExecStart=${exec_cmd}
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable "${SERVER_SERVICE}" >/dev/null 2>&1
    systemctl restart "${SERVER_SERVICE}"

    success "================================================="
    success "  🎉 VibeMonitor Master Server successfully started! "
    success "  Dashboard URL: http://<Your_VPS_IP>:${listen_port}"
    success "  Service Status: systemctl status ${SERVER_SERVICE}"
    success "================================================="
    if [ -z "$admin_password" ]; then
        info "Admin password was automatically generated."
        info "Run 'journalctl -u ${SERVER_SERVICE} -n 30' to view the initial password."
    fi
}

install_agent() {
    local server_url="$1"
    local token="$2"
    local interval="${3:-3s}"

    if [ -z "$server_url" ] || [ -z "$token" ]; then
        error "Server URL and Token are required to install Agent. Usage: $0 agent -s <SERVER_URL> -t <TOKEN>"
    fi

    check_root
    download_binary

    info "Configuring VibeMonitor Agent Probe..."

    cat > "/etc/systemd/system/${AGENT_SERVICE}.service" <<EOF
[Unit]
Description=VibeMonitor Server Monitor Agent
After=network.target

[Service]
Type=simple
User=root
ExecStart=${INSTALL_BIN} agent --server ${server_url} --token ${token} --interval ${interval}
Restart=always
RestartSec=5
KillMode=process

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable "${AGENT_SERVICE}" >/dev/null 2>&1
    systemctl restart "${AGENT_SERVICE}"

    success "================================================="
    success "  ✅ VibeMonitor Agent successfully started! "
    success "  Target Master: ${server_url}"
    success "  Service Status: systemctl status ${AGENT_SERVICE}"
    success "================================================="
}

uninstall_all() {
    check_root
    info "Uninstalling VibeMonitor..."

    for svc in "$SERVER_SERVICE" "$AGENT_SERVICE" "vibemonitor"; do
        if systemctl is-active --quiet "$svc" 2>/dev/null || systemctl is-enabled --quiet "$svc" 2>/dev/null; then
            info "Stopping and disabling service: $svc..."
            systemctl stop "$svc" 2>/dev/null || true
            systemctl disable "$svc" 2>/dev/null || true
        fi
        rm -f "/etc/systemd/system/${svc}.service"
    done
    systemctl daemon-reload

    rm -f "$INSTALL_BIN"
    read -r -p "Do you want to delete configuration and data directory (${CONFIG_DIR})? [y/N]: " confirm_del
    if [[ "$confirm_del" =~ ^[yY]$ ]]; then
        rm -rf "$CONFIG_DIR"
        info "Deleted ${CONFIG_DIR}."
    fi

    success "VibeMonitor has been completely removed."
}

show_status() {
    echo -e "${BLUE}=== VibeMonitor Services Status ===${PLAIN}"
    if systemctl is-active --quiet "$SERVER_SERVICE" 2>/dev/null; then
        echo -e "Server Service (${SERVER_SERVICE}): ${GREEN}Running${PLAIN}"
    else
        echo -e "Server Service (${SERVER_SERVICE}): ${RED}Stopped / Not installed${PLAIN}"
    fi

    if systemctl is-active --quiet "$AGENT_SERVICE" 2>/dev/null; then
        echo -e "Agent Service (${AGENT_SERVICE}): ${GREEN}Running${PLAIN}"
    else
        echo -e "Agent Service (${AGENT_SERVICE}): ${RED}Stopped / Not installed${PLAIN}"
    fi
}

menu() {
    clear
    echo -e "${BLUE}=================================================${PLAIN}"
    echo -e "${GREEN}       ⚡ VibeMonitor VPS Management Script      ${PLAIN}"
    echo -e "${BLUE}=================================================${PLAIN}"
    echo -e "  1. 安装 / 更新 Master 服务端 (Server)"
    echo -e "  2. 安装 / 更新 Agent 客户端探针 (Agent)"
    echo -e "  3. 查看运行状态 (Status)"
    echo -e "  4. 重启服务 (Restart)"
    echo -e "  5. 停止服务 (Stop)"
    echo -e "  6. 查看运行日志 (Logs)"
    echo -e "  7. 彻底卸载 (Uninstall)"
    echo -e "  0. 退出 (Exit)"
    echo -e "${BLUE}=================================================${PLAIN}"
    read -r -p "请输入选项 [0-7]: " choice

    case "$choice" in
        1)
            read -r -p "请输入服务端监听端口 [默认 1314]: " port
            port=${port:-1314}
            read -r -p "请输入管理员密码 (回车留空则首次启动自动生成): " pass
            install_server "$port" "$pass"
            ;;
        2)
            read -r -p "请输入 Master 服务端地址 (例如 http://1.2.3.4:1314): " s_url
            read -r -p "请输入节点 Token: " s_token
            read -r -p "请输入上报频率 [默认 3s]: " s_interval
            s_interval=${s_interval:-3s}
            install_agent "$s_url" "$s_token" "$s_interval"
            ;;
        3)
            show_status
            ;;
        4)
            systemctl restart "$SERVER_SERVICE" 2>/dev/null || true
            systemctl restart "$AGENT_SERVICE" 2>/dev/null || true
            success "Services restarted."
            show_status
            ;;
        5)
            systemctl stop "$SERVER_SERVICE" 2>/dev/null || true
            systemctl stop "$AGENT_SERVICE" 2>/dev/null || true
            info "Services stopped."
            show_status
            ;;
        6)
            echo "1. Master Server 日志"
            echo "2. Agent 探针日志"
            read -r -p "请选择 [1-2]: " log_choice
            if [ "$log_choice" = "1" ]; then
                journalctl -u "$SERVER_SERVICE" -f -n 50
            else
                journalctl -u "$AGENT_SERVICE" -f -n 50
            fi
            ;;
        7)
            uninstall_all
            ;;
        0)
            exit 0
            ;;
        *)
            echo -e "${RED}无效选项${PLAIN}"
            ;;
    esac
}

# Parse command line arguments
if [ $# -eq 0 ]; then
    menu
    exit 0
fi

CMD="$1"
shift

case "$CMD" in
    server)
        PORT="1314"
        PASSWORD=""
        while [[ $# -gt 0 ]]; do
            case "$1" in
                -p|--port)
                    PORT="$2"
                    shift 2
                    ;;
                -w|--password)
                    PASSWORD="$2"
                    shift 2
                    ;;
                *)
                    shift
                    ;;
            esac
        done
        install_server "$PORT" "$PASSWORD"
        ;;
    agent)
        SERVER_URL=""
        TOKEN=""
        INTERVAL="3s"
        while [[ $# -gt 0 ]]; do
            case "$1" in
                -s|--server)
                    SERVER_URL="$2"
                    shift 2
                    ;;
                -t|--token)
                    TOKEN="$2"
                    shift 2
                    ;;
                -i|--interval)
                    INTERVAL="$2"
                    shift 2
                    ;;
                *)
                    shift
                    ;;
            esac
        done
        install_agent "$SERVER_URL" "$TOKEN" "$INTERVAL"
        ;;
    uninstall)
        uninstall_all
        ;;
    status)
        show_status
        ;;
    restart)
        systemctl restart "$SERVER_SERVICE" 2>/dev/null || true
        systemctl restart "$AGENT_SERVICE" 2>/dev/null || true
        show_status
        ;;
    help|--help|-h)
        echo "VibeMonitor VPS Installer"
        echo "Usage: $0 [command] [options]"
        echo ""
        echo "Commands:"
        echo "  server    Install Master Server:  $0 server [-p port] [-w password]"
        echo "  agent     Install Agent Probe:    $0 agent -s <server_url> -t <token> [-i interval]"
        echo "  status    Show service status:    $0 status"
        echo "  restart   Restart services:       $0 restart"
        echo "  uninstall Completely uninstall:   $0 uninstall"
        ;;
    *)
        error "Unknown command: $CMD. Use '$0 help' for usage."
        ;;
esac

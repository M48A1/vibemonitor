#!/usr/bin/env bash
# VibeMonitor Linux x86-64 installer and maintenance commands.
set -e
umask 077
GITHUB_REPO="M48A1/vibemonitor"
INSTALL_BIN="/usr/local/bin/vibemonitor"
CONFIG_DIR="/etc/vibemonitor"
UNIT_DIR="/etc/systemd/system"
SERVER_SERVICE="vibemonitor-server"
AGENT_SERVICE="vibemonitor-agent"

# Color only on interactive terminals; logs remain readable when redirected.
C_RESET='' C_BLUE='' C_GREEN='' C_YELLOW=''
if [ -t 1 ] && [ "${TERM:-dumb}" != dumb ] && [ -z "${NO_COLOR:-}" ]; then
    C_RESET=$'\033[0m'; C_BLUE=$'\033[1;36m'; C_GREEN=$'\033[1;32m'; C_YELLOW=$'\033[1;33m'
fi
info() { printf '%s[信息]%s %s\n' "$C_BLUE" "$C_RESET" "$*"; }
success() { echo "[OK] $*"; }
warn() { echo "[WARN] $*" >&2; }
error() { echo "[ERROR] $*" >&2; exit 1; }
check_root() { [ "$(id -u)" = 0 ] || error "Run as root."; }

detect_arch() {
    [ "$(uname -s)" = Linux ] || error "Only Linux x86-64 is supported."
    case "$(uname -m)" in
        x86_64|amd64) SYSTEM_ARCH=amd64 ;;
        *) error "Only x86-64 (Intel/AMD 64-bit) is supported." ;;
    esac
}

check_dependencies() {
    for cmd in curl sha256sum systemctl mktemp od awk; do
        command -v "$cmd" >/dev/null 2>&1 || error "Missing dependency: $cmd. Install it before continuing."
    done
    [ -d /run/systemd/system ] || error "This installer requires a running systemd system."
}

resolve_release() {
    local effective tag
    effective=$(curl -4 -fsSL --connect-timeout 10 --max-time 60 -o /dev/null -w '%{url_effective}' "https://github.com/${GITHUB_REPO}/releases/latest")
    tag=${effective##*/}
    [[ "$effective" == https://github.com/*/releases/tag/* && "$tag" =~ ^[A-Za-z0-9._-]+$ ]] || error "Could not resolve a release version."
    RELEASE_BASE="https://github.com/${GITHUB_REPO}/releases/download/${tag}"
}

verify_checksum() {
    local file="$1" manifest="$2" asset="$3" expected
    expected=$(awk -v asset="$asset" '$2 == asset {print $1}' "$manifest")
    [[ "$expected" =~ ^[[:xdigit:]]{64}$ ]] || error "Missing or invalid checksum for $asset."
    printf '%s  %s\n' "$expected" "$file" | sha256sum -c - >/dev/null || error "Checksum verification failed."
}

# The replacement and old binary live on the destination filesystem for atomic rename.
begin_update() {
    UPDATE_SERVICE="$1"
    check_root; detect_arch; check_dependencies
    mkdir -p "$(dirname "$INSTALL_BIN")" "$CONFIG_DIR" "$UNIT_DIR"
    UPDATE_DIR=$(mktemp -d "${INSTALL_BIN}.update.XXXXXX")
    UPDATE_COMMITTED=0
    BINARY_REPLACED=0
    WAS_ACTIVE=0
    WAS_ENABLED=0
    systemctl is-active --quiet "$UPDATE_SERVICE" && WAS_ACTIVE=1
    systemctl is-enabled --quiet "$UPDATE_SERVICE" && WAS_ENABLED=1
    trap 'rm -rf "$UPDATE_DIR"' EXIT
    if [ -f "$INSTALL_BIN" ]; then cp -p "$INSTALL_BIN" "$UPDATE_DIR/previous-binary"; fi
    if [ -f "$UNIT_DIR/$UPDATE_SERVICE.service" ]; then cp -p "$UNIT_DIR/$UPDATE_SERVICE.service" "$UPDATE_DIR/previous-unit"; fi
    trap cleanup_update EXIT
}

cleanup_update() {
    local result=$? rollback_failed=0
    trap - EXIT
    if [ "$UPDATE_COMMITTED" != 1 ] && [ "$BINARY_REPLACED" = 1 ]; then
        warn "Installation failed; restoring the previous binary and service configuration."
        systemctl stop "$UPDATE_SERVICE" >/dev/null 2>&1 || rollback_failed=1
        if [ -f "$UPDATE_DIR/previous-binary" ]; then
            # Keep the recovery copy until every rollback step has succeeded.
            if ! cp -p "$UPDATE_DIR/previous-binary" "$UPDATE_DIR/restore-binary" || ! mv -f "$UPDATE_DIR/restore-binary" "$INSTALL_BIN"; then
                warn "Could not restore binary."
                rollback_failed=1
            fi
        else
            rm -f "$INSTALL_BIN" || rollback_failed=1
        fi
        if [ -f "$UPDATE_DIR/previous-unit" ]; then
            cp -p "$UPDATE_DIR/previous-unit" "$UNIT_DIR/$UPDATE_SERVICE.service" || rollback_failed=1
        else
            rm -f "$UNIT_DIR/$UPDATE_SERVICE.service" || rollback_failed=1
        fi
        if [ "$WAS_ENABLED" = 0 ]; then systemctl disable "$UPDATE_SERVICE" >/dev/null 2>&1 || rollback_failed=1; fi
        systemctl daemon-reload || rollback_failed=1
        if [ "$WAS_ACTIVE" = 1 ]; then systemctl restart "$UPDATE_SERVICE" || rollback_failed=1; fi
        result=1
    fi
    if [ "$rollback_failed" = 1 ]; then
        warn "Rollback incomplete. Recovery files retained at: $UPDATE_DIR"
        warn "Check $UPDATE_SERVICE before removing those files."
    else
        rm -rf "$UPDATE_DIR"
    fi
    exit "$result"
}

download_binary() {
    resolve_release
    local asset="vibemonitor-linux-amd64"
    info "正在下载 ${RELEASE_BASE##*/} · Linux x86-64"
    local progress=(-sS)
    if [ -t 2 ]; then progress=(--progress-bar --show-error); fi
    curl -4 -fL "${progress[@]}" --connect-timeout 10 --max-time 180 -o "$UPDATE_DIR/new-binary" "$RELEASE_BASE/$asset"
    info "正在校验下载文件…"
    curl -4 -fsSL --connect-timeout 10 --max-time 60 -o "$UPDATE_DIR/sha256sums.txt" "$RELEASE_BASE/sha256sums.txt"
    verify_checksum "$UPDATE_DIR/new-binary" "$UPDATE_DIR/sha256sums.txt" "$asset"
    # Reject HTML, scripts, wrong ELF class, and wrong machine architecture.
    [ "$(od -An -tx1 -N5 "$UPDATE_DIR/new-binary" | tr -d ' \n')" = 7f454c4602 ] || error "Download is not a 64-bit ELF executable."
    [ "$(od -An -tx1 -j18 -N2 "$UPDATE_DIR/new-binary" | tr -d ' \n')" = 3e00 ] || error "Download is not an x86-64 executable."
    chmod 755 "$UPDATE_DIR/new-binary"
    "$UPDATE_DIR/new-binary" version
    mv -f "$UPDATE_DIR/new-binary" "$INSTALL_BIN"
    BINARY_REPLACED=1
}

# Escape one systemd ExecStart argument; never interpret it as shell source.
unit_arg() {
    local value="$1"
    [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || error "Arguments cannot contain line breaks."
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//\$/\$\$}
    value=${value//%/%%}
    printf '"%s"' "$value"
}

finish_update() {
    local port="${1:-}"
    info "正在启动服务并检查运行状态…"
    chmod 600 "$UNIT_DIR/$UPDATE_SERVICE.service"
    systemctl daemon-reload
    systemctl enable "$UPDATE_SERVICE" >/dev/null
    systemctl restart "$UPDATE_SERVICE"
    sleep 3
    systemctl is-active --quiet "$UPDATE_SERVICE" || error "Service did not remain running."
    if [ -n "$port" ]; then
        [ "$(curl -4 -fsS --max-time 5 "http://127.0.0.1:$port/ping")" = pong ] || error "Server health check failed."
    fi
    UPDATE_COMMITTED=1
    success "$UPDATE_SERVICE is running. Agent connectivity can be checked in the dashboard and journal."
}

confirm_backup_cleanup() {
    local confirmation
    warn "此操作将删除 $CONFIG_DIR/backups 内的全部备份，无法恢复。当前配置和监控数据保留。"
    read_input "确认继续安装/更新或卸载？输入 yes 继续，其他输入取消: " confirmation
    [ "$confirmation" = yes ]
}

clear_backups() {
    # Only remove this application's dedicated backup directory.
    [ -n "$CONFIG_DIR" ] && [ "$CONFIG_DIR" != / ] || error "Invalid configuration directory."
    [ ! -L "$CONFIG_DIR/backups" ] || error "Backup directory must not be a symbolic link."
    if [ -e "$CONFIG_DIR/backups" ]; then
        rm -rf -- "$CONFIG_DIR/backups"
        success "旧备份已全部删除。"
    fi
}

install_server() {
    local port="${1:-1314}" password="${2:-}" username="${3:-admin}"
    [[ "$port" =~ ^[0-9]+$ && ${#port} -le 5 ]] || error "Invalid port."
    (( 10#$port >= 1 && 10#$port <= 65535 )) || error "Port must be 1-65535."
    check_root; detect_arch; check_dependencies
    confirm_backup_cleanup || return 0
    clear_backups
    begin_update "$SERVER_SERVICE"
    download_binary
    local args
    args="$(unit_arg "$INSTALL_BIN") server --listen $(unit_arg "0.0.0.0:$port") --data $(unit_arg "$CONFIG_DIR/vibemonitor-data.json")"
    args="$args --admin-username $(unit_arg "$username")"
    if [ -n "$password" ]; then args="$args --admin-password $(unit_arg "$password")"; fi
    cat > "$UNIT_DIR/$SERVER_SERVICE.service" <<EOF
[Unit]
Description=VibeMonitor Server
After=network-online.target
[Service]
Type=simple
WorkingDirectory=$CONFIG_DIR
ExecStart=$args
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target
EOF
    finish_update "$port"
    info "Initial password, if generated: journalctl -u $SERVER_SERVICE -n 30"
}

install_agent() {
    local server="$1" token="$2" interval="${3:-3s}"
    [[ "$server" == http://* || "$server" == https://* ]] || error "Server URL must start with http:// or https://."
    [ -n "$token" ] || error "A node token is required."
    check_root; detect_arch; check_dependencies
    confirm_backup_cleanup || return 0
    clear_backups
    begin_update "$AGENT_SERVICE"
    download_binary
    cat > "$UNIT_DIR/$AGENT_SERVICE.service" <<EOF
[Unit]
Description=VibeMonitor Agent
After=network-online.target
[Service]
Type=simple
ExecStart=$(unit_arg "$INSTALL_BIN") agent --server $(unit_arg "$server") --token $(unit_arg "$token") --interval $(unit_arg "$interval")
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
EOF
    finish_update
}

backup_data() {
    check_root; detect_arch
    local was_active=0 result=0 destination
    [ -f "$CONFIG_DIR/vibemonitor-data.json" ] || error "No server data exists."
    mkdir -p "$CONFIG_DIR/backups"
    chmod 700 "$CONFIG_DIR/backups"
    systemctl is-active --quiet "$SERVER_SERVICE" && was_active=1
    if [ "$was_active" = 1 ]; then systemctl stop "$SERVER_SERVICE"; fi
    destination=$(mktemp "$CONFIG_DIR/backups/data-$(date +%Y%m%d-%H%M%S).XXXXXX") || result=1
    if [ "$result" = 0 ]; then cp "$CONFIG_DIR/vibemonitor-data.json" "$destination" || result=1; fi
    if [ "$result" = 0 ] && [ -f "$CONFIG_DIR/vibemonitor-data.json.ping.json" ]; then
        cp "$CONFIG_DIR/vibemonitor-data.json.ping.json" "$destination.ping.json" || result=1
    fi
    if [ "$was_active" = 1 ]; then systemctl start "$SERVER_SERVICE" || result=1; fi
    [ "$result" = 0 ] || error "Backup failed; check the service status."
    success "Backup saved: $destination"
}

restore_data() {
    check_root; detect_arch
    local source="$1" was_active=0 staged previous ping_path
    ping_path="$CONFIG_DIR/vibemonitor-data.json.ping.json"
    "$INSTALL_BIN" validate-data "$source"
    backup_data
    staged=$(mktemp "$CONFIG_DIR/.restore.XXXXXX")
    previous=$(mktemp "$CONFIG_DIR/.previous.XXXXXX")
    cp "$source" "$staged"
    if [ -f "$source.ping.json" ]; then cp "$source.ping.json" "$staged.ping.json"; fi
    systemctl is-active --quiet "$SERVER_SERVICE" && was_active=1
    if [ "$was_active" = 1 ]; then systemctl stop "$SERVER_SERVICE"; fi
    if [ -f "$ping_path" ]; then
        if ! cp "$ping_path" "$previous.ping.json"; then
            if [ "$was_active" = 1 ]; then systemctl start "$SERVER_SERVICE"; fi
            error "Could not preserve previous ping data."
        fi
    fi
    if ! cp "$CONFIG_DIR/vibemonitor-data.json" "$previous" || ! mv -f "$staged" "$CONFIG_DIR/vibemonitor-data.json"; then
        if [ "$was_active" = 1 ]; then systemctl start "$SERVER_SERVICE"; fi
        rm -f "$staged" "$previous"
        error "Restore failed."
    fi
    local ping_result=0
    if [ -f "$staged.ping.json" ]; then
        mv -f "$staged.ping.json" "$ping_path" || ping_result=1
    else
        rm -f "$ping_path" || ping_result=1
    fi
    if [ "$ping_result" != 0 ]; then
        mv -f "$previous" "$CONFIG_DIR/vibemonitor-data.json"
        if [ -f "$previous.ping.json" ]; then mv -f "$previous.ping.json" "$ping_path"; fi
        if [ "$was_active" = 1 ]; then systemctl start "$SERVER_SERVICE"; fi
        error "Could not restore ping data; previous data restored."
    fi
    if [ "$was_active" = 1 ]; then
        if ! systemctl start "$SERVER_SERVICE" || ! sleep 3 || ! systemctl is-active --quiet "$SERVER_SERVICE"; then
            systemctl stop "$SERVER_SERVICE" || true
            mv -f "$previous" "$CONFIG_DIR/vibemonitor-data.json"
            if [ -f "$previous.ping.json" ]; then
                mv -f "$previous.ping.json" "$ping_path"
            else
                rm -f "$ping_path"
            fi
            systemctl start "$SERVER_SERVICE" || true
            error "Restored data could not start; previous data restored."
        fi
    fi
    rm -f "$previous" "$previous.ping.json"
    success "Data restored. Restored passwords and node tokens now apply."
}

read_input() {
    local prompt="$1" variable="$2"
    # Read the controlling terminal, never the piped script source.
    printf '%s' "$prompt"
    if ! { read -r "$variable" </dev/tty; } 2>/dev/null; then
        error "No interactive terminal. Use: bash install.sh server, agent, backup, or restore FILE."
    fi
}

show_status() { systemctl --no-pager status "$SERVER_SERVICE" "$AGENT_SERVICE" || true; }
uninstall_all() {
    check_root
    confirm_backup_cleanup || return 0
    clear_backups
    for service in "$SERVER_SERVICE" "$AGENT_SERVICE" vibemonitor; do
        systemctl stop "$service" 2>/dev/null || true
        systemctl disable "$service" 2>/dev/null || true
        rm -f "$UNIT_DIR/$service.service"
    done
    systemctl daemon-reload
    rm -f "$INSTALL_BIN"
    info "Programs removed. Server data retained in $CONFIG_DIR; backups deleted."
}
read_secret() {
    local prompt="$1" variable="$2"
    printf '%s' "$prompt"
    if ! { read -r -s "$variable" </dev/tty; } 2>/dev/null; then
        error "No interactive terminal. Use command-line options."
    fi
    printf '\n'
}

service_label() {
    if ! command -v systemctl >/dev/null 2>&1; then
        printf '不可用'
    elif systemctl is-active --quiet "$1"; then
        printf '运行中'
    elif [ -f "$UNIT_DIR/$1.service" ]; then
        printf '已停止'
    else
        printf '未安装'
    fi
}

menu_header() {
    printf '\n%s================================================%s\n' "$C_BLUE" "$C_RESET"
    printf '              VibeMonitor 管理面板\n'
    printf '================================================\n'
    printf '  支持系统  Linux x86-64 · IPv4\n'
    printf '  服务端    %s    |    探针  %s\n' "$(service_label "$SERVER_SERVICE")" "$(service_label "$AGENT_SERVICE")"
    printf '%s------------------------------------------------%s\n' "$C_BLUE" "$C_RESET"
    printf '  安装与更新\n    1. 安装 / 更新服务端\n    2. 安装 / 更新探针\n\n'
    printf '  服务管理\n    3. 查看状态\n    4. 重启服务\n    5. 停止服务\n    6. 查看最近日志\n\n'
    printf '  数据与维护\n    8. 备份数据\n    9. 恢复备份\n    7. 卸载程序（保留数据、删除备份）\n\n'
    printf '    0. 退出\n'
    printf '%s================================================%s\n' "$C_BLUE" "$C_RESET"
}

manage_services() {
    local action="$1" service found=0
    check_root
    for service in "$SERVER_SERVICE" "$AGENT_SERVICE"; do
        if [ -f "$UNIT_DIR/$service.service" ]; then
            systemctl "$action" "$service"
            found=1
        fi
    done
    if [ "$found" = 0 ]; then warn "尚未安装服务。"; fi
}

menu() {
    local choice port password username server token interval source confirm pause
    while true; do
        menu_header
        read_input "请选择 [0-9]: " choice
        case "$choice" in
            # Run installations in a subshell so their EXIT rollback always runs,
            # even when the interactive menu is kept open afterwards.
            1) read_input "监听端口 [1314]: " port
               if [ -f "$CONFIG_DIR/vibemonitor-data.json" ]; then
                   info "已有管理员账号和密码将保留；以下设置仅首次安装生效。"
               fi
               read_input "管理员账号 [admin，仅一个管理员]: " username
               read_secret "初始密码 [留空自动生成，已有密码不变]: " password
               ( install_server "${port:-1314}" "$password" "${username:-admin}" ) ;;
            2) read_input "主控地址（http:// 或 https://）: " server
               read_secret "节点 Token（输入不显示）: " token
               read_input "上报间隔 [3s]: " interval
               ( install_agent "$server" "$token" "${interval:-3s}" ) ;;
            3) show_status ;;
            4) manage_services restart ;;
            5) manage_services stop ;;
            6) journalctl --no-pager -u "$SERVER_SERVICE" -u "$AGENT_SERVICE" -n 50 ;;
            7) uninstall_all ;;
            8) backup_data ;;
            9) read_input "备份主文件路径: " source
               read_input "将覆盖当前配置、密码和数据。输入 yes 继续: " confirm
               if [ "$confirm" = yes ]; then restore_data "$source"; fi ;;
            0) return ;;
            *) warn "请输入 0 到 9 之间的菜单编号。" ;;
        esac
        read_input "按回车返回管理菜单…" pause
    done
}

# Sourcing is supported for isolated installer tests.
if [[ -n "${BASH_SOURCE[0]:-}" && "${BASH_SOURCE[0]}" != "$0" ]]; then return; fi
CMD="${1:-menu}"
if [ $# -gt 0 ]; then shift; fi
case "$CMD" in
    server)
        port=1314; password=""; username=admin
        while [ $# -gt 0 ]; do
            case "$1" in
                -p|--port) port="${2:?missing port}"; shift 2 ;;
                -u|--username) username="${2:?missing username}"; shift 2 ;;
                -w|--password) password="${2:?missing password}"; shift 2 ;;
                *) error "Unknown server option: $1" ;;
            esac
        done
        install_server "$port" "$password" "$username" ;;
    agent)
        server=""; token=""; interval=3s
        while [ $# -gt 0 ]; do
            case "$1" in
                -s|--server) server="${2:?missing server}"; shift 2 ;;
                -t|--token) token="${2:?missing token}"; shift 2 ;;
                -i|--interval) interval="${2:?missing interval}"; shift 2 ;;
                *) error "Unknown agent option: $1" ;;
            esac
        done
        install_agent "$server" "$token" "$interval" ;;
    menu) menu ;;
    backup) backup_data ;;
    restore) restore_data "${1:?backup file required}" ;;
    status) show_status ;;
    restart) systemctl restart "$SERVER_SERVICE" "$AGENT_SERVICE" ;;
    uninstall) uninstall_all ;;
    help|--help|-h) echo "Usage: $0 [server [-p PORT] [-w PASSWORD] | agent -s URL -t TOKEN [-i INTERVAL] | backup | restore FILE | status | restart | uninstall]" ;;
    *) error "Unknown command: $CMD" ;;
esac

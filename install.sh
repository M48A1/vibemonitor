#!/usr/bin/env bash
# VibeMonitor Linux (x86-64 / arm64) installer and maintenance commands.
set -e
umask 077
GITHUB_REPO="M48A1/vibemonitor"
INSTALL_BIN="/usr/local/bin/vibemonitor"
CONFIG_DIR="/etc/vibemonitor"
UNIT_DIR="/etc/systemd/system"
SERVER_SERVICE="vibemonitor-server"
AGENT_SERVICE="vibemonitor-agent"
NGINX_CONF="/etc/nginx/conf.d/vibemonitor.conf"
ACME_WEBROOT="/var/www/vibemonitor-acme"
CERTBOT_DEPLOY_HOOK="/etc/letsencrypt/renewal-hooks/deploy/vibemonitor-nginx"
CERTBOT_RENEWAL_DIR="/etc/letsencrypt/renewal"
CERT_REFERENCE_DIRS=(/etc/apache2 /etc/httpd /etc/postfix /etc/dovecot /etc/haproxy /etc/caddy /etc/exim4)
RENEW_SERVICE="$UNIT_DIR/vibemonitor-cert-renew.service"
RENEW_TIMER="$UNIT_DIR/vibemonitor-cert-renew.timer"

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
    [ "$(uname -s)" = Linux ] || error "Only Linux (x86-64 / arm64) is supported."
    case "$(uname -m)" in
        x86_64|amd64) SYSTEM_ARCH=amd64 ;;
        aarch64|arm64) SYSTEM_ARCH=arm64 ;;
        *) error "Only Linux (x86-64 / arm64) is supported." ;;
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
    UNIT_TOUCHED=0
    WAS_ACTIVE=0
    WAS_ENABLED=0
    PROXY_CONFIG_TOUCHED=0
    PROXY_HOOK_TOUCHED=0
    PROXY_NGINX_WAS_ACTIVE=0
    PROXY_NGINX_WAS_ENABLED=0
    RENEW_TIMER_CREATED=0
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
    if [ "$UPDATE_COMMITTED" != 1 ] && { [ "$BINARY_REPLACED" = 1 ] || [ "$UNIT_TOUCHED" = 1 ]; }; then
        warn "操作失败；正在恢复原有程序和服务配置。"
        systemctl stop "$UPDATE_SERVICE" >/dev/null 2>&1 || rollback_failed=1
        if [ "$BINARY_REPLACED" = 1 ]; then
            if [ -f "$UPDATE_DIR/previous-binary" ]; then
                # Keep the recovery copy until every rollback step has succeeded.
                if ! cp -p "$UPDATE_DIR/previous-binary" "$UPDATE_DIR/restore-binary" || ! mv -f "$UPDATE_DIR/restore-binary" "$INSTALL_BIN"; then
                    warn "Could not restore binary."
                    rollback_failed=1
                fi
            else
                rm -f "$INSTALL_BIN" || rollback_failed=1
            fi
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
    if [ "$UPDATE_COMMITTED" != 1 ] && [ "$PROXY_CONFIG_TOUCHED" = 1 ]; then
        if [ -f "$UPDATE_DIR/previous-nginx" ]; then
            cp -p "$UPDATE_DIR/previous-nginx" "$NGINX_CONF" || rollback_failed=1
        else
            rm -f "$NGINX_CONF" || rollback_failed=1
        fi
        if [ "$PROXY_HOOK_TOUCHED" = 1 ]; then
            if [ -f "$UPDATE_DIR/previous-deploy-hook" ]; then
                cp -p "$UPDATE_DIR/previous-deploy-hook" "$CERTBOT_DEPLOY_HOOK" || rollback_failed=1
            else
                rm -f "$CERTBOT_DEPLOY_HOOK" || rollback_failed=1
            fi
        fi
        if command -v nginx >/dev/null 2>&1 && nginx -t; then
            if [ "$PROXY_NGINX_WAS_ACTIVE" = 1 ]; then
                systemctl reload nginx || rollback_failed=1
            else
                systemctl stop nginx || rollback_failed=1
            fi
        else
            rollback_failed=1
        fi
        if [ "$PROXY_NGINX_WAS_ENABLED" = 0 ]; then systemctl disable nginx >/dev/null 2>&1 || rollback_failed=1; fi
        result=1
    fi
    if [ "$UPDATE_COMMITTED" != 1 ] && [ "$RENEW_TIMER_CREATED" = 1 ]; then
        systemctl stop vibemonitor-cert-renew.timer >/dev/null 2>&1 || rollback_failed=1
        systemctl disable vibemonitor-cert-renew.timer >/dev/null 2>&1 || rollback_failed=1
        rm -f "$RENEW_SERVICE" "$RENEW_TIMER" || rollback_failed=1
        systemctl daemon-reload || rollback_failed=1
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
    local asset="vibemonitor-linux-${SYSTEM_ARCH}"
    info "正在下载 ${RELEASE_BASE##*/} · Linux ${SYSTEM_ARCH}"
    local progress=(-sS)
    if [ -t 2 ]; then progress=(--progress-bar --show-error); fi
    curl -4 -fL "${progress[@]}" --connect-timeout 10 --max-time 180 -o "$UPDATE_DIR/new-binary" "$RELEASE_BASE/$asset"
    info "正在校验下载文件…"
    curl -4 -fsSL --connect-timeout 10 --max-time 60 -o "$UPDATE_DIR/sha256sums.txt" "$RELEASE_BASE/sha256sums.txt"
    verify_checksum "$UPDATE_DIR/new-binary" "$UPDATE_DIR/sha256sums.txt" "$asset"
    # Reject HTML, scripts, wrong ELF class, and wrong machine architecture.
    [ "$(od -An -tx1 -N5 "$UPDATE_DIR/new-binary" | tr -d ' \n')" = 7f454c4602 ] || error "Download is not a 64-bit ELF executable."
    local expected_machine="3e00"
    if [ "$SYSTEM_ARCH" = "arm64" ]; then
        expected_machine="b700"
    fi
    [ "$(od -An -tx1 -j18 -N2 "$UPDATE_DIR/new-binary" | tr -d ' \n')" = "$expected_machine" ] || error "Download is not a $SYSTEM_ARCH executable."
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
    local port="${1:-}" domain="${2:-}"
    info "正在启动服务并检查运行状态…"
    chmod 600 "$UNIT_DIR/$UPDATE_SERVICE.service"
    systemctl daemon-reload
    systemctl enable "$UPDATE_SERVICE" >/dev/null
    systemctl restart "$UPDATE_SERVICE"
    sleep 3
    systemctl is-active --quiet "$UPDATE_SERVICE" || error "Service did not remain running."
    if [ -n "$port" ]; then
        [ "$(curl -4 -fsS --noproxy '*' --max-time 5 "http://127.0.0.1:$port/ping")" = pong ] || error "Server health check failed."
    fi
    if [ -n "$domain" ]; then
        [ "$(curl -4 -fsS --noproxy '*' --max-time 10 --resolve "$domain:443:127.0.0.1" "https://$domain/ping")" = pong ] || error "HTTPS reverse proxy health check failed."
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

confirm_full_cleanup() {
    local confirmation
    warn "将永久删除全部配置、管理员账号密码、节点、监控历史和备份，无法恢复。"
    read_input "确认清空重装/彻底卸载？输入 yes 继续，其他输入取消: " confirmation
    [ "$confirmation" = yes ]
}

clear_server_data() {
    [ -n "$CONFIG_DIR" ] && [ "$CONFIG_DIR" != / ] && [ "$CONFIG_DIR" != /etc ] || error "Invalid configuration directory."
    [ ! -L "$CONFIG_DIR" ] || error "Configuration directory must not be a symbolic link."
    if systemctl is-active --quiet "$SERVER_SERVICE"; then
        systemctl stop "$SERVER_SERVICE" || error "Cannot stop server; data was not deleted."
    fi
    # Stop first: shutdown flush must finish before deleting persistent files.
    rm -rf -- "$CONFIG_DIR"
    success "全部配置、账号、监控数据和备份已删除。"
}

# One-time correction of the old installer argument; never reads legacy data files.
switch_server_to_sqlite() {
    local unit="$UNIT_DIR/$SERVER_SERVICE.service" override
    local legacy_arg_pattern='(--data| -d)[ =]+("[^"]*[.]json"|[^[:space:]]*[.]json)([[:space:]]|$)'
    for override in "$UNIT_DIR/$SERVER_SERVICE.service.d/"*.conf; do
        [ -f "$override" ] || continue
        if awk -v pattern="$legacy_arg_pattern" '/^ExecStart=/ && $0 ~ pattern {found=1} END {exit !found}' "$override"; then
            error "Update --data in $override to the existing .db path before upgrading."
        fi
    done
    if ! awk -v pattern="$legacy_arg_pattern" '/^ExecStart=/ && $0 ~ pattern {found=1} END {exit !found}' "$unit"; then return 0; fi
    [ -f "$CONFIG_DIR/vibemonitor-data.db" ] || error "Existing SQLite database missing; complete migration with v1.0.40 before upgrading."
    "$INSTALL_BIN" validate-data "$CONFIG_DIR/vibemonitor-data.db" || error "Existing SQLite database is invalid."
    if ! VIBEMONITOR_OLD_DATA_ARG="--data $(unit_arg "$CONFIG_DIR/vibemonitor-data.json")" \
         VIBEMONITOR_NEW_DATA_ARG="--data $(unit_arg "$CONFIG_DIR/vibemonitor-data.db")" \
         awk '
            /^ExecStart=/ {
                pos=index($0, ENVIRON["VIBEMONITOR_OLD_DATA_ARG"])
                if (pos) {
                    $0=substr($0,1,pos-1) ENVIRON["VIBEMONITOR_NEW_DATA_ARG"] substr($0,pos+length(ENVIRON["VIBEMONITOR_OLD_DATA_ARG"]))
                    changed=1
                }
            }
            {print}
            END {if (!changed) exit 1}
         ' "$unit" > "$UPDATE_DIR/sqlite-unit"; then
        error "Custom data argument detected; update --data in $unit to the existing .db path."
    fi
    chmod 600 "$UPDATE_DIR/sqlite-unit"
    mv -f "$UPDATE_DIR/sqlite-unit" "$unit"
    info "启动参数已切换到现有 SQLite 数据库。"
}

update_server() {
    local port="${1:-1314}"
    [[ "$port" =~ ^[0-9]+$ && ${#port} -le 5 ]] || error "Invalid port."
    (( 10#$port >= 1 && 10#$port <= 65535 )) || error "Port must be 1-65535."
    check_root; detect_arch; check_dependencies
    [ -f "$INSTALL_BIN" ] && [ -f "$UNIT_DIR/$SERVER_SERVICE.service" ] || error "尚未安装主控，请先安装。"
    info "更新主控程序，保留现有账号、节点、配置、历史、备份及服务设置。"
    begin_update "$SERVER_SERVICE"
    download_binary
    switch_server_to_sqlite
    finish_update "$port"
}

validate_domain() {
    local domain="$1"
    local label='[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?'
    [[ ${#domain} -le 253 && "$domain" =~ ^${label}(\.${label})+$ ]] || error "请输入有效的域名（例如 monitor.example.com），不要包含协议或路径。"
    [[ ! "$domain" =~ ^[0-9]+(\.[0-9]+){3}$ ]] || error "请输入域名，不能使用 IP 地址。"
}

managed_proxy_domain() {
    [ -f "$NGINX_CONF" ] || return 0
    if [ "$(head -n 1 "$NGINX_CONF")" != '# Managed by VibeMonitor installer.' ]; then
        grep -Eq 'proxy_pass[[:space:]]+http://127\.0\.0\.1:[0-9]+;' "$NGINX_CONF" || return 0
    fi
    awk '$1 == "server_name" {sub(/;$/, "", $2); print $2; exit}' "$NGINX_CONF"
}

certificate_is_referenced() {
    local name="$1" reference="live/$1/" active_config dir matches status
    if command -v nginx >/dev/null 2>&1; then
        active_config=$(mktemp)
        if ! nginx -T > "$active_config" 2>&1; then
            rm -f "$active_config"
            warn "Nginx 配置无法验证，已取消删除。"
            return 0
        fi
        if grep -Fq -- "$reference" "$active_config"; then
            rm -f "$active_config"
            warn "该证书仍被 Nginx 当前配置引用，已取消删除。"
            return 0
        fi
        rm -f "$active_config"
    fi
    for dir in "${CERT_REFERENCE_DIRS[@]}"; do
        [ -d "$dir" ] || continue
        if matches=$(grep -r -F -l -- "$reference" "$dir" 2>/dev/null); then
            warn "该证书仍被 $matches 引用，已取消删除。"
            return 0
        else
            status=$?
            if [ "$status" != 1 ]; then
                warn "无法检查 $dir 中的证书引用，已取消删除。"
                return 0
            fi
        fi
    done
    return 1
}

delete_old_domain_certificate() {
    local current name file choice confirmation selected index=0
    local cert_names=()
    check_root
    command -v certbot >/dev/null 2>&1 || { warn "尚未安装 Certbot。"; return 0; }
    current=$(managed_proxy_domain)
    for file in "$CERTBOT_RENEWAL_DIR/"*.conf; do
        [ -f "$file" ] && [ ! -L "$file" ] || continue
        name=${file##*/}
        name=${name%.conf}
        [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || continue
        [ "$name" = "$current" ] && continue
        cert_names+=("$name")
    done
    if [ ${#cert_names[@]} -eq 0 ]; then
        info "没有可选择的旧证书；当前域名证书不会列出。"
        return 0
    fi
    info "本机 Certbot 的非当前域名证书（当前域名 ${current:-未配置} 已排除）："
    for name in "${cert_names[@]}"; do
        index=$((index + 1))
        printf '  %d. %s\n' "$index" "$name"
    done
    read_input "选择要删除的证书编号 [0 取消]: " choice
    [ "$choice" != 0 ] && [ -n "$choice" ] || return 0
    [[ "$choice" =~ ^[0-9]+$ ]] || { warn "请输入列表中的编号。"; return 0; }
    index=$((10#$choice - 1))
    [ "$index" -ge 0 ] && [ "$index" -lt ${#cert_names[@]} ] || { warn "请输入列表中的编号。"; return 0; }
    selected=${cert_names[$index]}
    [ "$selected" != "$(managed_proxy_domain)" ] || error "当前域名证书不能删除。"
    [ -f "$CERTBOT_RENEWAL_DIR/$selected.conf" ] || error "证书记录已变化，请重新打开菜单。"
    certbot certificates --cert-name "$selected" || error "无法读取所选证书信息。"
    certificate_is_referenced "$selected" && return 0
    warn "删除 $selected 后，其证书文件和 Certbot 续期记录无法自动恢复。"
    warn "仅自动检查 Nginx 和常见服务配置；请确认其他程序也没有使用此证书。"
    read_input "输入证书名称 $selected 确认删除（其他输入取消）: " confirmation
    [ "$confirmation" = "$selected" ] || { info "已取消删除。"; return 0; }
    [ "$selected" != "$(managed_proxy_domain)" ] || error "当前域名证书不能删除。"
    certificate_is_referenced "$selected" && return 0
    certbot delete --non-interactive --cert-name "$selected" || error "证书删除失败。"
    success "已删除旧证书：$selected"
}

installed_server_port() {
    local unit="$UNIT_DIR/$SERVER_SERVICE.service"
    [ -f "$unit" ] || return 0
    awk '/^ExecStart=/ {
        if (match($0, /--listen "(0\.0\.0\.0|127\.0\.0\.1):[0-9]+"/)) {
            value = substr($0, RSTART, RLENGTH)
            sub(/^.*:/, "", value)
            sub(/"$/, "", value)
            print value
            exit
        }
    }' "$unit"
}

install_proxy_dependencies() {
    local packages=()
    command -v nginx >/dev/null 2>&1 || packages+=(nginx)
    command -v certbot >/dev/null 2>&1 || packages+=(certbot)
    [ ${#packages[@]} -gt 0 ] || return 0
    info "正在安装 Nginx 和 Certbot…"
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y "${packages[@]}"
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y "${packages[@]}"
    elif command -v yum >/dev/null 2>&1; then
        yum install -y "${packages[@]}"
    else
        error "无法自动安装 Nginx 和 Certbot：不支持当前系统的包管理器。"
    fi
    command -v nginx >/dev/null 2>&1 && command -v certbot >/dev/null 2>&1 || error "Nginx 或 Certbot 安装失败。"
}

write_proxy_http_config() {
    local domain="$1"
    cat > "$NGINX_CONF" <<EOF
# Managed by VibeMonitor installer.
server {
    listen 80;
    server_name $domain;
    location ^~ /.well-known/acme-challenge/ {
        root $ACME_WEBROOT;
        default_type text/plain;
    }
    location / { return 503; }
}
EOF
}

append_proxy_http_config() {
    local domain="$1"
    cat >> "$NGINX_CONF" <<EOF
server {
    listen 80;
    server_name $domain;
    location ^~ /.well-known/acme-challenge/ {
        root $ACME_WEBROOT;
        default_type text/plain;
    }
    location / { return 503; }
}
EOF
}

write_proxy_https_config() {
    local domain="$1" port="$2"
    cat > "$NGINX_CONF" <<EOF
# Managed by VibeMonitor installer.
server {
    listen 80;
    server_name $domain;
    location ^~ /.well-known/acme-challenge/ {
        root $ACME_WEBROOT;
        default_type text/plain;
    }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl;
    server_name $domain;
    ssl_certificate /etc/letsencrypt/live/$domain/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$domain/privkey.pem;
    client_max_body_size 3m;
    location / {
        proxy_pass http://127.0.0.1:$port;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 90s;
    }
}
EOF
}

enable_certbot_renewal() {
    local certbot_bin
    if systemctl cat certbot.timer >/dev/null 2>&1; then
        systemctl enable --now certbot.timer || error "Certbot 自动续期定时器启动失败。"
    elif systemctl cat certbot-renew.timer >/dev/null 2>&1; then
        systemctl enable --now certbot-renew.timer || error "Certbot 自动续期定时器启动失败。"
    elif systemctl cat snap.certbot.renew.timer >/dev/null 2>&1; then
        systemctl enable --now snap.certbot.renew.timer || error "Certbot 自动续期定时器启动失败。"
    else
        [ ! -L "$RENEW_SERVICE" ] && [ ! -L "$RENEW_TIMER" ] || error "自动续期服务文件不能是符号链接。"
        if [ -e "$RENEW_SERVICE" ] || [ -e "$RENEW_TIMER" ]; then
            [ -f "$RENEW_SERVICE" ] && [ -f "$RENEW_TIMER" ] || error "自动续期服务文件不完整。"
            [ "$(head -n 1 "$RENEW_SERVICE")" = '# Managed by VibeMonitor installer.' ] || error "现有自动续期服务不是由安装脚本管理。"
            [ "$(head -n 1 "$RENEW_TIMER")" = '# Managed by VibeMonitor installer.' ] || error "现有自动续期定时器不是由安装脚本管理。"
        else
            certbot_bin=$(command -v certbot)
            cat > "$RENEW_SERVICE" <<EOF
# Managed by VibeMonitor installer.
[Unit]
Description=Renew VibeMonitor HTTPS certificate
[Service]
Type=oneshot
ExecStart=$(unit_arg "$certbot_bin") renew --quiet
EOF
            cat > "$RENEW_TIMER" <<'EOF'
# Managed by VibeMonitor installer.
[Unit]
Description=Check VibeMonitor HTTPS certificate renewal twice daily
[Timer]
OnCalendar=*-*-* 00,12:00:00
RandomizedDelaySec=3600
Persistent=true
[Install]
WantedBy=timers.target
EOF
            RENEW_TIMER_CREATED=1
        fi
        chmod 644 "$RENEW_SERVICE" "$RENEW_TIMER"
        systemctl daemon-reload
        systemctl enable --now vibemonitor-cert-renew.timer || error "自动续期定时器启动失败。"
    fi
}

configure_domain_proxy() {
    local domain="$1" port="$2" email="${3:-}" probe result old_domain
    local certbot_contact=(--register-unsafely-without-email)
    validate_domain "$domain"
    (( 10#$port != 80 && 10#$port != 443 )) || error "域名反代时，主控监听端口不能使用 80 或 443。"
    if [ -n "$email" ]; then
        [[ "$email" =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || error "证书通知邮箱格式不正确。"
        certbot_contact=(--email "$email" --no-eff-email)
    fi
    systemctl is-active --quiet nginx && PROXY_NGINX_WAS_ACTIVE=1
    systemctl is-enabled --quiet nginx && PROXY_NGINX_WAS_ENABLED=1
    install_proxy_dependencies
    mkdir -p "$(dirname "$NGINX_CONF")" "$ACME_WEBROOT/.well-known/acme-challenge"
    chmod 755 "$ACME_WEBROOT" "$ACME_WEBROOT/.well-known" "$ACME_WEBROOT/.well-known/acme-challenge"
    if command -v restorecon >/dev/null 2>&1; then restorecon -R "$ACME_WEBROOT" || true; fi
    [ ! -L "$NGINX_CONF" ] || error "Nginx 站点配置不能是符号链接：$NGINX_CONF"
    if [ -e "$NGINX_CONF" ]; then
        [ -f "$NGINX_CONF" ] || error "现有 Nginx 配置不是普通文件：$NGINX_CONF"
        if [ "$(head -n 1 "$NGINX_CONF")" != '# Managed by VibeMonitor installer.' ]; then
            grep -Eq 'proxy_pass[[:space:]]+http://127\.0\.0\.1:[0-9]+;' "$NGINX_CONF" || error "现有 Nginx 配置不是 VibeMonitor 反代：$NGINX_CONF"
            [ ! -e "$NGINX_CONF.before-vibemonitor.bak" ] || error "旧手工配置备份已存在，请先检查：$NGINX_CONF.before-vibemonitor.bak"
            cp -p "$NGINX_CONF" "$NGINX_CONF.before-vibemonitor.bak"
            info "已备份旧反代配置：$NGINX_CONF.before-vibemonitor.bak"
        fi
        cp -p "$NGINX_CONF" "$UPDATE_DIR/previous-nginx"
    fi
    old_domain=$(managed_proxy_domain)
    PROXY_CONFIG_TOUCHED=1
    if [ -n "$old_domain" ] && [ "$old_domain" = "$domain" ]; then
        : # Existing HTTP challenge location can be used without interrupting HTTPS.
    elif [ -n "$old_domain" ]; then
        append_proxy_http_config "$domain"
    else
        write_proxy_http_config "$domain"
    fi
    chmod 644 "$NGINX_CONF"
    nginx -t || error "Nginx 配置检查失败。"
    systemctl enable --now nginx || error "Nginx 启动失败；检查 80/443 端口占用。"
    systemctl reload nginx || error "Nginx 重载失败。"
    probe=$(mktemp "$ACME_WEBROOT/.well-known/acme-challenge/vibemonitor.XXXXXX")
    printf 'vibemonitor-check\n' > "$probe"
    chmod 644 "$probe"
    result=$(curl -4 -fsS --noproxy '*' --max-time 5 --resolve "$domain:80:127.0.0.1" "http://$domain/.well-known/acme-challenge/${probe##*/}") || result=''
    rm -f "$probe"
    [ "$result" = vibemonitor-check ] || error "域名的 HTTP 验证路径不可用；请检查 Nginx 站点和 80 端口。"
    info "正在为 $domain 申请 HTTPS 证书…"
    certbot certonly --webroot -w "$ACME_WEBROOT" -d "$domain" --cert-name "$domain" \
        --non-interactive --agree-tos "${certbot_contact[@]}" --keep-until-expiring || \
        error "证书申请失败。请确认域名已解析到本机，且公网 80 端口可访问。"
    write_proxy_https_config "$domain" "$port"
    nginx -t || error "HTTPS 配置检查失败。"
    systemctl reload nginx || error "HTTPS 配置重载失败。"
    curl -4 -sS --noproxy '*' --max-time 10 --resolve "$domain:443:127.0.0.1" -o /dev/null "https://$domain/" || \
        error "HTTPS 握手失败；请检查 443 端口和证书配置。"
    mkdir -p "$(dirname "$CERTBOT_DEPLOY_HOOK")"
    [ ! -L "$CERTBOT_DEPLOY_HOOK" ] || error "Certbot 续期钩子不能是符号链接：$CERTBOT_DEPLOY_HOOK"
    if [ -e "$CERTBOT_DEPLOY_HOOK" ]; then
        [ -f "$CERTBOT_DEPLOY_HOOK" ] && [ "$(head -n 2 "$CERTBOT_DEPLOY_HOOK" | tail -n 1)" = '# Managed by VibeMonitor installer.' ] || error "现有 Certbot 续期钩子不是由安装脚本管理：$CERTBOT_DEPLOY_HOOK"
        cp -p "$CERTBOT_DEPLOY_HOOK" "$UPDATE_DIR/previous-deploy-hook"
    fi
    PROXY_HOOK_TOUCHED=1
    cat > "$CERTBOT_DEPLOY_HOOK" <<'EOF'
#!/bin/sh
# Managed by VibeMonitor installer.
[ -f /etc/nginx/conf.d/vibemonitor.conf ] || exit 0
nginx -t && systemctl reload nginx
EOF
    chmod 755 "$CERTBOT_DEPLOY_HOOK"
    enable_certbot_renewal
}

install_server() {
    local port="${1:-1314}" password="${2:-}" username="${3:-}" domain="${4:-}" email="${5:-}"
    [[ "$username" =~ [^[:space:]] ]] || error "管理员账号不能为空，请填写 --username。"
    [[ "$password" =~ [^[:space:]] ]] || error "管理员密码不能为空，请填写 --password。"
    [[ "$port" =~ ^[0-9]+$ && ${#port} -le 5 ]] || error "Invalid port."
    (( 10#$port >= 1 && 10#$port <= 65535 )) || error "Port must be 1-65535."
    if [ -n "$domain" ]; then
        validate_domain "$domain"
        domain=$(printf '%s' "$domain" | tr '[:upper:]' '[:lower:]')
        (( 10#$port != 80 && 10#$port != 443 )) || error "域名反代时，主控监听端口不能使用 80 或 443。"
    elif [ -n "$email" ]; then
        error "提供证书通知邮箱时还需填写域名。"
    fi
    check_root; detect_arch; check_dependencies
    confirm_full_cleanup || return 0
    begin_update "$SERVER_SERVICE"
    download_binary
    if [ -n "$domain" ]; then configure_domain_proxy "$domain" "$port" "$email"; fi
    clear_server_data
    WAS_ACTIVE=0 # Deleted data cannot be recovered; do not restart old credentials on failure.
    mkdir -p "$CONFIG_DIR"
    rm -rf -- "$UNIT_DIR/$SERVER_SERVICE.service.d"
    local args
    if [ -n "$domain" ]; then
        args="$(unit_arg "$INSTALL_BIN") server --listen $(unit_arg "127.0.0.1:$port") --data $(unit_arg "$CONFIG_DIR/vibemonitor-data.db")"
    else
        args="$(unit_arg "$INSTALL_BIN") server --listen $(unit_arg "0.0.0.0:$port") --data $(unit_arg "$CONFIG_DIR/vibemonitor-data.db")"
    fi
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
    finish_update "$port" "$domain"
    if [ -n "$domain" ]; then success "访问地址：https://$domain"; fi
    info "Initial password, if generated: journalctl -u $SERVER_SERVICE -n 30"
}

configure_existing_server_domain() {
    local domain="$1" port="${2:-}" email="${3:-}" unit="$UNIT_DIR/$SERVER_SERVICE.service" override content old_listen new_listen old_domain
    validate_domain "$domain"
    domain=$(printf '%s' "$domain" | tr '[:upper:]' '[:lower:]')
    check_root; detect_arch; check_dependencies
    [ -f "$INSTALL_BIN" ] && [ -f "$unit" ] || error "尚未安装主控，请先安装。"
    if [ -z "$port" ]; then
        port=$(installed_server_port)
        [ -n "$port" ] || error "无法识别当前主控监听端口，请用 -p 指定。"
    fi
    [[ "$port" =~ ^[0-9]+$ && ${#port} -le 5 ]] || error "Invalid port."
    (( 10#$port >= 1 && 10#$port <= 65535 )) || error "Port must be 1-65535."
    (( 10#$port != 80 && 10#$port != 443 )) || error "域名反代时，主控监听端口不能使用 80 或 443。"
    for override in "$UNIT_DIR/$SERVER_SERVICE.service.d/"*.conf; do
        [ -f "$override" ] || continue
        if awk '/^ExecStart=/ {found=1} END {exit !found}' "$override"; then
            error "检测到自定义 ExecStart，请先检查 $override 的监听地址。"
        fi
    done
    old_listen="--listen $(unit_arg "0.0.0.0:$port")"
    new_listen="--listen $(unit_arg "127.0.0.1:$port")"
    content=$(cat "$unit")
    [[ "$content" == *"$old_listen"* || "$content" == *"$new_listen"* ]] || error "主控服务未使用预期的监听地址和端口，请检查 $unit。"
    old_domain=$(managed_proxy_domain)
    begin_update "$SERVER_SERVICE"
    configure_domain_proxy "$domain" "$port" "$email"
    if [[ "$content" == *"$old_listen"* ]]; then
        printf '%s\n' "${content/$old_listen/$new_listen}" > "$UPDATE_DIR/loopback-unit"
        chmod 600 "$UPDATE_DIR/loopback-unit"
        UNIT_TOUCHED=1
        mv -f "$UPDATE_DIR/loopback-unit" "$unit"
    fi
    finish_update "$port" "$domain"
    success "访问地址：https://$domain"
    if [ -n "$old_domain" ] && [ "$old_domain" != "$domain" ]; then
        info "旧域名 $old_domain 已从反代配置移除。请将各探针的主控地址改为 https://$domain。"
        info "旧证书仍由 Certbot 保留；确认不再使用后，可在管理菜单选择 12 清理。"
    fi
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
Environment=GOMEMLIMIT=25MiB
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
    mkdir -p "$CONFIG_DIR/backups"
    chmod 700 "$CONFIG_DIR/backups"
    systemctl is-active --quiet "$SERVER_SERVICE" && was_active=1
    if [ "$was_active" = 1 ]; then systemctl stop "$SERVER_SERVICE" || error "Could not stop server."; fi
    destination=$(mktemp "$CONFIG_DIR/backups/data-$(date +%Y%m%d-%H%M%S).XXXXXX") || result=1
    if [ "$result" = 0 ]; then
        mv "$destination" "$destination.db" || result=1
        destination="$destination.db"
    fi
    if [ "$result" = 0 ]; then
        "$INSTALL_BIN" export-data "$CONFIG_DIR/vibemonitor-data.db" "$destination" || result=1
    fi
    if [ "$was_active" = 1 ]; then systemctl start "$SERVER_SERVICE" || result=1; fi
    [ "$result" = 0 ] || error "Backup failed; check service status. Any partial output is at $destination."
    success "Backup saved: $destination"
}

restore_data() {
    check_root; detect_arch
    local source="$1" was_active=0 previous result=0
    "$INSTALL_BIN" validate-data "$source" || error "Invalid backup."
    mkdir -p "$CONFIG_DIR/backups"
    chmod 700 "$CONFIG_DIR/backups"
    systemctl is-active --quiet "$SERVER_SERVICE" && was_active=1
    if [ "$was_active" = 1 ]; then systemctl stop "$SERVER_SERVICE" || error "Could not stop server."; fi
    previous=$(mktemp "$CONFIG_DIR/backups/before-restore-XXXXXX") || result=1
    if [ "$result" = 0 ]; then
        mv "$previous" "$previous.db" || result=1
        previous="$previous.db"
    fi
    if [ "$result" = 0 ]; then
        "$INSTALL_BIN" export-data "$CONFIG_DIR/vibemonitor-data.db" "$previous" || result=1
    fi
    if [ "$result" != 0 ]; then
        if [ "$was_active" = 1 ]; then systemctl start "$SERVER_SERVICE" || true; fi
        error "Could not back up current database; restore cancelled."
    fi
    if ! "$INSTALL_BIN" restore-data "$source" "$CONFIG_DIR/vibemonitor-data.db"; then
        if [ "$was_active" = 1 ]; then systemctl start "$SERVER_SERVICE" || true; fi
        error "Restore transaction failed; previous database retained. Backup: $previous"
    fi
    if [ "$was_active" = 1 ]; then
        if ! systemctl start "$SERVER_SERVICE" || ! sleep 3 || ! systemctl is-active --quiet "$SERVER_SERVICE"; then
            systemctl stop "$SERVER_SERVICE" || error "Cannot stop failed service; recovery backup: $previous"
            if ! "$INSTALL_BIN" restore-data "$previous" "$CONFIG_DIR/vibemonitor-data.db"; then
                error "Rollback failed; recovery backup retained: $previous"
            fi
            systemctl start "$SERVER_SERVICE" || error "Previous database restored but service failed; backup: $previous"
            error "Restored database could not start; previous database restored. Backup: $previous"
        fi
    fi
    success "Data restored, including passwords, tokens and history. Previous backup: $previous"
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
remove_managed_proxy() {
    if [ -f "$NGINX_CONF" ] && [ "$(head -n 1 "$NGINX_CONF")" = '# Managed by VibeMonitor installer.' ]; then
        rm -f "$NGINX_CONF"
        if command -v nginx >/dev/null 2>&1 && nginx -t && systemctl is-active --quiet nginx; then
            systemctl reload nginx || warn "Nginx 重载失败，请检查站点配置。"
        fi
    fi
    if [ -f "$CERTBOT_DEPLOY_HOOK" ] && [ "$(head -n 2 "$CERTBOT_DEPLOY_HOOK" | tail -n 1)" = '# Managed by VibeMonitor installer.' ]; then
        rm -f "$CERTBOT_DEPLOY_HOOK"
    fi
    if [ -f "$RENEW_SERVICE" ] && [ "$(head -n 1 "$RENEW_SERVICE")" = '# Managed by VibeMonitor installer.' ] &&
       [ -f "$RENEW_TIMER" ] && [ "$(head -n 1 "$RENEW_TIMER")" = '# Managed by VibeMonitor installer.' ]; then
        systemctl stop vibemonitor-cert-renew.timer >/dev/null 2>&1 || true
        systemctl disable vibemonitor-cert-renew.timer >/dev/null 2>&1 || true
        rm -f "$RENEW_SERVICE" "$RENEW_TIMER"
        systemctl daemon-reload
    fi
}
uninstall_all() {
    check_root
    confirm_full_cleanup || return 0
    clear_server_data
    remove_managed_proxy
    for service in "$SERVER_SERVICE" "$AGENT_SERVICE" vibemonitor; do
        systemctl stop "$service" 2>/dev/null || true
        systemctl disable "$service" 2>/dev/null || true
        rm -f "$UNIT_DIR/$service.service"
        rm -rf -- "$UNIT_DIR/$service.service.d"
    done
    systemctl daemon-reload
    rm -f "$INSTALL_BIN"
    info "程序、配置、账号、数据和备份已全部删除。"
}

uninstall_agent() {
    check_root
    local confirmation
    read_input "将停止探针并彻底删除本机 Token、服务文件和程序，输入 yes 继续: " confirmation
    [ "$confirmation" = yes ] || return 0
    systemctl stop "$AGENT_SERVICE" 2>/dev/null || true
    systemctl disable "$AGENT_SERVICE" 2>/dev/null || true
    rm -f "$UNIT_DIR/$AGENT_SERVICE.service"
    systemctl daemon-reload
    if [ ! -f "$UNIT_DIR/$SERVER_SERVICE.service" ]; then
        rm -f "$INSTALL_BIN"
    fi
    success "本机探针 Token、服务文件和程序已彻底清理；服务端节点记录未修改。"
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
    local current_domain
    current_domain=$(managed_proxy_domain)
    printf '\n%s================================================%s\n' "$C_BLUE" "$C_RESET"
    printf '              VibeMonitor 管理面板\n'
    printf '================================================\n'
    printf '  支持系统  Linux (x86-64 / arm64) · IPv4\n'
    printf '  服务端    %s    |    探针  %s\n' "$(service_label "$SERVER_SERVICE")" "$(service_label "$AGENT_SERVICE")"
    printf '  访问域名  %s\n' "${current_domain:-未由脚本配置}"
    printf '%s------------------------------------------------%s\n' "$C_BLUE" "$C_RESET"
    printf '  主控管理\n    1. 安装 / 清空重装主控（删除全部数据）\n    2. 更新主控（保留数据）\n    3. 设置 / 更换访问域名与 HTTPS\n\n'
    printf '  探针管理\n    4. 安装 / 更新探针\n    5. 卸载本机探针\n\n'
    printf '  运行状态\n    6. 查看状态\n    7. 重启服务\n    8. 停止服务\n    9. 查看最近日志\n\n'
    printf '  数据与维护\n   10. 备份数据\n   11. 恢复备份\n   12. 删除旧域名证书\n   13. 彻底卸载（删除全部配置和数据）\n\n'
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
    local choice port port_default password username domain current_domain email server token interval source confirm pause
    while true; do
        menu_header
        read_input "请选择 [0-13]: " choice
        case "$choice" in
            # Run installations in a subshell so their EXIT rollback always runs,
            # even when the interactive menu is kept open afterwards.
            1) read_input "监听端口 [1314]: " port
               read_input "访问域名（留空则不自动配置 HTTPS）: " domain
               email=''
               if [ -n "$domain" ]; then read_input "证书通知邮箱（可留空）: " email; fi
               if [ -f "$CONFIG_DIR/vibemonitor-data.db" ]; then
                   info "重装会删除旧账号和全部数据，使用下面的新账号密码。"
               fi
               while true; do
                   read_input "管理员账号 [必填，仅一个管理员]: " username
                   [[ "$username" =~ [^[:space:]] ]] && break
                   warn "管理员账号不能为空，请重新输入。"
               done
               while true; do
                   read_secret "管理员密码 [必填，输入不显示]: " password
                   [[ "$password" =~ [^[:space:]] ]] && break
                   warn "管理员密码不能为空，请重新输入。"
               done
               ( install_server "${port:-1314}" "$password" "$username" "$domain" "$email" ) ;;
            2) port_default=$(installed_server_port)
               read_input "当前主控监听端口 [${port_default:-1314}]: " port
               ( update_server "${port:-${port_default:-1314}}" ) ;;
            3) current_domain=$(managed_proxy_domain)
               info "当前访问域名：${current_domain:-未配置}"
               read_input "新域名（留空取消）: " domain
               if [ -n "$domain" ]; then
                   read_input "证书通知邮箱（可留空）: " email
                   ( configure_existing_server_domain "$domain" "" "$email" )
               fi ;;
            4) read_input "主控地址（http:// 或 https://）: " server
               read_secret "节点 Token（输入不显示）: " token
               read_input "上报间隔 [3s]: " interval
               ( install_agent "$server" "$token" "${interval:-3s}" ) ;;
            5) uninstall_agent ;;
            6) show_status ;;
            7) manage_services restart ;;
            8) manage_services stop ;;
            9) journalctl --no-pager -u "$SERVER_SERVICE" -u "$AGENT_SERVICE" -n 50 ;;
            10) backup_data ;;
            11) read_input "备份主文件路径: " source
               read_input "将覆盖当前配置、密码和数据。输入 yes 继续: " confirm
               if [ "$confirm" = yes ]; then restore_data "$source"; fi ;;
            12) delete_old_domain_certificate ;;
            13) uninstall_all ;;
            0) return ;;
            *) warn "请输入 0 到 13 之间的菜单编号。" ;;
        esac
        read_input "按回车返回管理菜单…" pause
    done
}

# Sourcing is supported for isolated installer tests.
if [[ -n "${BASH_SOURCE[0]:-}" && "${BASH_SOURCE[0]}" != "$0" ]]; then return; fi
CMD="${1:-menu}"
if [ $# -gt 0 ]; then shift; fi
case "$CMD" in
    update|update-server)
        port=1314
        while [ $# -gt 0 ]; do
            case "$1" in
                -p|--port) port="${2:?missing port}"; shift 2 ;;
                *) error "Unknown update option: $1" ;;
            esac
        done
        update_server "$port" ;;
    server)
        port=1314; password=""; username=""; domain=""; email=""
        while [ $# -gt 0 ]; do
            case "$1" in
                -p|--port) port="${2:?missing port}"; shift 2 ;;
                -u|--username) username="${2:?missing username}"; shift 2 ;;
                -w|--password) password="${2:?missing password}"; shift 2 ;;
                -d|--domain) domain="${2:?missing domain}"; shift 2 ;;
                -e|--email) email="${2:?missing email}"; shift 2 ;;
                *) error "Unknown server option: $1" ;;
            esac
        done
        install_server "$port" "$password" "$username" "$domain" "$email" ;;
    domain)
        port=1314; domain=""; email=""
        while [ $# -gt 0 ]; do
            case "$1" in
                -p|--port) port="${2:?missing port}"; shift 2 ;;
                -d|--domain) domain="${2:?missing domain}"; shift 2 ;;
                -e|--email) email="${2:?missing email}"; shift 2 ;;
                *) error "Unknown domain option: $1" ;;
            esac
        done
        [ -n "$domain" ] || error "Domain is required."
        configure_existing_server_domain "$domain" "$port" "$email" ;;
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
    agent-uninstall|uninstall-agent) uninstall_agent ;;
    help|--help|-h) echo "Usage: $0 [update [-p PORT] | server [-p PORT] -u USER -w PASSWORD [-d DOMAIN] [-e EMAIL] | domain -d DOMAIN [-p PORT] [-e EMAIL] | agent -s URL -t TOKEN [-i INTERVAL] | agent-uninstall | backup | restore FILE | status | restart | uninstall]" ;;
    *) error "Unknown command: $CMD" ;;
esac

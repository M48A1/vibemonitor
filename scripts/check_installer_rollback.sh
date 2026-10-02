#!/usr/bin/env bash
# Isolated failure-path regression checks. No system service or network is used.
set -euo pipefail
PROJECT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

if [ "${1:-}" = --attempt ] || [ "${1:-}" = --invalid-args ]; then
    fixture="$2"; mode="$3"
    source "$PROJECT_DIR/install.sh"
    INSTALL_BIN="$fixture/vibemonitor"
    CONFIG_DIR="$fixture/config"
    UNIT_DIR="$fixture/units"
    check_root() { :; }
    detect_arch() { SYSTEM_ARCH=amd64; }
    check_dependencies() { :; }
    confirm_backup_cleanup() { return 0; }
    clear_backups() { :; }
    sleep() { :; }
    curl() { return 22; }
    systemctl() {
        printf '%s\n' "$*" >> "$fixture/operations"
        case "$1" in
            is-active)
                # Pretend the replacement failed its health check while still
                # running, so rollback must explicitly stop that process.
                if [ "$mode" != server-http-health ] && [ -e "$fixture/replacement-started" ] && grep -q 'new binary' "$INSTALL_BIN" 2>/dev/null; then return 1; fi
                [ "$(< "$fixture/active")" = 1 ] ;;
            is-enabled) [ "$(< "$fixture/enabled")" = 1 ] ;;
            stop) printf '0' > "$fixture/active" ;;
            restart|start)
                printf '1' > "$fixture/active"
                if grep -q 'new binary' "$INSTALL_BIN" 2>/dev/null; then touch "$fixture/replacement-started"; fi ;;
            enable) printf '1' > "$fixture/enabled" ;;
            disable) printf '0' > "$fixture/enabled" ;;
            daemon-reload) : ;;
            *) printf 'Unexpected systemctl operation: %s\n' "$*" >&2; return 1 ;;
        esac
    }
    download_binary() {
        printf '#!/usr/bin/env bash\n# new binary\nexit 0\n' > "$UPDATE_DIR/new-binary"
        chmod 755 "$UPDATE_DIR/new-binary"
        mv -f "$UPDATE_DIR/new-binary" "$INSTALL_BIN"
        BINARY_REPLACED=1
    }
    if [ "$1" = --invalid-args ]; then
        # Any call beyond input validation would be too late: reinstalling a
        # server can erase its database, and agent installs clear old backups.
        check_root() { touch "$fixture/side-effect"; }
        confirm_full_cleanup() { touch "$fixture/side-effect"; return 0; }
        confirm_backup_cleanup() { touch "$fixture/side-effect"; return 0; }
        begin_update() { touch "$fixture/side-effect"; exit 90; }
        clear_server_data() { touch "$fixture/side-effect"; exit 90; }
        clear_backups() { touch "$fixture/side-effect"; exit 90; }
        separator=$'\n'
        if [ "$4" = CR ]; then separator=$'\r'; fi
        bad_value="good${separator}bad"
        case "$mode" in
            server-password) install_server 1314 "$bad_value" admin ;;
            server-username) install_server 1314 password "$bad_value" ;;
            agent-server) install_agent "https://example.test/${bad_value}" token 1s ;;
            agent-token) install_agent https://example.test "$bad_value" 1s ;;
            agent-interval) install_agent https://example.test token "$bad_value" ;;
            *) exit 91 ;;
        esac
        exit 0
    fi
    if [ "$mode" = unit-write ]; then
        cat() { return 1; }
    elif [ "$mode" = sqlite-migration ]; then
        # Fail between the migration rename and finish_update so only the
        # migration's own UNIT_TOUCHED marker can recover the old argument.
        finish_update() { return 1; }
    elif [ "$mode" = restore-failure ]; then
        mv() {
            if [[ "$*" == *restore-binary* ]]; then return 1; fi
            command mv "$@"
        }
    fi
    if [ "$mode" = sqlite-migration ] || [ "$mode" = server-http-health ]; then
        update_server 1314
    else
        install_agent https://new.example new-token 1s
    fi
    exit 0
fi

TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/vibemonitor-installer-check.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT
checks=0
for mode in unit-write health-check sqlite-migration server-http-health restore-failure first-install; do
    for active in 0 1; do
        for enabled in 0 1; do
            if [ "$mode" = first-install ] && { [ "$active" != 0 ] || [ "$enabled" != 0 ]; }; then continue; fi
            fixture="$TEST_DIR/$mode-$active-$enabled"
            mkdir -p "$fixture/config" "$fixture/units"
            printf '%s' "$active" > "$fixture/active"
            printf '%s' "$enabled" > "$fixture/enabled"
            if [ "$mode" != first-install ]; then
                printf '#!/usr/bin/env bash\n# old binary\nexit 0\n' > "$fixture/vibemonitor"
                chmod 755 "$fixture/vibemonitor"
                cp -p "$fixture/vibemonitor" "$fixture/expected-binary"
                service=vibemonitor-agent
                if [ "$mode" = sqlite-migration ] || [ "$mode" = server-http-health ]; then service=vibemonitor-server; fi
                printf '[Unit]\nDescription=Previous service\n[Service]\nExecStart="%s" server --data "%s/vibemonitor-data.json"\n' "$fixture/vibemonitor" "$fixture/config" > "$fixture/units/$service.service"
                chmod 640 "$fixture/units/$service.service"
                cp -p "$fixture/units/$service.service" "$fixture/expected-unit"
                if [ "$mode" = sqlite-migration ] || [ "$mode" = server-http-health ]; then touch "$fixture/config/vibemonitor-data.db"; fi
            fi
            set +e
            bash "${BASH_SOURCE[0]}" --attempt "$fixture" "$mode" > "$fixture/output" 2>&1
            result=$?
            set -e
            [ "$result" != 0 ] || { printf 'Failure was not detected: %s\n' "$fixture" >&2; exit 1; }
            if [ "$mode" = first-install ]; then
                [ ! -e "$fixture/vibemonitor" ] && [ ! -e "$fixture/units/vibemonitor-agent.service" ]
                [ "$(< "$fixture/active")" = 0 ] && [ "$(< "$fixture/enabled")" = 0 ]
            else
                cmp "$fixture/expected-unit" "$fixture/units/$service.service"
                [ "$(< "$fixture/enabled")" = "$enabled" ]
                if [ "$mode" = restore-failure ]; then
                    [ "$(< "$fixture/active")" = 0 ]
                    recovery=("$fixture/vibemonitor.update."*)
                    [ ${#recovery[@]} = 1 ] && [ -f "${recovery[0]}/previous-binary" ]
                    cmp "$fixture/expected-binary" "${recovery[0]}/previous-binary"
                else
                    cmp "$fixture/expected-binary" "$fixture/vibemonitor"
                    [ "$(< "$fixture/active")" = "$active" ]
                    # cp -p restores permissions as well as content.
                    [ -n "$(find "$fixture/units/$service.service" -prune -perm 0640 -print)" ]
                fi
            fi
            checks=$((checks + 1))
        done
    done
done
printf 'Installer rollback checks passed: %s failure paths\n' "$checks"
input_checks=0
for field in server-password server-username agent-server agent-token agent-interval; do
    for separator in LF CR; do
        fixture="$TEST_DIR/input-$field-$separator"
        mkdir -p "$fixture/config" "$fixture/units"
        printf 'previous binary' > "$fixture/vibemonitor"
        printf 'previous server unit' > "$fixture/units/vibemonitor-server.service"
        printf 'previous agent unit' > "$fixture/units/vibemonitor-agent.service"
        printf 'previous database' > "$fixture/config/vibemonitor-data.db"
        cp -p "$fixture/vibemonitor" "$fixture/expected-binary"
        cp -p "$fixture/units/vibemonitor-server.service" "$fixture/expected-server"
        cp -p "$fixture/units/vibemonitor-agent.service" "$fixture/expected-agent"
        cp -p "$fixture/config/vibemonitor-data.db" "$fixture/expected-data"
        set +e
        bash "${BASH_SOURCE[0]}" --invalid-args "$fixture" "$field" "$separator" > "$fixture/output" 2>&1
        result=$?
        set -e
        [ "$result" = 1 ] && [ ! -e "$fixture/side-effect" ]
        grep -q 'Arguments cannot contain line breaks' "$fixture/output"
        cmp "$fixture/expected-binary" "$fixture/vibemonitor"
        cmp "$fixture/expected-server" "$fixture/units/vibemonitor-server.service"
        cmp "$fixture/expected-agent" "$fixture/units/vibemonitor-agent.service"
        cmp "$fixture/expected-data" "$fixture/config/vibemonitor-data.db"
        input_checks=$((input_checks + 1))
    done
done
printf 'Installer preflight checks passed: %s CR/LF inputs rejected before changes\n' "$input_checks"

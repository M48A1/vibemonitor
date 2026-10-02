#!/usr/bin/env bash
# Exercise delayed readiness and startup failures without services or networking.
set -euo pipefail
PROJECT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/vibemonitor-health-check.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT

for mode in delayed wrong-body stopped timeout https; do
    fixture="$TEST_DIR/$mode"
    mkdir -p "$fixture"
    if (
        source "$PROJECT_DIR/install.sh"
        UPDATE_SERVICE=vibemonitor-server
        SECONDS=0
        sleep() { SECONDS=$((SECONDS + $1)); }
        systemctl() {
            [ "$*" = 'is-active --quiet vibemonitor-server' ]
            [ "$mode" != stopped ]
        }
        curl() {
            local calls=0
            if [ -f "$fixture/calls" ]; then calls=$(< "$fixture/calls"); fi
            calls=$((calls + 1))
            printf '%s' "$calls" > "$fixture/calls"
            printf '%s\n' "$*" >> "$fixture/requests"
            case "$mode" in
                delayed) [ "$calls" -ge 4 ] || return 7 ;;
                wrong-body) if [ "$calls" -lt 3 ]; then printf 'not ready'; return 0; fi ;;
                timeout) return 7 ;;
            esac
            printf pong
        }
        if [ "$mode" = https ]; then
            wait_for_http_health https://example.test/ping --resolve example.test:443:127.0.0.1
        else
            wait_for_http_health http://127.0.0.1:1314/ping
        fi
    ) > "$fixture/output" 2>&1; then
        case "$mode" in
            delayed) [ "$(< "$fixture/calls")" = 4 ] ;;
            wrong-body) [ "$(< "$fixture/calls")" = 3 ] ;;
            https) grep -q -- '--resolve example.test:443:127.0.0.1 https://example.test/ping' "$fixture/requests" ;;
            *) printf 'Expected failure: %s\n' "$mode" >&2; exit 1 ;;
        esac
    else
        case "$mode" in
            stopped) [ ! -f "$fixture/calls" ]; grep -q '服务未保持运行' "$fixture/output" ;;
            timeout) [ "$(< "$fixture/calls")" = 60 ]; grep -q '健康检查超时' "$fixture/output" || { cat "$fixture/output" >&2; exit 1; } ;;
            *) cat "$fixture/output" >&2; exit 1 ;;
        esac
        grep -q 'journalctl -u vibemonitor-server' "$fixture/output"
    fi
done
printf 'Installer health checks passed: delayed readiness, response validation, stopped service, timeout, HTTPS\n'

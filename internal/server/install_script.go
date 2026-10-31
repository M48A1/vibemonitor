package server

import (
	"encoding/hex"
	"net/http"
	"net/netip"
	"strings"

	"vibemonitor/internal/store"
)

func HandleInstallScript(s *store.Store, trustedProxies ...netip.Prefix) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		register := r.URL.Query().Get("register")
		if token == "" && register == "" {
			http.Error(w, "Error: missing token in URL, e.g. /install.sh?token=YOUR_TOKEN", http.StatusBadRequest)
			return
		}
		// The flag the embedded script hands to install.sh: a node token that
		// already exists, or a registration key that creates one on the machine
		// being installed. A key is only checked against the open window when the
		// agent uses it, so here it merely has to be shaped like one.
		flag, credential := "-t", token
		if register != "" {
			if len(register) != 32 {
				http.Error(w, "Invalid registration key", http.StatusUnauthorized)
				return
			}
			if _, err := hex.DecodeString(register); err != nil {
				http.Error(w, "Invalid registration key", http.StatusUnauthorized)
				return
			}
			flag, credential = "-r", register
		} else if _, err := hex.DecodeString(token); err != nil || s.FindNodeByToken(token) == nil {
			http.Error(w, "Invalid node token", http.StatusUnauthorized)
			return
		}

		scheme := "http"
		if requestHTTPS(r, trustedProxies...) {
			scheme = "https"
		}
		host := r.Host
		if host == "" || strings.ContainsAny(host, "\"'`$\\\r\n\t ;%/?#@") {
			http.Error(w, "Invalid server host", http.StatusBadRequest)
			return
		}
		serverURL := scheme + "://" + host

		script := strings.NewReplacer("@SERVER@", serverURL, "@CREDENTIAL_FLAG@", flag, "@CREDENTIAL@", credential).Replace(`#!/usr/bin/env bash
set -e
SERVER_URL="@SERVER@"
CREDENTIAL_FLAG="@CREDENTIAL_FLAG@"
CREDENTIAL="@CREDENTIAL@"
if [ "$#" -gt 1 ]; then echo "Usage: bash -s -- [agent|agent-uninstall]" >&2; exit 1; fi
case "${1-agent}" in
    agent) ACTION=agent ;;
    agent-uninstall|uninstall-agent) ACTION=agent-uninstall ;;
    *) echo "Unknown action: $1" >&2; exit 1 ;;
esac
if [ "$(id -u)" != 0 ]; then echo "Run as root."; exit 1; fi
if [ "$(uname -s)" != Linux ]; then echo "Only Linux (x86-64 / arm64) is supported."; exit 1; fi
case "$(uname -m)" in
    x86_64|amd64|aarch64|arm64) ;;
    *) echo "Only Linux (x86-64 / arm64) is supported."; exit 1 ;;
esac
# Download and verify the installer before either operation.
for cmd in curl sha256sum awk; do command -v "$cmd" >/dev/null || { echo "Missing $cmd"; exit 1; }; done
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT
EFFECTIVE=$(curl -4 -fsSL --connect-timeout 10 --max-time 60 -o /dev/null -w '%{url_effective}' https://github.com/M48A1/vibemonitor/releases/latest)
TAG=${EFFECTIVE##*/}
[[ "$EFFECTIVE" == https://github.com/*/releases/tag/* && "$TAG" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "Could not resolve release."; exit 1; }
BASE="https://github.com/M48A1/vibemonitor/releases/download/$TAG"
curl -4 -fsSL --connect-timeout 10 --max-time 60 -o "$TMP_DIR/install.sh" "$BASE/install.sh"
curl -4 -fsSL --connect-timeout 10 --max-time 60 -o "$TMP_DIR/sha256sums.txt" "$BASE/sha256sums.txt"
EXPECTED=$(awk '$2 == "install.sh" {print $1}' "$TMP_DIR/sha256sums.txt")
[[ "$EXPECTED" =~ ^[[:xdigit:]]{64}$ ]] || { echo "Missing installer checksum."; exit 1; }
printf '%s  %s\n' "$EXPECTED" "$TMP_DIR/install.sh" | sha256sum -c - >/dev/null
# Use the verified installer's own installation and uninstall confirmation flows.
if [ "$ACTION" = agent-uninstall ]; then
    bash "$TMP_DIR/install.sh" agent-uninstall
else
    bash "$TMP_DIR/install.sh" agent -s "$SERVER_URL" "$CREDENTIAL_FLAG" "$CREDENTIAL"
fi
`)

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		_, _ = w.Write([]byte(strings.ReplaceAll(script, "\r\n", "\n")))
	}
}

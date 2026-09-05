package server

import (
	"encoding/hex"
	"net/http"
	"strings"

	"vibemonitor/internal/store"
)

func HandleInstallScript(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, "Error: missing token in URL, e.g. /install.sh?token=YOUR_TOKEN", http.StatusBadRequest)
			return
		}
		if _, err := hex.DecodeString(token); err != nil || s.FindNodeByToken(token) == nil {
			http.Error(w, "Invalid node token", http.StatusUnauthorized)
			return
		}

		scheme := "http"
		if requestHTTPS(r) {
			scheme = "https"
		}
		host := r.Host
		if host == "" || strings.ContainsAny(host, "\"'`$\\\r\n\t ;%/?#@") {
			http.Error(w, "Invalid server host", http.StatusBadRequest)
			return
		}
		serverURL := scheme + "://" + host

		script := strings.NewReplacer("@SERVER@", serverURL, "@TOKEN@", token).Replace(`#!/usr/bin/env bash
set -e
SERVER_URL="@SERVER@"
TOKEN="@TOKEN@"
if [ "$(id -u)" != 0 ]; then echo "Run as root."; exit 1; fi
if [ "$(uname -s)" != Linux ]; then echo "Only Linux x86-64 is supported."; exit 1; fi
case "$(uname -m)" in
    x86_64|amd64) ;;
    *) echo "Only x86-64 (Intel/AMD 64-bit) is supported."; exit 1 ;;
esac
# Installation begins here
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
# Reuse the same verified installer, atomic update, and rollback for both entry points.
bash "$TMP_DIR/install.sh" agent -s "$SERVER_URL" -t "$TOKEN"
`)

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		_, _ = w.Write([]byte(strings.ReplaceAll(script, "\r\n", "\n")))
	}
}

package server

import (
	"fmt"
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

		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		host := r.Host
		serverURL := fmt.Sprintf("%s://%s", scheme, host)

		script := fmt.Sprintf(`#!/usr/bin/env bash
# VibeMonitor Agent One-Line Installer
set -e

SERVER_URL="%s"
TOKEN="%s"
INSTALL_DIR="/opt/vibemonitor"
SERVICE_NAME="vibemonitor"

echo "============================================="
echo "   ⚡ Installing VibeMonitor Agent...        "
echo "============================================="

# Check root
if [ "$(id -u)" != "0" ]; then
    echo "[-] Error: Please run this script as root (e.g. sudo bash)."
    exit 1
fi

# Detect Architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)
        BINARY_ARCH="amd64"
        ;;
    aarch64|arm64)
        BINARY_ARCH="arm64"
        ;;
    armv7l|armhf)
        BINARY_ARCH="arm"
        ;;
    *)
        echo "[-] Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

mkdir -p "$INSTALL_DIR"

echo "[*] Downloading vibemonitor binary for $BINARY_ARCH..."
# If server provides precompiled binaries
DOWNLOAD_URL="${SERVER_URL}/download/vibemonitor-linux-${BINARY_ARCH}"
if curl -fsSL -o "$INSTALL_DIR/vibemonitor" "$DOWNLOAD_URL"; then
    chmod +x "$INSTALL_DIR/vibemonitor"
    echo "[+] Downloaded binary successfully."
else
    echo "[!] Warning: Server download endpoint not present, checking local /usr/local/bin/vibemonitor..."
    if [ -f "/usr/local/bin/vibemonitor" ]; then
        cp "/usr/local/bin/vibemonitor" "$INSTALL_DIR/vibemonitor"
        chmod +x "$INSTALL_DIR/vibemonitor"
    else
        echo "[-] Please ensure vibemonitor binary is placed at $INSTALL_DIR/vibemonitor"
        exit 1
    fi
fi

echo "[*] Creating systemd service..."
cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=VibeMonitor Server Monitor Agent
After=network.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/vibemonitor agent --server ${SERVER_URL} --token ${TOKEN}
Restart=always
RestartSec=5
KillMode=process

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"
systemctl restart "${SERVICE_NAME}"

echo "============================================="
echo "   ✅ VibeMonitor Agent successfully started! "
echo "   Check status: systemctl status ${SERVICE_NAME}"
echo "============================================="
`, serverURL, token)

		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		_, _ = w.Write([]byte(strings.ReplaceAll(script, "\r\n", "\n")))
	}
}

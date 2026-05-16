#!/bin/bash
set -e

INSTALL_DIR="/opt/spp"
SERVICE_FILE="/etc/systemd/system/spp.service"
SERVICE_NAME="spp"

echo "==> Stopping and disabling SPP service..."
if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  sudo systemctl stop "$SERVICE_NAME"
fi
if systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
  sudo systemctl disable "$SERVICE_NAME"
fi

echo "==> Removing systemd service..."
if [ -f "$SERVICE_FILE" ]; then
  sudo rm -f "$SERVICE_FILE"
  sudo systemctl daemon-reload
  sudo systemctl reset-failed "$SERVICE_NAME" 2>/dev/null || true
fi

echo "==> Removing installed files..."
if [ -d "$INSTALL_DIR" ]; then
  read -r -p "Remove config and data in $INSTALL_DIR? [y/N] " confirm
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    sudo rm -rf "$INSTALL_DIR"
    echo "    Removed $INSTALL_DIR"
  else
    sudo rm -f "$INSTALL_DIR/spp"
    echo "    Kept $INSTALL_DIR (config/data preserved), removed binary only."
  fi
fi

echo ""
echo "SPP has been uninstalled."

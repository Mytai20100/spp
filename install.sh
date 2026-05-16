#!/bin/bash
set -e

INSTALL_DIR="/opt/spp"
SERVICE_FILE="/etc/systemd/system/spp.service"

echo "==> Building SPP..."
go build -o spp .

echo "==> Installing to $INSTALL_DIR..."
sudo mkdir -p "$INSTALL_DIR"
sudo cp spp "$INSTALL_DIR/spp"
sudo chmod +x "$INSTALL_DIR/spp"

echo "==> Installing systemd service..."
sudo cp spp.service "$SERVICE_FILE"
sudo systemctl daemon-reload
sudo systemctl enable spp

echo ""
echo "Done. Start with:"
echo "  sudo systemctl start spp"
echo "  # then open http://localhost:8080"

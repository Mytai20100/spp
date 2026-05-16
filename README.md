# SPP — Service Process Panel

A minimal, self-hosted process management panel with a modern web UI.
No Docker. No heavy dependencies. One binary.

---

## Features

- **Process management** — add, start, stop, restart any command
- **Live terminal** — real-time stdout/stderr per process via SSE
- **stdin input** — send commands to running processes from the browser
- **Auto-restart** — on crash, or always (survives machine reboots)
- **System metrics** — CPU, RAM, network RX/TX rates, uptime, load avg
- **Disk usage** — scans the process working directory
- **Boot recovery** — processes set to `always` restart automatically on launch

---

## Build

Requirements: Go 1.22+

```bash
go build -o spp .
```

Or cross-compile:
```bash
GOOS=linux GOARCH=amd64 go build -o spp-linux-amd64 .
```

---

## Run

```bash
./spp
# Listens on http://localhost:8080
```

Options:
```
-port  int     HTTP server port (default 8080)
-config string Config file path (default spp.json)
```

Example:
```bash
./spp -port 9090 -config /etc/spp/config.json
```

---

## Boot Auto-Start (systemd)

Create `/etc/systemd/system/spp.service`:

```ini
[Unit]
Description=SPP Service Process Panel
After=network.target

[Service]
Type=simple
User=youruser
WorkingDirectory=/opt/spp
ExecStart=/opt/spp/spp -config /opt/spp/spp.json
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

Enable:
```bash
sudo systemctl daemon-reload
sudo systemctl enable spp
sudo systemctl start spp
```

When the machine boots, SPP starts, and then SPP auto-starts any processes
configured with `auto_restart: always`.

---

## Config File

SPP auto-creates `spp.json` on first run. Example:

```json
{
  "processes": [
    {
      "id": "1234567890",
      "name": "My Node App",
      "command": "/usr/bin/node",
      "args": ["server.js", "--port", "3000"],
      "work_dir": "/home/user/myapp",
      "auto_restart": "always",
      "env": ["NODE_ENV=production", "PORT=3000"],
      "created_at": "2024-01-01T00:00:00Z"
    }
  ],
  "server_port": 8080
}
```

`auto_restart` values:
- `never` — manual start only
- `on_crash` — restart only if the process exits with an error
- `always` — restart on crash AND on SPP startup (boot recovery)

---

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/processes` | List all processes + status |
| POST | `/api/processes` | Create process |
| PUT | `/api/processes/:id` | Update process config |
| DELETE | `/api/processes/:id` | Delete process |
| POST | `/api/processes/:id/start` | Start process |
| POST | `/api/processes/:id/stop` | Stop process |
| POST | `/api/processes/:id/restart` | Restart process |
| GET | `/api/processes/logs/:id` | Get log history (JSON) |
| GET | `/api/processes/logs/stream/:id` | Live log stream (SSE) |
| POST | `/api/processes/stdin/:id` | Send stdin input |
| GET | `/api/processes/disk/:id` | Get disk usage of workdir |
| GET | `/api/metrics` | Current system metrics |
| GET | `/api/metrics/stream` | Live metrics stream (SSE) |

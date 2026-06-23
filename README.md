# NSSH - SSH Reverse Tunnel Client

NSSH is a lightweight SSH reverse tunnel client written in Go. It allows you to expose local services behind NAT or firewalls to the public internet through an SSH server.

## Features

- **SSH Reverse Tunnel** (`ssh -R` equivalent)
- **Auto Reconnect** — configurable delay, automatically reconnects on disconnection
- **Heartbeat** — periodic keepalive to detect connection drops
- **Daemon Mode** — run in background as a daemon managing multiple tunnels
- **Multiple Auth Methods** — password and SSH key authentication
- **Cross-platform** — Linux, macOS, Windows, FreeBSD
- **Minimal Footprint** — small binary size, low memory usage

## Quick Start

### Build

```bash
go build -o nssh main.go
```

### Run a Tunnel

```bash
# Password authentication
nssh -R 8080:localhost:80 user@your-server.com -p 22 --passwd your_password

# SSH key authentication  
nssh -R 8080:localhost:80 user@your-server.com -p 22 -i ~/.ssh/id_rsa
```

This creates a reverse tunnel: `your-server.com:8080 → localhost:80`

### Daemon Mode (Background)

```bash
nssh --daemon -R 8080:localhost:80 user@your-server.com -p 22 --passwd your_password
```

### Manage Tunnels

```bash
nssh --list                    # List all running tunnels
nssh --stop user@server        # Stop a specific tunnel
nssh --stop-all                # Stop all tunnels
nssh --restart user@server     # Restart a specific tunnel
nssh --log user@server         # View tunnel logs
```

## Installation

### From Source

```bash
git clone https://github.com/Vrolist/nssh.git
cd nssh
go build -o nssh main.go
sudo cp nssh /usr/local/bin/
```

### Docker

```bash
docker build -t nssh -f docker/Dockerfile .
docker run -d --name nssh_tunnel --restart=unless-stopped \
  -e REMOTE_PORT=8080 \
  -e LOCAL_HOST=127.0.0.1 \
  -e LOCAL_PORT=80 \
  -e USERNAME=user \
  -e SERVER_NODE=your-server.com \
  -e SERVER_PORT=22 \
  -e PASSWORD=your_password \
  nssh
```

## Configuration

### CLI Arguments

| Argument | Short | Description |
|----------|-------|-------------|
| `--remote` | `-R` | Reverse tunnel: `remote_port:local_host:local_port` |
| `--port` | `-p` | SSH server port |
| `--passwd` | `-P` | SSH password |
| `--identity` | `-i` | SSH private key path |
| `--reconnect` | `-r` | Reconnect delay in seconds (default: 30) |
| `--daemon` | | Run as background daemon |
| `--list` | | List all running tunnels |
| `--stop` | | Stop a tunnel by username |
| `--stop-all` | | Stop all tunnels |
| `--restart` | | Restart a tunnel |
| `--log` | | View tunnel logs |
| `--version` | `-v` | Show version |

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SERVER_HOST` | SSH server address | - |
| `SERVER_PORT` | SSH server port | `20022` |
| `USERNAME` | SSH username | - |
| `PASSWORD` | SSH password | - |
| `SSH_KEY` | SSH private key path | - |
| `LOCAL_PORT` | Local service port | `80` |
| `REMOTE_PORT` | Remote listener port | `8000` |
| `RECONNECT_DELAY` | Reconnect delay (seconds) | `30` |

## Architecture

```
┌──────────────────────────────────────────────┐
│  CLI (main.go)                                │
│  Parses arguments → dispatches to daemon      │
└──────────────────────┬───────────────────────┘
                       │ Unix socket / named pipe
┌──────────────────────▼───────────────────────┐
│  Daemon (daemon/)                             │
│  Background process, manages multiple workers │
│  - Start/stop/restart tunnels                 │
│  - Status queries, log viewing                │
│  - Auto-reconnect on failure                  │
└──────────────────────┬───────────────────────┘
                       │ Spawns child processes
         ┌─────────────┼─────────────┐
         ▼             ▼             ▼
    Worker 1        Worker 2     Worker N
  (user@svr:8080) (user@svr:8081)
```

## Build Matrix

```bash
# Build for current platform
go build -o nssh main.go

# Linux
GOOS=linux GOARCH=amd64 go build -o nssh_linux_amd64 main.go
GOOS=linux GOARCH=arm64 go build -o nssh_linux_arm64 main.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o nssh_darwin_amd64 main.go
GOOS=darwin GOARCH=arm64 go build -o nssh_darwin_arm64 main.go

# Windows
GOOS=windows GOARCH=amd64 go build -o nssh_windows_amd64.exe main.go

# FreeBSD
GOOS=freebsd GOARCH=amd64 go build -o nssh_freebsd_amd64 main.go
```

## License

MIT

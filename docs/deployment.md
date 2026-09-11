# Deployment

This document describes how to deploy the application with Docker (recommended) or as a standalone binary.

## Deployment with Docker Compose (Recommended)

Docker Compose provides container isolation, automatic restarts, and persistent storage.

### Prerequisites
- Docker Engine installed.
- Docker Compose plugin installed.

### Setup Steps

1. Clone the project repository to your host:
   ```bash
   git clone <repository-url>
   cd ethold
   ```

2. Create your `.env` configuration file:
   ```bash
   cp .env.example .env
   ```

3. Edit `.env` with your actual PENS credentials and optional Telegram tokens.

4. Start the container in detached mode:
   ```bash
   docker compose up -d
   ```

5. Check the application logs:
   ```bash
   docker compose logs -f
   ```

### Docker Compose Architecture

The `docker-compose.yml` file defines:
- **Build Context:** Builds from the local `Dockerfile`.
- **Environment:** Reads from optional `.env` file or directly defined `environment:` variables.
- **Volumes:** Creates a named volume `state` mounted at `/app/data/`.
- **Command:** Passes `--state /app/data/attended_keys.json` to store records on persistent storage.
- **Restart Policy:** `unless-stopped` restarts the service after server reboot.

---

## Standalone Binary Deployment

You can run the application directly on a Linux server without Docker.

### Build the Binary

Compile a stripped binary with disabled CGO:
```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o ethold ./cmd/ethold
```

### Running the Binary

Run directly with flags or environment variables:
```bash
./ethold --config .env --state attended_keys.json
```

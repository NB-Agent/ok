# OK — One-Click Deploy

This directory contains everything needed to run OK as a containerized web service.

## Quick Start

```bash
docker-compose up -d
```

Open http://localhost:8080 in your browser.

## Architecture

```
┌──────────┐     ┌──────────────┐     ┌──────────────┐
│  Browser  │────▶│   NGINX      │────▶│  OK Server   │
│  (SSE)    │     │   (reverse   │     │  (serve)     │
│           │◀────│    proxy)    │◀────│              │
└──────────┘     └──────────────┘     └──────────────┘
```

## Configuration

1. Copy `.env.example` to `.env` and set your API keys:

```bash
cp .env.example .env
```

2. Edit `.env` with your preferred provider keys.

## Production Deployment

For production use, add HTTPS with Let's Encrypt behind the NGINX proxy:

```nginx
# In nginx/ok.conf — uncomment the SSL section
server {
    listen 443 ssl;
    server_name ok.yourdomain.com;
    ssl_certificate /etc/letsencrypt/live/ok.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/ok.yourdomain.com/privkey.pem;
}
```

## Volumes

- `ok-data`: Session persistence and memory storage
- Logs are written to stdout/stderr (captured by docker logs)

## Stopping

```bash
docker-compose down
```

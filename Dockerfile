# syntax=docker/dockerfile:1

# ── Build stage ────────────────────────────────
FROM golang:1.25-alpine AS build

RUN apk add --no-cache git gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bin/ok ./cmd/ok
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bin/ok-plugin-example ./cmd/ok-plugin-example

# ── Tree-sitter variant (needs CGo) ────────────
FROM golang:1.25-alpine AS build-ts

RUN apk add --no-cache git gcc musl-dev tree-sitter-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -tags=treesitter -trimpath -ldflags="-s -w" -o /bin/ok-ts ./cmd/ok

# ── Runtime ─────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -h /home/ok ok

USER ok
WORKDIR /home/ok

COPY --from=build /bin/ok /usr/local/bin/ok
COPY --from=build /bin/ok-plugin-example /usr/local/bin/ok-plugin-example

ENV OK_HOME=/home/ok/.config/ok

ENTRYPOINT ["ok"]
CMD ["--help"]

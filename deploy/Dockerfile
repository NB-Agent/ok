# Build stage
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ok-server ./cmd/ok

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -h /home/ok ok
WORKDIR /home/ok
COPY --from=builder /build/ok-server /usr/local/bin/ok
COPY --from=builder /build/deploy/nginx/ /etc/nginx/conf.d/
COPY --from=builder /build/internal/serve/index.html /home/ok/
USER ok
EXPOSE 8080
ENV OK_PORT=8080
ENV OK_HOST=0.0.0.0
ENTRYPOINT ["ok", "serve"]

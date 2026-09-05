FROM node:22-alpine AS web-builder
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@11.7.0 --activate
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/package.json
RUN pnpm install --frozen-lockfile --filter embymedia-web...
COPY web/ web/
RUN pnpm --dir web build

FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web-builder /app/web/dist ./cmd/server/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /embymedia ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl \
    && addgroup -S -g 10001 embymedia \
    && adduser -S -D -H -u 10001 -G embymedia embymedia \
    && install -d -o embymedia -g embymedia -m 0750 /app /data
WORKDIR /app
COPY --from=builder --chown=embymedia:embymedia /embymedia /app/embymedia
USER embymedia
VOLUME ["/data"]
EXPOSE 8080 8081
ENTRYPOINT ["/app/embymedia"]
CMD ["-host", "0.0.0.0", "-port", "8080", "-mcp-host", "0.0.0.0", "-mcp-port", "8081", "-db", "/data/embymedia.db"]

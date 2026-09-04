FROM node:22-alpine AS web-builder
WORKDIR /app/web
RUN corepack enable && corepack prepare pnpm@latest --activate
COPY web/package.json web/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install
COPY web/ ./
RUN pnpm build

FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web-builder /app/web/dist ./cmd/server/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /embymedia ./cmd/server/main.go ./cmd/server/web.go

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl
WORKDIR /app
COPY --from=builder /embymedia /app/embymedia
EXPOSE 8080 8081
ENTRYPOINT ["/app/embymedia"]
CMD ["-port", "8080", "-mcp-port", "8081", "-db", "/data/embymedia.db"]

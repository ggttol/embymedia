.PHONY: build build-web build-linux install-web test check

.DEFAULT_GOAL := build

install-web:
	pnpm --dir web install --frozen-lockfile

build-web:
	pnpm --dir web build
	rm -rf cmd/server/dist
	cp -R web/dist cmd/server/dist

build: build-web
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/embymedia ./cmd/server

build-linux: build-web
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/embymedia-linux-amd64 ./cmd/server
	cd bin && shasum -a 256 embymedia-linux-amd64 > embymedia-linux-amd64.sha256

test: build-web
	go test -race ./...
	python3 -m unittest discover -s deploy/scripts -p '*_test.py'

check: build-web
	pnpm --dir web typecheck
	go vet ./...
	python3 scripts/check-docs.py

SHELL := /usr/bin/env bash

.PHONY: bootstrap generate check-fdg-v2 test-unit test-integration test-e2e build dev dev-dex dev-app check
bootstrap:
	go mod download
	GOWORK=off go -C tools/openapi mod download
	npm --prefix web ci
# The generated Go server and TypeScript client are ignored local build
# outputs; every build, test, and dev target regenerates them first.
generate:
	./scripts/generate-openapi.sh
check-fdg-v2:
	./scripts/check-fdg-v2.sh
test-unit: generate
	go test ./...
	npm --prefix web test
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest scripts/test_generate_openapi.py
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/local-connections -p 'test_*.py'
test-integration: generate
	./scripts/with-dex.sh go test -tags=integration ./...
test-e2e: generate
	./scripts/with-dex.sh ./scripts/run-e2e.sh
build: generate
	npm --prefix web run build
	go build -o bin/dex-tech-blog ./cmd/server
# Local development keeps Dex state in .dex-dev/ (Runs survive restarts) and
# uses dexcli's default ports 8801/8802 so Dex Web matches the default
# dexWebUrl; override with DEX_DEV_PORT / DEX_DEV_WEB_PORT. Tests keep free ports.
# dev = dev-dex + dev-app; run them in two terminals to restart only the app.
dev: generate
	./scripts/dev.sh all
dev-dex: generate
	./scripts/dev.sh dex
dev-app: generate
	./scripts/dev.sh app
# check generates once, then runs every gate against the same outputs.
check: bootstrap generate check-fdg-v2
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './upstream-dex/*'))" || { gofmt -d $$(gofmt -l $$(find . -name '*.go' -not -path './upstream-dex/*')); exit 1; }
	go mod tidy -diff
	go vet ./...
	go test ./...
	npm --prefix web test
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest scripts/test_generate_openapi.py
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/local-connections -p 'test_*.py'
	./scripts/with-dex.sh go test -tags=integration ./...
	./scripts/with-dex.sh ./scripts/run-e2e.sh
	npm --prefix web run build
	go build -o bin/dex-tech-blog ./cmd/server

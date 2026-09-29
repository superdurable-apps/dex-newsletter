SHELL := /usr/bin/env bash

.PHONY: bootstrap generate check-fdg-v2 test-unit test-integration test-e2e build dev dev-dex dev-app check
bootstrap:
	go mod download
	go -C tools/openapi mod download
	npm --prefix web ci
generate:
	./scripts/generate-openapi.sh
check-fdg-v2:
	./scripts/check-fdg-v2.sh
test-unit: generate
	go test ./...
	python3 -m unittest scripts/test_generate_openapi.py
	python3 -m unittest discover -s scripts/local-connections -p "test_*.py"
	npm --prefix web test
test-integration: generate
	./scripts/with-dex.sh go test -tags=integration ./...
test-e2e: generate
	./scripts/with-dex.sh ./scripts/run-e2e.sh
build: generate
	npm --prefix web run build
	go build -o bin/blog-newsletter ./cmd/server
dev: generate
	./scripts/with-dex.sh bash -c 'npm --prefix web run build && go run ./cmd/server'
dev-dex:
	./scripts/dev-dex.sh
dev-app: generate
	./scripts/dev-app.sh
check: bootstrap generate check-fdg-v2
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './upstream-dex/*'))" || { gofmt -d $$(gofmt -l $$(find . -name '*.go' -not -path './upstream-dex/*')); exit 1; }
	go mod tidy -diff
	go vet ./...
	go test ./...
	python3 -m unittest scripts/test_generate_openapi.py
	python3 -m unittest discover -s scripts/local-connections -p "test_*.py"
	npm --prefix web test
	./scripts/with-dex.sh go test -tags=integration ./...
	./scripts/with-dex.sh ./scripts/run-e2e.sh
	npm --prefix web run build
	go build -o bin/blog-newsletter ./cmd/server

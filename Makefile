SHELL := /usr/bin/env bash

# tools/openapi is an isolated tool module; a local uncommitted go.work used for
# connector development must not change how the code generator resolves.
OGEN := GOWORK=off go -C tools/openapi tool ogen

.PHONY: bootstrap generate check-generated check-fdg-v2 test-unit test-integration test-e2e build dev dev-dex dev-app check
bootstrap:
	go mod download
	GOWORK=off go -C tools/openapi mod download
	npm --prefix web ci
generate:
	$(OGEN) --clean --target ../../internal/api/generated --package generated ../../openapi/openapi.yaml
	npm --prefix web run generate
check-generated:
	./scripts/check-generated.sh
check-fdg-v2:
	./scripts/check-fdg-v2.sh
test-unit:
	go test ./...
	npm --prefix web test
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/local-connections -p 'test_*.py'
test-integration:
	./scripts/with-dex.sh go test -tags=integration ./...
test-e2e:
	./scripts/with-dex.sh ./scripts/run-e2e.sh
build:
	npm --prefix web run build
	go build -o bin/dex-tech-blog ./cmd/server
# Local development keeps Dex state in .dex-dev/ (Runs survive restarts) and
# uses dexcli's default ports 8801/8802 so Dex Web matches the default
# dexWebUrl; override with DEX_DEV_PORT / DEX_DEV_WEB_PORT. Tests keep free ports.
# dev = dev-dex + dev-app; run them in two terminals to restart only the app.
dev:
	./scripts/dev.sh all
dev-dex:
	./scripts/dev.sh dex
dev-app:
	./scripts/dev.sh app
check: bootstrap check-generated check-fdg-v2
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.agents/*' -not -path './upstream-dex/*'))" || { gofmt -d $$(gofmt -l $$(find . -name '*.go' -not -path './.agents/*' -not -path './upstream-dex/*')); exit 1; }
	go mod tidy -diff
	go vet ./...
	$(MAKE) test-unit
	$(MAKE) test-integration
	$(MAKE) test-e2e
	$(MAKE) build

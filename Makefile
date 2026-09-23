SHELL := /usr/bin/env bash

.PHONY: fmt fmt-check vet test arch build check run generate-orval orval-check radar-check hxc-identity-boundaries prepare-donor-views

generate-orval:
	npx orval --config ./orval.config.mjs

orval-check:
	@before="$$(mktemp)"; after="$$(mktemp)"; \
	find web/v3/generated -type f -print0 2>/dev/null | sort -z | xargs -0 shasum -a 256 > "$$before" 2>/dev/null || true; \
	npx orval --config ./orval.config.mjs >/dev/null; \
	find web/v3/generated -type f -print0 | sort -z | xargs -0 shasum -a 256 > "$$after"; \
	cmp -s "$$before" "$$after" || { echo 'generated v3 API clients are stale' >&2; rm -f "$$before" "$$after"; exit 1; }; \
	rm -f "$$before" "$$after"

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal && exit 1)

prepare-donor-views:
	@test -d web/v3
	node scripts/prepare-donor-source-views.mjs

vet: prepare-donor-views
	GOWORK=off go vet ./...

test: prepare-donor-views
	GOWORK=off go test ./...

arch:
	python3 scripts/check-architecture.py

build: prepare-donor-views
	mkdir -p bin
	GOWORK=off go build -o bin/aicrm ./cmd/aicrm

check: fmt-check vet test arch hxc-identity-boundaries build

hxc-identity-boundaries:
	bash scripts/check-hxc-identity-boundaries.sh

radar-check: prepare-donor-views
	bash scripts/check-radar-boundaries.sh
	node scripts/validate-openapi.mjs
	GOWORK=off go test ./internal/radar/... ./cmd/migrate-radar-v2 ./cmd/aicrm

run: prepare-donor-views
	GOWORK=off go run ./cmd/aicrm

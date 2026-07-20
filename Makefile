# Hugr Hub — build targets.
#
# The management console SPA (console/) is embedded into the Go hub binary via
# go:embed. `hub-service` therefore depends on `console-build` so the real SPA
# ships instead of the committed dist/index.html placeholder.

GO_TAGS := duckdb_arrow
PNPM    := pnpm --dir console

.PHONY: help
help:
	@echo "Console:"
	@echo "  console-install     install console deps (pnpm, frozen lockfile)"
	@echo "  console-build       build the management console SPA  -> console/dist"
	@echo "  console-chat        build the embeddable chat bundle   -> console/dist-chat"
	@echo "  console-typecheck   tsc --noEmit over the console"
	@echo "  console-clean       remove console build output"
	@echo "Go:"
	@echo "  hub-service         build the Go hub binary (embeds the built console)"
	@echo "  test                go test -tags=$(GO_TAGS) ./..."
	@echo "  vet                 go vet -tags=$(GO_TAGS) ./..."
	@echo "Docker:"
	@echo "  docker-hub-service  docker build -f Dockerfile.hub-service (builds+embeds console)"

# ── Console ──────────────────────────────────────────────────────────
.PHONY: console-install console-build console-chat console-typecheck console-clean
console-install:
	$(PNPM) install --frozen-lockfile

console-build: console-install
	$(PNPM) run build

console-chat: console-install
	$(PNPM) run build:chat

console-typecheck: console-install
	$(PNPM) run typecheck

console-clean:
	rm -rf console/dist/assets console/dist-chat

# ── Go ───────────────────────────────────────────────────────────────
.PHONY: hub-service test vet
hub-service: console-build
	CGO_ENABLED=1 go build -tags=$(GO_TAGS) -o bin/hub-service ./cmd/hub-service/

test:
	go test -tags=$(GO_TAGS) ./...

vet:
	go vet -tags=$(GO_TAGS) ./...

# ── Docker ───────────────────────────────────────────────────────────
.PHONY: docker-hub-service
docker-hub-service:
	docker build -f Dockerfile.hub-service -t hugr-lab/hub-service:local .

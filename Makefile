.PHONY: all gen check-gen web build test lint clean

all: build

# Generate Go and TypeScript code from proto/ (requires web deps for protoc-gen-es).
gen: web/node_modules
	go tool buf generate

# Fail if generated code differs from what buf would generate now.
check-gen: web/node_modules
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	go tool buf generate -o "$$tmp" && \
	diff -r "$$tmp/gen/go" gen/go && \
	diff -r "$$tmp/web/src/gen" web/src/gen

# Build the web GUI into internal/webui/dist for embedding.
web: web/node_modules
	cd web && npm run build
	touch internal/webui/dist/.gitkeep

web/node_modules: web/package-lock.json
	cd web && npm ci
	touch web/node_modules

build: web
	go build -o bin/underdraw ./cmd/underdraw

test:
	go test ./...

lint: check-gen
	go tool buf lint
	go vet ./...
	cd web && npm run lint

clean:
	rm -rf bin
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete

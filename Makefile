.PHONY: all gen web build test lint clean

all: build

# Generate Go and TypeScript code from proto/ (requires web deps for protoc-gen-es).
gen: web/node_modules
	go tool buf generate

# Build the web GUI into internal/webui/dist for embedding.
web: web/node_modules
	cd web && npm run build
	touch internal/webui/dist/.gitkeep

web/node_modules: web/package-lock.json
	cd web && npm ci
	touch web/node_modules

build: web
	go build -o bin/kiln ./cmd/kiln

test:
	go test ./...

lint:
	go tool buf lint
	go vet ./...
	cd web && npm run lint

clean:
	rm -rf bin
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete

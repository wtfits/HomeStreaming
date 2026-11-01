# HomeStreaming build targets (see README.md).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
export CGO_ENABLED := 0

.PHONY: all web build run test test-e2e dist docker clean

all: web build

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/homestreaming ./cmd/homestreaming

run: build
	./bin/homestreaming --data-dir ./data

test:
	go test ./...

test-e2e:
	./scripts/e2e.sh

dist:
	VERSION=$(VERSION) ./scripts/build-dist.sh

docker:
	docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile -t homestreaming:$(VERSION) .

clean:
	rm -rf bin dist web/dist/assets web/dist/index.html

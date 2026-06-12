.PHONY: build run test lint clean docker-build

BINARY_NAME=configsync
DOCKER_IMAGE=configsync:latest

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/$(BINARY_NAME) ./cmd/configsync

run:
	go run ./cmd/configsync

test:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	go vet ./...
	@golangci-lint run ./... 2>/dev/null || echo "golangci-lint not installed, running go vet only"

clean:
	rm -rf bin/ coverage.out

docker-build:
	docker build -t $(DOCKER_IMAGE) .

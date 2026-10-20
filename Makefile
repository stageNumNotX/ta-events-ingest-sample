.PHONY: test race vet fmt build run

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

build:
	go build -o /tmp/server ./cmd/server
	go build -o /tmp/traffic-runtime ./cmd/traffic-runtime

run:
	@test -f .env || (echo "copy .env.example to .env first" && exit 1)
	set -a && . ./.env && set +a && go run ./cmd/server

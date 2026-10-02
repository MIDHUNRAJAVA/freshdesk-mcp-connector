.PHONY: build test race lint cover demo demo-flaky demo-outage tools-json clean

build:
	go build -o bin/ ./cmd/...

test:
	go test ./...

race:
	go test -race ./...

# gofmt must report nothing, and go vet must pass.
lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)
	go vet ./...

cover:
	go test -coverprofile=coverage.txt ./...
	go tool cover -func=coverage.txt | tail -1

# Regenerate docs/mcp-tools.json after changing a tool definition.
tools-json: build
	./bin/freshdesk-mcp -print-tools > docs/mcp-tools.json

# One-command demo: fake Freshdesk with invented data, no credentials needed.
demo: build
	./scripts/demo.sh basic

# Same, but the fake server fails on purpose so you can watch retries.
demo-flaky: build
	./scripts/demo.sh flaky

# A short outage: watch the circuit breaker open and recover.
demo-outage: build
	./scripts/demo.sh outage

clean:
	rm -rf bin coverage.txt

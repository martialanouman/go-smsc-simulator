BINARY      := smsc-simulator
CMD         := ./cmd/smsc-simulator
BIN_DIR     := bin
CONFIG      ?= examples/minimal.yml

GOLANGCI_LINT_VERSION := v2.3.0
DOCKER_IMAGE          := smsc-simulator:dev

.PHONY: all tools build test lint vuln run docker clean

all: lint test build

## tools: install the Go binaries kept out of go.mod (plan §1.3)
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@latest

## build: compile the single binary
build:
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

## test: the race detector is mandatory, never optional (CLAUDE.md)
test:
	go test -race ./...

## lint: must report zero warnings
lint:
	golangci-lint run

## vuln: scan dependencies for known vulnerabilities
vuln:
	govulncheck ./...

## run: start the simulator against a fixture, e.g. make run CONFIG=examples/minimal.yml
run:
	go run $(CMD) --config $(CONFIG)

## docker: build the distribution image.
## The Dockerfile is an S7 deliverable (plan §11); this target fails until S7 lands it.
docker:
	@test -f Dockerfile || { \
		echo "make docker: no Dockerfile yet — it is an S7 deliverable (plan §11)"; \
		exit 1; \
	}
	docker build -t $(DOCKER_IMAGE) .

clean:
	rm -rf $(BIN_DIR)

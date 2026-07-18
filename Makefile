BINARY      := smsc-simulator
CMD         := ./cmd/smsc-simulator
BIN_DIR     := bin
CONFIG      ?= examples/minimal.yml

GOLANGCI_LINT_VERSION := v2.3.0
GOVULNCHECK_VERSION   := v1.6.0
GORELEASER_VERSION    := v2.17.0
DOCKER_IMAGE          := smsc-simulator:dev

# VERSION stamps the binary. git describe reports the exact tag on a release
# commit (e.g. v0.4.0) or a dev suffix in between (v0.4.0-3-gabc123-dirty); with
# no tags yet it falls back to a short SHA, and to "dev" outside a git checkout.
# The release build overrides this with the tag GoReleaser injects into the same
# main.version symbol.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all check tools build test lint vuln run snapshot docker clean

all: lint test build

## check: the full Definition of Done gate — lint, race tests, vuln scan, build
check: lint test vuln build

## tools: install the Go binaries kept out of go.mod (plan §1.3)
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

## build: compile the single binary, stamped with VERSION
build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

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

## snapshot: build the release artifacts locally without tagging or publishing.
## Mirrors what CI runs on a merge to main, so the .goreleaser.yml can be verified
## before it ever runs for real. Requires `make tools`.
snapshot:
	goreleaser release --snapshot --clean

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

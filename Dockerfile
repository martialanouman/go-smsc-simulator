# syntax=docker/dockerfile:1

# Build a static binary, then ship it on scratch — the smallest possible image for a
# test/CI SMSC peer (plan §11 / T3). CGO is off so there is no libc to match on the
# target runner; the S6 TLS cert is generated in memory, so no system CA pool is needed
# and scratch (no shell, no certs, no packages) suffices.

FROM golang:1.26 AS build
WORKDIR /src

# Download modules in their own layer, so a source edit that leaves go.mod/go.sum
# untouched reuses the cached dependencies.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION stamps main.version, mirroring the Makefile / GoReleaser -ldflags. `make docker`
# passes --build-arg VERSION=$(git describe ...); a bare `docker build` defaults to "docker".
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /smsc-simulator ./cmd/smsc-simulator

# --- distribution image ---
FROM scratch

# Non-root numeric uid/gid: scratch has no /etc/passwd, so only a numeric id works.
# 65534 is the conventional "nobody".
USER 65534:65534

COPY --from=build /smsc-simulator /smsc-simulator

# The config is the only input, mounted read-only at runtime — nothing is baked in (the
# simulator has no defaults on purpose). The SMPP and observability ports come from the
# .yml; EXPOSE documents the fixture defaults (2775 SMPP, 9000 observability).
EXPOSE 2775 9000

ENTRYPOINT ["/smsc-simulator"]
CMD ["--config", "/etc/smsc/config.yml"]

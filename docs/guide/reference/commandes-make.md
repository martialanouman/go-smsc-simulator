# Référence — Commandes Make

> **Catégorie Diátaxis : Référence.** Les cibles du `Makefile`. Variables surchargeables
> avec `make <cible> VAR=valeur`.

## Cibles

| Cible | Commande | Rôle |
|---|---|---|
| `all` | `lint test build` | Chaîne de base. |
| `check` | `lint test vuln build` | Chaîne complète (proche de la CI). |
| `tools` | `go install` golangci-lint, govulncheck, goreleaser | Installe l'outillage (hors `go.mod`). |
| `build` | `go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/smsc-simulator ./cmd/smsc-simulator` | Compile le binaire. |
| `test` | `go test -race ./...` | **Obligatoire avant toute PR.** |
| `fuzz` | `go test -fuzz FuzzReadPDU` puis `FuzzDecode` sur `./internal/smpp` (`FUZZTIME=30s`) | Fuzz borné du décodeur PDU. |
| `loadtest` | `go test -tags loadtest -bench BenchmarkThroughput -benchmem ./internal/smsc` | Bench de débit + déterminisme sous charge (hors CI unitaire). |
| `lint` | `golangci-lint run` | Linters (config `.golangci.yml`). |
| `vuln` | `govulncheck ./...` | Vulnérabilités. |
| `run` | `go run ./cmd/smsc-simulator --config $(CONFIG)` | Lance le simulateur (`CONFIG` défaut `examples/minimal.yml`). |
| `snapshot` | `goreleaser release --snapshot --clean` | Build de release local (sans publier). |
| `docker` | `docker build --build-arg VERSION=$(VERSION) -t smsc-simulator:dev .` | Image de distribution (scratch). |
| `clean` | `rm -rf bin` | Nettoyage. |

## Variables

| Variable | Défaut | Usage |
|---|---|---|
| `CONFIG` | `examples/minimal.yml` | Fixture pour `make run`. |
| `FUZZTIME` | `30s` | Durée par cible de fuzz. |
| `VERSION` | `git describe --tags --always --dirty` (sinon `dev`) | Version injectée au build. |
| `DOCKER_IMAGE` | `smsc-simulator:dev` | Tag de l'image. |

## Exemples

```bash
make run CONFIG=examples/flaky-carrier.yml   # lance un carrier instable
make test                                    # tests -race (avant PR)
make fuzz FUZZTIME=60s                        # fuzz plus long
make docker VERSION=v1.2.0                    # image taguée
```

## Voir aussi

- [reference/cli.md](cli.md) — le binaire lui-même.
- [how-to/deployer-avec-docker.md](../how-to/deployer-avec-docker.md) — de l'image au cluster.

# Step 000 — S0 · Fondations & outillage

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §4.
> **Statut : ✅ LIVRÉ** (commits `9711021` / branche `s0-fondations-outillage`).
> Ce fichier documente le plan d'implémentation et sert de checklist de non-régression.

## Objectif

Un dépôt qui build/teste/lint, un binaire qui démarre depuis `--config`, et les primitives transverses (config, observabilité). **Aucun SMPP.**

## Dépend de

—

## Livrables (état réel du dépôt)

| Livrable | Fichier(s) | Fait |
|---|---|---|
| Module + deps | `go.mod` (`github.com/martialanouman/go-smsc-simulator`, go 1.26) | ✅ |
| Cibles de build | `Makefile` (`tools build test lint vuln run docker check`) | ✅ |
| Lint | `.golangci.yml` (`local-prefixes` = module) | ✅ |
| CI | `.github/workflows/ci.yml` (lint / test -race / vuln / build) | ✅ |
| Squelette binaire | `cmd/smsc-simulator/main.go` (flag `--config`, `signal.NotifyContext`, boot gate, arrêt gracieux) + `main_test.go` | ✅ |
| Config (amorce) | `internal/config/config.go` (`Config`, `Load`, `ErrNoConfigPath`) | ✅ |
| Observabilité | `internal/observability/observability.go` (`NewLogger`), `server.go` (`Server`, `/health`), `server_test.go` | ✅ |

## Décisions actées à S0 (ne pas ré-ouvrir sans décision d'équipe)

- Nom de module : `github.com/martialanouman/go-smsc-simulator`.
- Chemins `/health` et `/metrics` **nus** (sans `/v1`) ; `/v1` réservé à l'inspection (S2).
- `examples/` ne contient **que** des fixtures valides démontrables ; les fixtures cassées vivent sous `internal/config/testdata/`.
- Codec SMPP **interne** (option §1.1 retenue), pas de module partagé.

## Hors périmètre

Aucun SMPP, aucun scénario, aucun endpoint au-delà de `/health`, aucune validation métier.

## Critères d'acceptation (checklist de non-régression)

- [x] `make build/test/lint/vuln` verts (`make check`).
- [x] `make run CONFIG=examples/minimal.yml` démarre ; `GET :PORT/health` → 200.
- [x] Invariant (b) amorcé : `--config` absent / fichier illisible / YAML invalide → sortie non nulle **avant** ouverture de port (testé dans `main_test.go`).
- [x] `SIGTERM` → arrêt gracieux (le process rend la main).

## Notes de vérification

Le boot gate est le commentaire `--- boot gate ---` dans `main.go:run` : rien au-dessus ne doit ouvrir de socket. Toute PR future qui déplace une ouverture de listener au-dessus de la ligne `config.Load` casse l'invariant (b).

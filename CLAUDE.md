# CLAUDE.md — Simulateur SMSC (Go)

Manuel de travail pour Claude Code sur ce dépôt. Lis-le en entier avant d'écrire du code. Il est court volontairement : les détails vivent dans les guides et la spec référencés en bas (`docs/`).

> Ce fichier est le manuel de session, à la racine du repo. Ne pas le confondre avec le `CLAUDE.md` de la **passerelle SMS** (projet distinct — le système sous test).

## Ce qu'on construit

Un **simulateur SMSC** en Go : un **serveur SMPP** qui se fait passer pour un SMSC opérateur, pour tester la passerelle SMS (le système sous test). Il accepte les binds, répond aux `submit_sm` selon un **scénario prédéfini paramétré**, émet DLR et MO, et injecte des pannes (latence, erreurs, timeouts, déconnexions) — le tout **déterministe et reproductible** à graine fixe. C'est un **outil de test/CI**, jamais un composant de production.

**Ce que ce n'est pas** : pas un vrai SMSC, pas un composant déployé en prod, pas un service configurable à chaud. Toute la config vient d'un **fichier `.yml` chargé au démarrage** ; il n'y a **aucune API HTTP de configuration**.

## Les 3 idées directrices (à ne jamais perdre de vue)

1. **Configuration 100 % déclarative.** Un `.yml` chargé et validé au démarrage est la **seule** entrée de config. Reconfigurer = éditer le fichier et relancer (démarrage < 2 s). Aucune mutation runtime.
2. **Scénarios prédéfinis.** Un catalogue **figé** de 6 profils nommés (`healthy`, `flaky-carrier`, `throttling-carrier`, `dead-carrier`, `slow-carrier`, `throughput-capped`). Le `.yml` sélectionne un profil et règle ses paramètres exposés — **jamais** de règles de réponse arbitraires.
3. **Déterminisme honnête.** À `seed` fixe, tout est reproductible **par session de bind**, ancré sur un compteur logique de PDU (`per_bind_clock`), **jamais** sur l'horloge murale. Les événements temporels (DLR, MO, déconnexions, transitions) sont des **planifications sur tick** déclarées dans le `.yml`.

## Commandes

```bash
make build                 # compile cmd/smsc-simulator
make test                  # go test -race ./...   (OBLIGATOIRE avant toute PR)
make lint                  # golangci-lint (config .golangci.yml)
make vuln                  # govulncheck
make run CONFIG=examples/healthy.yml   # lance le simulateur avec une fixture
make docker                # build l'image de distribution
```

## Architecture (carte mentale)

Un **binaire unique** (`cmd/smsc-simulator`) héberge N **SMSC virtuels** décrits dans le `.yml`, plus une **surface HTTP read-only** d'observabilité.

Composants (`internal/`) : `config` (chargement + validation fail-fast du `.yml`), `smpp` (codec PDU côté serveur + machine à états de session), `smsc` (SMPP Server Engine : listener + goroutines par connexion), `scenario` (catalogue des 6 profils + sélection de résultat pondérée), `fault` (latence, timeout, disconnect), `schedule` (Schedule Runner : DLR/MO/déconnexions/transitions par tick + flush de quiescence), `recorder` (tampon circulaire des `submit_sm`), `rng` (PRNG graînée par bind), `metrics` (Prometheus), `observability` (slog + serveur read-only).

**Flux d'un `submit_sm`** : décodage PDU → Scenario Engine (profil actif, incrémente `per_bind_clock`/`logical_clock`) → sélection de résultat pondérée `(seed, per_bind_clock)` → Fault Injector (latence/erreur/timeout/disconnect) → `submit_sm_resp` → DLR planifié dans `pending_logical_schedule` → PDU enregistrée. Aucune base externe : tout est en mémoire, borné, éphémère.

## Layout du dépôt

```
cmd/smsc-simulator/main.go
internal/config  internal/smpp  internal/smsc  internal/scenario  internal/fault
internal/schedule  internal/recorder  internal/rng  internal/metrics  internal/observability
internal/smpptest (client SMPP in-process pour les tests)
examples/*.yml   deploy/   docs/   Dockerfile   docker-compose.yml
```

Tout le code métier vit sous `internal/`. Interfaces définies côté consommateur. Détail : `docs/convention-style-go-simulateur-smsc.md` §2.

## Règles d'or (toujours / jamais)

- **JAMAIS de configuration hors du `.yml`.** Pas d'endpoint HTTP mutant, pas de reconfiguration runtime. La surface HTTP est **strictement en lecture seule**. Un `POST`/`PATCH`/`PUT`/`DELETE` sur l'observabilité est un bug.
- **JAMAIS d'horloge murale sur un chemin déterministe.** En mode graîné (`seed` défini), tout mécanisme temporel lit `per_bind_clock`. Jamais `time.Now()`, jamais un PRNG non graîné sur le chemin de décision. Le mode chaos (`seed` absent) est le **seul** endroit où l'horloge murale est permise.
- **JAMAIS de scénario arbitraire.** Le catalogue de 6 profils est figé dans le code. Le `.yml` sélectionne et paramètre ; il ne définit pas de `response_rules` à la main.
- **TOUJOURS fail-fast à la config.** La validation complète du `.yml` (profil connu, cohérence `seed`/`clock`, ports uniques, bornes) s'exécute **avant** d'ouvrir le moindre port. Un `.yml` invalide → sortie non nulle avec message explicite. `log.Fatal` **uniquement** au boot.
- **TOUJOURS `go test -race ./...` vert** avant une PR. Aucune goroutine sans condition d'arrêt. `context.Context` en 1er paramètre partout.
- **Modèle SMPP** : une goroutine lecture + une écriture par connexion, l'état de session possédé par une seule goroutine (pas de verrou sur la fenêtre). Arrêt gracieux : unbind propre des binds sur `SIGTERM`.
- **Déterminisme scopé par bind, pas global.** `logical_clock` est un observable d'assertion (`GET /logical-clock`), **jamais** une référence de planification — son ordre entre binds concurrents n'est pas reproductible. Toute planification lit `per_bind_clock`.
- **Le PDU Recorder retient le contenu volontairement** (c'est la fonctionnalité d'assertion). Mais les logs `slog` ne déversent pas le contenu brut au niveau `info` — le contenu s'inspecte via `GET /received-pdus`.
- **Labels Prometheus bornés** : `virtual_smsc`, `bind_type`, `outcome`, `scenario`. **Jamais** de MSISDN, `message_id` ou contenu en label.
- **Versions & API des bibliothèques : TOUJOURS via Context7 (`ctx7`).** Avant d'ajouter ou d'utiliser l'API d'une bibliothèque (yaml.v3, prometheus/client_golang, google/uuid), appelle le skill `ctx7` pour la doc à jour et la bonne version. Ne devine JAMAIS un numéro de version ni une signature depuis la mémoire.

## Les 4 invariants (tests bloquants, verts à vie)

a) **Déterminisme par bind** — à `seed` fixe, la même fixture produit la même séquence de résultats/latences/DLR/MO par bind. b) **Config fail-fast** — toute config invalide échoue au chargement, avant d'ouvrir un port ; aucune reconfiguration runtime n'existe. c) **HTTP read-only** — aucun endpoint mutant ; l'observabilité ne modifie jamais l'état. d) **Flush de quiescence** — une planification en ticks au repos est drainée, jamais figée.

## Tests

Pyramide : beaucoup d'unitaires (codec, sélection de résultat, distributions, validation config), des tests de **déterminisme** (rejeu à graine fixe — le plus important), du **fuzz** sur le décodeur PDU, des tests d'intégration bout-en-bout avec un **client SMPP in-process**. **Aucune dépendance d'infrastructure** — pas de `testcontainers`, tout est Go pur. Détail complet : `docs/strategie-de-test-simulateur-smsc.md`.

## Definition of Done (chaque PR)

`gofmt`/`goimports` verts • `golangci-lint` sans alerte • `go test -race` vert • `govulncheck` vert • critères d'acceptation de la tâche couverts par des tests • aucun invariant violé • godoc sur l'exporté • PR petite et focalisée (une tâche du plan d'exécution).

## Recettes fréquentes

- **Ajouter une dépendance** : d'abord `ctx7` (Context7) pour la bonne version et l'API à jour, puis `go get`, puis `go mod tidy`. Jamais de version devinée. Rappel : le simulateur vise le **minimalisme** (zéro dépendance d'infra) — justifier tout ajout.
- **Ajouter un paramètre à un profil** : l'ajouter au schéma `.yml` (`internal/config`), à la validation fail-fast, au profil concerné dans `internal/scenario`, et à une fixture `examples/`. Jamais de paramètre non validé.
- **Ajouter un mécanisme temporel** : le brancher sur le **Schedule Runner** (`internal/schedule`), ancré sur `per_bind_clock`, drainé par le flush de quiescence. Jamais un `time.Timer` sur horloge murale en mode graîné.
- **Ajouter un endpoint d'observabilité** : uniquement en **lecture** (`GET`). Tout verbe mutant est refusé par principe (invariant c).
- **Changer le schéma `.yml`** : mettre à jour `internal/config` (structs + validation), la spec §3.1, et au moins une fixture `examples/`.
- **Exécuter un jalon** : chaque jalon `S0`–`S7` a un plan d'implémentation `steps/step-00N.md`. `steps/` = reste à faire, `steps-done/` = archive. Dès qu'un jalon est livré (critères d'acceptation verts, PR mergée), déplacer son `step-00N.md` de `steps/` vers `steps-done/`. Voir `steps/README.md`.

## Index documentaire (source de vérité)

- Quoi/pourquoi : `docs/specification-technique-simulateur-smsc.md` (spec v3.0 — le schéma `.yml` de sa §3.1 est le contrat de config)
- Plan de construction : `docs/plan-execution-simulateur-smsc.md` (jalons S0–S7)
- Style Go : `docs/convention-style-go-simulateur-smsc.md`
- Tests : `docs/strategie-de-test-simulateur-smsc.md`
- Système sous test (repo passerelle, séparé) : `specification-technique-passerelle-sms.md` (§6.4 throttling adaptatif, §6.13 auto-reconnexion, §6.15 disjoncteur — ce que les profils exercent) ; `glossaire-domaine-sms.md` (vocabulaire SMS partagé)

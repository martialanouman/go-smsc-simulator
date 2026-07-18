# Convention de style Go — Simulateur SMSC

**Composant :** Simulateur SMSC configurable (Go) — outil de test/CI
**Statut :** Convention de style v1.0
**Portée :** ce guide fixe **l'apparence du code** — nommage, formatage, imports, godoc, idiomes. Les décisions d'**architecture** (concurrence, déterminisme, tests) sont dans le plan d'exécution et la stratégie de test ; ce document ne les répète pas.

> *La prose est en français ; le code, les identifiants et les commentaires de code sont en anglais. Les clés du fichier `.yml` sont en `snake_case` (comme la spec §3.1) ; les identifiants Go sont en `MixedCaps` — le mapping des tags fait le pont.*

Ce document **hérite de la convention de style de la passerelle** (`convention-style-go.md`) : sauf mention contraire ci-dessous, les mêmes règles s'appliquent (même casse d'acronymes, même politique d'imports en trois groupes, même godoc, mêmes anti-idiomes). Il ne réénonce que ce qui diffère ou mérite d'être souligné pour un **outil de test déterministe**. Base de référence, dans l'ordre : ce document, [Google Go Style Guide](https://google.github.io/styleguide/go/), [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments), [Effective Go](https://go.dev/doc/effective_go). Règles **[MUST]** vérifiées en CI/revue ; **[SHOULD]** fortement recommandées.

---

## 1. Formatage (identique à la passerelle)

**[MUST]** Tout fichier est passé à `gofmt` via `goimports` ; `gofmt -l` doit être vide en CI. Indentation par tabulations. Ligne cible 99 colonnes, plafond souple 120 — extraire une variable plutôt que laisser filer une ligne.

**[MUST]** `goimports` en **trois groupes** (stdlib / tiers / interne), `local-prefixes: github.com/martialanouman/go-smsc-simulator`.

---

## 2. Nommage

### 2.1 Règles générales (identiques à la passerelle)

**[MUST]** `MixedCaps` / `mixedCaps`, jamais de `snake_case` pour les identifiants Go. Le `snake_case` n'existe que dans les **clés YAML**, les tags de struct et (le cas échéant) les clés JSON de sortie de l'observabilité.

**[MUST]** Acronymes à casse homogène : `ID`, `URL`, `API`, `SMPP`, `PDU`, `MSISDN`, `TON`, `NPI`, `TLV`, `UDH`, `TLS`, `DLR`, `MO`, `MT`, `SMSC`. Donc `bindID`, `smppPDU`, `smscID`, `virtualSMSC`, `destMSISDN`. Jamais `SmscId`, `SmppPdu`.

**[MUST]** Pas de « bruit » : pas de `util`, `common`, `helpers`, `base`, `manager`, `data`. Nommer par le rôle (`scenario`, `fault`, `schedule`, `recorder`, `rng`).

### 2.2 Vocabulaire du domaine simulateur (spec — casse imposée)

Ces termes viennent de la spec ; leur casse Go est fixée une fois pour éviter les variantes :

| Concept (spec) | Identifiant Go | Note |
|---|---|---|
| SMSC virtuel | `VirtualSMSC`, `virtualSMSC` | jamais `Vsmsc`, `VSmsc` |
| horloge par bind | `perBindClock` | référence de timing déterministe |
| horloge globale | `logicalClock` | observable d'assertion uniquement |
| planification en attente | `pendingSchedule` / `PendingSchedule` | draine par tick |
| flush de quiescence | `quiescenceFlush` | jamais `quiescenceFlushMs` en Go (c'est la clé YAML) |
| profil de scénario | `Profile` (type nommé) | valeur = string alignée sur le `.yml` |
| tick logique | `tick` (type `uint64`) | unité de `perBindClock` |

### 2.3 Énumérations (identique à la passerelle, aligné sur le `.yml`)

**[MUST]** Une énumération est un **type nommé** sur `string` (lisible dans les logs et le `.yml`). Valeurs préfixées par le type et **exactement** égales aux valeurs du schéma `.yml` (spec §3.1) — une divergence est un bug de contrat.

```go
// Profile is one of the fixed, predefined scenario profiles. No arbitrary profiles exist.
type Profile string

const (
    ProfileHealthy          Profile = "healthy"
    ProfileFlakyCarrier     Profile = "flaky-carrier"
    ProfileThrottlingCarrier Profile = "throttling-carrier"
    ProfileDeadCarrier      Profile = "dead-carrier"
    ProfileSlowCarrier      Profile = "slow-carrier"
    ProfileThroughputCapped Profile = "throughput-capped"
)

// Valid reports whether p is a known profile. Config loading rejects any unknown value (fail-fast).
func (p Profile) Valid() bool { /* ... */ }
```

**[MUST]** Idem pour les énumérations issues du `.yml` : `LatencyDistribution` (`fixed`/`uniform`/`normal`/`spike`), `Clock` (`logical`/`wallclock`), `DisconnectScope` (`all`/`oldest`/`random`), `DisconnectWhen` (`before_response`/`after_response`), `MOMode` (`scheduled`/`auto`/`disabled`). Chaque type externe expose un `Valid()` ou un `Parse<Type>` — la validation est **centralisée au chargement**, jamais dispersée en `switch`.

### 2.4 Receveurs & interfaces (identique à la passerelle)

**[MUST]** Receveur court (1–2 lettres), cohérent sur toutes les méthodes d'un type : `func (e *Engine) …`, `func (s *Session) …`, `func (r *Runner) …`. Jamais `this`/`self`.

**[SHOULD]** Interfaces petites, nommées par l'agent (`-er`), **définies côté consommateur** : `scenario` déclare ce dont il a besoin, `config` fournit l'implémentation concrète.

---

## 3. Structs, champs et tags YAML

**[MUST]** Les structs de configuration portent des tags `yaml` en `snake_case`, alignés **exactement** sur le schéma de la spec §3.1 :

```go
type VirtualSMSCConfig struct {
    Name                  string          `yaml:"name"`
    Port                  int             `yaml:"port"`
    BindCredentials       BindCredentials `yaml:"bind_credentials"`
    AddrTON               int             `yaml:"addr_ton"`
    AddrNPI               int             `yaml:"addr_npi"`
    AddressRange          string          `yaml:"address_range"`
    Seed                  *uint64         `yaml:"seed"`          // nil = chaos/unseeded mode
    PDUBufferSize         int             `yaml:"pdu_buffer_size"`
    ThroughputLimitPerSec *int            `yaml:"throughput_limit_per_sec"` // nil = no limit
    Scenario              ScenarioConfig  `yaml:"scenario"`
}
```

**[MUST]** Les champs **optionnels** du `.yml` (`seed`, `throughput_limit_per_sec`) sont des **pointeurs** (ou un type `Optional`) pour distinguer « absent » de « zéro » — `seed: 0` est un mode graîné valide, `seed` absent est le mode chaos. Ne jamais confondre les deux.

**[MUST]** Le `bind_credentials.password` ne doit pas fuiter en clair par une sérialisation d'observabilité : champ `yaml` en entrée, mais **jamais** exposé par un endpoint read-only. Contrairement à la passerelle, il n'y a pas de type `Body` masquant généralisé (le contenu des `submit_sm` est justement conservé pour l'assertion) — mais **les secrets de bind restent des secrets**.

**[SHOULD]** Booléens nommés positivement : `tlsEnabled`, `protocolEdgeCasesEnabled` — pas `disabledTLS`.

---

## 4. Déterminisme — idiomes imposés (spécifique au simulateur)

C'est la particularité du projet. Ces règles n'existent pas dans la passerelle.

**[MUST]** **Aucun `time.Now()`, `time.Since()`, `time.Timer`, `time.Ticker` ni `math/rand` non graîné sur un chemin de décision déterministe.** Le timing déterministe lit `perBindClock`. `time.*` mural n'est autorisé **que** dans le mode chaos (`seed == nil`) et pour la latence murale réelle explicitement demandée (`clock: wallclock`).

**[MUST]** Le PRNG est `math/rand/v2`, **une instance par SMSC virtuel / par bind**, graine dérivée de `seed` de façon stable et documentée. Jamais le PRNG global du package `rand`. Jamais de source de hasard partagée entre binds (ça casserait la reproductibilité par bind).

```go
// newBindRNG derives a per-bind deterministic PRNG. The derivation MUST be stable across runs:
// same (seed, virtualSMSCID, bindOrdinal) => same stream. Never seed from the wall clock.
func newBindRNG(seed uint64, smscID string, bindOrdinal uint32) *rand.Rand { /* ... */ }
```

**[MUST]** Toute décision « aléatoire » (choix de résultat pondéré, tirage de latence, choix de résultat DLR) consomme le PRNG **du bond concerné**, dans un **ordre stable** relativement au `perBindClock`. Documenter, en commentaire, l'ancrage au tick là où la décision est prise :

```go
// selectOutcome draws the weighted outcome for the submit_sm at tick t on this bind.
// Deterministic: identical (seed, bind, t) => identical outcome. See spec §6.3.
func (e *Engine) selectOutcome(t tick, rng *rand.Rand) Outcome { /* ... */ }
```

**[SHOULD]** Les tests de déterminisme (rejeu) sont la garde primaire ; le style aide en gardant l'ancrage au tick **visible et grep-able** (`// anchored to perBindClock`).

---

## 5. Commentaires et godoc (identique à la passerelle, + annotations d'invariant)

**[MUST]** Tout symbole exporté a un godoc commençant par son nom, expliquant le **pourquoi** et les **invariants**, pas la paraphrase de la signature.

**[SHOULD]** Les invariants du simulateur sont annotés en clair là où ils sont maintenus, avec la référence de spec : `// deterministic per bind (§6.3)`, `// read-only: never mutates state`, `// drained by the quiescence flush (§6.3)`, `// fail-fast: rejected at config load`. Ces annotations sont la mémoire de conception.

**[MUST]** Les marqueurs `TODO`/`FIXME` sont tracés : `// TODO(martial): borne le fan-out — TICKET-123`. Pas de TODO anonyme. Les STUB de jalon suivent la convention du plan (`// STUB S3: ... See plan §6`).

---

## 6. Idiomes imposés (identique à la passerelle)

**[MUST]** Early-return plutôt qu'imbrication. `context.Context` premier paramètre nommé `ctx`, jamais stocké dans une struct. Vérifier les erreurs immédiatement ; un ignore volontaire est annoté (`_ = conn.Close() // best-effort on teardown`). Erreurs enrobées avec `%w`, message en minuscule sans ponctuation ni « error »/« failed ». Sentinelles préfixées `Err`. Logs uniquement via `log/slog` structuré — jamais `fmt.Println`/`log.Printf`.

**[MUST]** Constructeur `New<Type>` retournant un `*Type` concret. Pas de variable de package mutable exportée, pas de singleton global, pas d'`init()` à effet de bord.

---

## 7. Anti-idiomes (refusés en revue)

Tous ceux de la passerelle (`convention-style-go.md` §8), **plus** ces spécifiques au simulateur, systématiquement rejetés :

- **Toute source de non-déterminisme sur un chemin graîné** : `time.Now()`, `time.Since()`, PRNG global `rand.Intn(...)`, itération de `map` dont l'ordre influence une décision, goroutine dont l'ordonnancement change un résultat *au sein d'un bind*.
- **Un endpoint d'observabilité mutant** (`POST`/`PATCH`/`PUT`/`DELETE`) — l'invariant read-only interdit tout verbe non-`GET`.
- **Une `response_rules` arbitraire construite en dur** dans le code au lieu d'un profil du catalogue — les scénarios sont prédéfinis.
- **Un chemin de reconfiguration runtime** (setter sur la config chargée, endpoint de config) — la config est immuable après le boot.
- **Un label Prometheus à cardinalité non bornée** (MSISDN, `message_id`, contenu).

---

## 8. Configuration du linter (source de vérité)

**[MUST]** `golangci-lint` avec `.golangci.yml` versionné ; le CI échoue sur toute alerte. Ensemble minimal (aligné sur la passerelle, sans les linters SQL/HTTP inutiles ici) :

```yaml
version: "2"
run:
  timeout: 5m
linters:
  enable:
    - govet
    - staticcheck
    - revive          # naming / style
    - errcheck
    - ineffassign
    - unused
    - unconvert
    - misspell
    - gocritic
    - gosec           # no leaked secrets
    - contextcheck
    - nakedret
    - prealloc
  settings:
    revive:
      rules:
        - name: exported
        - name: var-naming
        - name: receiver-naming
        - name: context-as-argument
        - name: error-strings
formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/martialanouman/go-smsc-simulator
issues:
  max-same-issues: 0
```

> **Schéma v2.** Ce fichier suit le schéma **golangci-lint v2** (version épinglée : `v2.3.0`, cf. `Makefile`). Différences avec le v1, si tu croises une ancienne config : la clé `version: "2"` est obligatoire ; `gofmt`/`goimports` sont des **formatters**, plus des linters, et vivent dans un bloc `formatters` séparé ; `linters-settings` devient `linters.settings` ; `local-prefixes` prend une **liste**, plus une chaîne. Une config v1 est rejetée au chargement par la v2.

*(Pas de `bodyclose`/`rowserrcheck`/`sqlclosecheck`/`noctx` : ni SQL, ni client HTTP sortant sur les chemins chauds.)*

---

## 9. Résumé — la revue de style en dix points

Un relecteur vérifie, pour le style seul : `gofmt`/`goimports` verts ; nommage `MixedCaps` avec casse d'acronymes correcte (`smppPDU`, `virtualSMSC`, `smscID`) ; vocabulaire du domaine cohérent (`perBindClock`, `logicalClock`) ; pas de bégaiement ni de `util`/`manager` ; énumérations typées **exactement** alignées sur le `.yml` ; tags `yaml` en `snake_case` corrects, champs optionnels en pointeur, secrets de bind non exposés ; **aucune source de non-déterminisme sur un chemin graîné** (pas de `time.Now()`/PRNG global) ; aucun endpoint mutant ; early-return sans `else` superflu ; aucun anti-idiome du §7. Le fond (concurrence, déterminisme, tests) relève du plan d'exécution et de la stratégie de test.

---

## 10. Commits (Conventional Commits) [MUST]

Les messages de commit suivent **[Conventional Commits](https://www.conventionalcommits.org/)** : `type(scope): description`.

- **Message intégralement en anglais** — type *et* description. C'est une **exception assumée** à la règle « prose en français » du §0 : le commit s'aligne sur le code et les identifiants, déjà en anglais, et le lint de titre de PR l'exige.
- **Types autorisés** : `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`, `perf`, `build`, `style`, `revert`.
- **Scope** = composant `internal/` ou l'ancien id de jalon : `feat(scenario): …`, `feat(s3): …`.
- **Impact semver** (le dépôt merge en **squash** — le **titre de PR** devient le commit sur `main` et pilote la version) : `fix`/`perf` → **patch**, `feat` → **minor**, un `!` après le type/scope ou un footer `BREAKING CHANGE:` → **major**. Les types `docs`/`chore`/`ci`/`test`/`refactor`/`style` seuls ne déclenchent **aucune** release.
- **Vérifié en CI** : le job `pr-title` (`.github/workflows/ci.yml`) refuse un titre non conforme ; le job `release` calcule le tag semver et publie la GitHub Release à chaque merge sur `main` (`svu` + `.goreleaser.yml`).

Conforme : `feat: generate release and tag on merge to main` · `fix(smpp): reject a malformed bind PDU length`.
Non conforme : `feat: génère la release` (français) · `ajoute la release` (pas de type) · `Update main.go` (pas de type, pas conventionnel).

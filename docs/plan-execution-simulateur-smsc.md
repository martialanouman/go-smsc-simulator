# Plan d'exécution — Implémentation du simulateur SMSC

**Composant :** Simulateur SMSC configurable (Go) — outil de test/CI
**Spécification de référence :** `specification-technique-simulateur-smsc.md` (v3.0)
**Statut :** Plan d'exécution v1.0
**Méthode :** tranche verticale MVP d'abord (walking skeleton SMPP), puis épaississement capacité par capacité.
**Contexte outil :** implémentation assistée par **Claude Code CLI**.

> Chaque jalon (`S0`…`S7`) précise : **Objectif**, **Dépend de**, **Livrables** (packages/fichiers/endpoints concrets), **Nouvelles dépendances**, **Hors périmètre** (ce qui n'est explicitement PAS fait ici) et **Critères d'acceptation** (tests). Pas d'estimation en jours : on pilote à l'acceptation. Les **conventions transverses (§1)** fixent une fois pour toutes les points récurrents (module, bibliothèques, ports, nommage, déterminisme). Les jalons sont volontairement **petits et revoyables** : un jalon = un lot de PR verticalement démontrable, validable par un humain en une revue.

---

## 0. Comment exécuter ce plan avec Claude Code CLI

### 0.1 La boucle par tâche

Une tâche = **une session Claude Code ciblée = une PR petite et verte**. Pour chaque tâche, donne à l'agent : (1) le **contexte** (réf de spec `§6.x`, le fichier de fixture `.yml` cible, le package cible) ; (2) le **livrable** (recopié depuis ce plan) ; (3) les **critères d'acceptation** (recopiés — ce sont des tests). Demande d'**écrire les tests en même temps que le code**. Termine par la *definition of done* (§0.4).

### 0.2 `CLAUDE.md` (à la racine du repo simulateur)

Claude Code le lit à chaque session : commandes, carte d'architecture, règles d'or, invariants, index docs. Garde-le à jour.

### 0.3 Règle d'or du séquencement + convention STUB

On construit un **squelette qui marche** (`S2`) le plus tôt possible, puis **chaque jalon épaissit une capacité sans casser le flux de bout en bout**. Une capacité non encore implémentée est un **pass-through explicitement marqué** :

```go
// STUB S3: fault injection — always succeeds with fixed latency until S3. See plan §6.
```

Un STUB reste **déterministe** et couvert par les tests d'invariant. Il n'est jamais silencieux, jamais un `TODO` anonyme.

### 0.4 Definition of Done (chaque PR)

`gofmt`/`goimports` verts • `golangci-lint` sans alerte • `go test -race ./...` vert • `govulncheck` vert • critères d'acceptation couverts par des tests • aucun invariant violé (§0.5) • godoc sur l'exporté • PR focalisée sur une tâche.

### 0.5 Les 4 invariants (tests bloquants, verts à vie)

**(a) Déterminisme par bind** — à `seed` fixe, deux exécutions de la même fixture produisent la **même séquence** de résultats, latences (en ticks), DLR et MO **par session de bind** (posé à `S3`, étendu à chaque jalon temporel). **(b) Config = source unique, fail-fast** — toute config invalide échoue **au chargement, avant d'ouvrir le moindre port** ; aucun chemin de reconfiguration runtime n'existe (posé à `S1`). **(c) HTTP strictement read-only** — aucun endpoint mutant ; la surface d'observabilité ne modifie jamais l'état du simulateur (posé à `S2`). **(d) Flush de quiescence** — une planification en ticks laissée au repos est drainée, jamais figée : un lot soumis puis silence délivre ses DLR/MO en attente dans l'ordre de tick (posé à `S4`).

### 0.6 Documents de référence (source de vérité)

Contrat de configuration : le **schéma `.yml`** de la spec §3.1 (les fixtures `examples/*.yml` en sont l'incarnation testée). Prose : `specification-technique-simulateur-smsc.md` (quoi/pourquoi), `convention-style-go-simulateur-smsc.md` (style Go), `strategie-de-test-simulateur-smsc.md` (tests). La spec de la passerelle (`specification-technique-passerelle-sms.md`) est le **système sous test** : ses §6.4/§6.13/§6.15 décrivent les mécanismes de résilience que les profils exercent.

---

## 1. Conventions transverses (à fixer AVANT S0)

Ces choix sont fixés une fois ; tous les jalons s'y conforment. Les changer plus tard est une décision d'équipe.

### 1.1 Module, repo & versions

- **Repo séparé de la passerelle.** Décision déjà actée côté passerelle (`plan-execution-passerelle.md` §1.8/§18, `CLAUDE.md`) : le simulateur est *un projet/binaire externe, pas un module Go de la passerelle*. Il a son propre dépôt, son cycle de release, zéro dépendance vers le code de la passerelle.
- **Module Go :** `github.com/martialanouman/go-smsc-simulator` (**acté à S0**, aligné sur le nom du dépôt). Figure dans `go.mod`, le `local-prefixes` de goimports/`.golangci.yml`, et tous les imports internes.
- **Go :** 1.23+ (pour `slog`, `math/rand/v2`, `go test -fuzz`, generics matures).

> **Point de décision — partage du codec SMPP.** La spec §1.3 souhaite « partager le codec de PDU SMPP » avec la passerelle. Comme le codec de la passerelle vit dans `internal/smpp` (non importable hors module) et que le simulateur est un **repo séparé**, le partage direct est impossible aujourd'hui. Deux options :
> - **(retenue par défaut)** le simulateur implémente son **propre codec SMPP côté serveur** — un sous-ensemble focalisé (décoder `bind_*`/`submit_sm`/`enquire_link`/`unbind`, encoder `*_resp`/`deliver_sm`). Autonomie totale, léger risque de dérive avec la passerelle.
> - **(à envisager)** extraire le codec dans un **module public partagé** (`github.com/martialanouman/go-smpp`) importé par les deux. Source unique, au prix d'un refactor côté passerelle et d'un module de plus à maintenir.
>
> Le plan est écrit pour l'option retenue ; passer à la seconde ne change que `S2` (on importe au lieu d'écrire le codec). À trancher avant `S2`.

### 1.2 Bibliothèques (dépendances Go) — minimalisme assumé

Le simulateur a **zéro dépendance d'infrastructure** (pas de Kafka/Postgres/Redis/ClickHouse — spec §1.2). La liste de dépendances est donc courte. Aucune autre bibliothèque pour ces rôles sans décision d'équipe.

| Rôle | Bibliothèque |
|---|---|
| Parsing YAML | `gopkg.in/yaml.v3` |
| Routeur HTTP (surface read-only) | `net/http` (stdlib) — `go-chi/chi/v5` toléré si le routage grossit |
| Métriques | `github.com/prometheus/client_golang` |
| UUIDv7 | `github.com/google/uuid` (`uuid.NewV7`) |
| PRNG déterministe | `math/rand/v2` (stdlib) |
| Logs | `log/slog` (stdlib) |
| Codec SMPP | **interne** (`internal/smpp`, aucune lib externe) |

*(Pas d'OTel : le simulateur est un pair de test, ses métriques Prometheus suffisent. Pas de testcontainers : aucune dépendance externe à monter.)*

### 1.3 Outillage à installer (binaires, hors `go.mod`)

- **Go 1.23+** et **Docker** (image de distribution + exemple `docker-compose.yml`).
- **golangci-lint**, **govulncheck** (`golang.org/x/vuln/cmd/govulncheck`).
- Un `make tools` installe les binaires Go via `go install`.

### 1.4 Ports

- **SMPP par SMSC virtuel** : chaque SMSC virtuel écoute sur **son** port, défini dans le `.yml` (convention d'exemple : `2775`, `2776`, …).
- **Observabilité (read-only)** : un port HTTP unique pour tout le processus, défini par `observability.http_port` du `.yml` (convention : `9000`). Sert `/health`, l'inspection read-only et `/metrics`. **Bloc `observability` omis → aucun serveur HTTP** (mode « boîte noire », spec §5.2).
- **Préfixe de chemin (acté à S0)** : `/health` et `/metrics` sont **nus**, sans préfixe ; le `/v1` de la spec §5.2 ne couvre que les endpoints d'**inspection** livrés à `S2` (`/v1/virtual-smscs`, …). Motif : les sondes de liveness et les scrapers Prometheus attendent des chemins conventionnels et non versionnés, et l'inspection est la seule surface dont le contrat mérite une version.

### 1.5 Déterminisme (le cœur du produit — spec §6.3)

- **Une PRNG par SMSC virtuel**, graine = `seed` du `.yml`. `seed` absent ⇒ mode **chaos** (PRNG non graînée, `clock: wallclock` autorisé).
- **`per_bind_clock`** : compteur monotone de `submit_sm` **par session de bind** — la **référence de timing déterministe** (DLR, `spike`, MO, déconnexions et transitions planifiées y sont ancrés).
- **`logical_clock`** : compteur global par SMSC virtuel — **observable d'assertion uniquement** (`GET /logical-clock`), jamais une référence de planification (son ordre entre binds concurrents n'est pas reproductible).
- **Garantie scopée par bind** : la reproductibilité est garantie *au sein d'un bind*. Une assertion à ordre global épingle `bind_pool_size = 1` côté passerelle.
- **Jamais d'horloge murale** en mode graîné : tout mécanisme temporel lit `per_bind_clock`. `math/rand/v2` graîné, jamais `time.Now()` ni `Math.random`, sur les chemins déterministes.

### 1.6 Nommage & emplacements

Binaire unique = `cmd/smsc-simulator/main.go`. Code métier sous `internal/` (jamais importable dehors). Fixtures d'exemple sous `examples/*.yml`. Clés YAML : `snake_case` (comme la spec §3.1) ; identifiants Go : `MixedCaps` (convention de style §2). Acronymes à casse homogène : `SMPP`, `PDU`, `TON`, `NPI`, `TLV`, `UDH`, `DLR`, `MO`, `MT`, `TLS`, `MSISDN`.

### 1.7 Journalisation du contenu (nuance vs passerelle)

Le **PDU Recorder retient volontairement le contenu** des `submit_sm` reçus — c'est *la fonctionnalité* (vérifier ce que la passerelle a envoyé). Ce n'est donc pas un secret à masquer comme dans la passerelle. En revanche, les **logs `slog`** ne déversent pas le contenu brut au niveau `info` : le contenu s'inspecte via `GET /received-pdus`, pas via les logs. Pas d'invariant « corps jamais sérialisé » ici — l'inspection de contenu est le but.

---

## 2. Le squelette qui marche (S2)

```
Gateway connector-pool-svc ──bind──► Virtual SMSC (SMPP Server Engine)
        │  submit_sm                        │  submit_sm_resp (100% OK, healthy)
        └──────────────────────────────────┘
                                            │  append
                                            ▼
                                    PDU Recorder (ring buffer)
                                            │
                                            ▼
                            GET /received-pdus  ◄── test d'assertion
```

Dès que la passerelle (ou un client SMPP de test) se binde, soumet, reçoit `ESME_ROK`, et que la PDU est visible via `GET /received-pdus`, l'architecture est prouvée. Les jalons `S3`+ s'y greffent.

---

## 3. Vue d'ensemble des jalons

| Jalon | Objectif | Débloque |
|---|---|---|
| **S0** | Fondations : repo, CI, squelette binaire, plomberie de config, observabilité de base | tout |
| **S1** | Config `.yml` déclarative complète + validation fail-fast | de quoi décrire une topologie |
| **S2** | **Squelette SMPP vertical** (bind → `submit_sm` → `healthy` → PDU recorder → inspection read-only) | l'architecture prouvée |
| **S3** | Moteur de scénario (6 profils paramétrés) + injecteur de panne + déterminisme graîné | résultats/latences configurables |
| **S4** | DLR asynchrones + horloge logique + flush de quiescence | voie DLR déterministe |
| **S5** | MO planifiés + déconnexions & transitions de scénario planifiées | scénarios de résilience scriptés |
| **S6** | Multi-SMSC + TLS par instance + métriques Prometheus | topologies multi-connecteurs |
| **S7** | Cas limites protocolaires (opt-in) + fuzz + packaging CI/CD + charge/NFR | outil prêt pour la CI et la charge |

---

## 4. S0 — Fondations & outillage

**Objectif :** un dépôt qui build/teste/lint, un binaire qui démarre à partir d'un `--config`, et les primitives transverses (config, observabilité).
**Dépend de :** —

**Livrables**

- `go.mod`/`go.sum` (module §1.1) ; `Makefile` (cibles : `tools`, `build`, `test`, `lint`, `vuln`, `run CONFIG=`, `docker`).
- `.golangci.yml` (aligné sur `convention-style-go-simulateur-smsc.md`, `local-prefixes` = module).
- CI (`.github/workflows/ci.yml` ou équivalent) : `lint`, `go test -race`, `govulncheck`, `build`.
- `cmd/smsc-simulator/main.go` **squelette canonique** : flag `--config`, `signal.NotifyContext(SIGTERM)`, chargement config (stub), init observabilité, démarrage du serveur read-only, blocage jusqu'à `SIGTERM`, arrêt gracieux (fermeture des listeners, drain).
- `internal/config` : type `Config` + `Load(path string)` (lecture fichier + parse YAML), **validation au boot** (le seul `log.Fatal` toléré) — à ce stade, valide juste « fichier lisible + YAML syntaxiquement correct ».
- `internal/observability` : init `slog` JSON ; registre Prometheus ; **serveur HTTP read-only réutilisable** exposant `/health` (les autres endpoints arrivent à `S2`), désactivé si `observability` absent.

**Nouvelles dépendances :** yaml.v3, prometheus/client_golang, slog (stdlib).

**Hors périmètre :** aucun SMPP, aucun scénario, aucun endpoint au-delà de `/health`, aucune validation métier de la config.

**Critères d'acceptation**

- `make build/test/lint/vuln` verts.
- `make run CONFIG=examples/minimal.yml` démarre ; `GET :9000/health` → 200.
- **Invariant (b) amorcé** : `--config` absent ou fichier illisible/YAML invalide → **sortie non nulle** avec un message d'erreur explicite, **avant** toute ouverture de port.
- Un `SIGTERM` provoque un arrêt gracieux (test : le process rend la main proprement).

---

## 5. S1 — Config `.yml` déclarative complète + validation fail-fast

**Objectif :** parser et **valider intégralement** le schéma `.yml` de la spec §3.1, matérialiser les SMSC virtuels en mémoire (pas encore servis).
**Dépend de :** S0.

**Livrables**

- `internal/config` complet : structs pour `observability`, `virtual_smscs[]` (name, port, `bind_credentials`, TON/NPI, `address_range`, `tls`, `seed`, `pdu_buffer_size`, `throughput_limit_per_sec`), `scenario` (`profile`, `params`, `latency`, `dlr`, `protocol_edge_cases_enabled`), `mo_injection`, `scheduled_disconnects[]`, `scheduled_transitions[]`.
- **Validation fail-fast** (spec §3.1) : `profile` inconnu → erreur ; `clock: wallclock` avec un `seed` défini → erreur ; port en doublon → erreur ; paramètre hors bornes → erreur ; référence de `to_profile` inconnue → erreur. Chaque erreur nomme le champ fautif.
- `examples/*.yml` : fixtures **valides** couvrant chaque profil. Les fixtures **invalides** (une par règle de validation) vivent dans `internal/config/testdata/` — **acté à S0** : `examples/` ne contient que des configurations démontrables (`make run CONFIG=…`) et un test itère le dossier en exigeant que tout y charge, ce qui interdit d'y ranger des fixtures cassées.
- `internal/config` expose un modèle **immuable** après `Load` (aucun setter).

**Nouvelles dépendances :** aucune.

**Hors périmètre :** aucun comportement runtime (pas de listener, pas de PDU) — uniquement charger, valider, matérialiser.

**Critères d'acceptation**

- Table-driven tests : chaque fixture valide charge sans erreur ; chaque fixture invalide **échoue au chargement** avec l'erreur spécifique attendue.
- **Invariant (b)** : la validation complète s'exécute **avant** toute ouverture de port ; un `.yml` invalide n'ouvre aucun listener.
- Tous les `examples/*.yml` valides passent (test qui itère le dossier).
- Le modèle chargé est immuable (aucune API de mutation exposée — vérifié par revue + absence de setter).

---

## 6. S2 — Squelette SMPP vertical (bind → submit_sm → healthy → recorder → inspection)

**Objectif :** le walking skeleton de bout en bout. **Jalon le plus important.** Un client SMPP se binde à un SMSC virtuel, soumet, reçoit `ESME_ROK`, la PDU est inspectable.
**Dépend de :** S0, S1.

**Livrables**

- `internal/smpp` : **codec PDU côté serveur** SMPP v3.4 — décodage `bind_transmitter`/`bind_receiver`/`bind_transceiver`, `submit_sm`, `enquire_link`, `unbind` ; encodage `bind_*_resp`, `submit_sm_resp`, `deliver_sm`, `enquire_link_resp`, `unbind_resp` ; support TLV/UDH et payload > 254 o. Round-trip testé. *(Ou import du module partagé si le point de décision §1.1 bascule.)*
- `internal/smsc` : **SMPP Server Engine** — un listener TCP par SMSC virtuel, **une goroutine lecture + une écriture par connexion** communiquant par canaux, machine à états de session (`open → bound → unbinding → closed`), auth de bind (`system_id`/`password` du `.yml`, temps constant), `enquire_link`, `unbind` gracieux.
- `per_bind_clock` (par session) + `logical_clock` (par SMSC virtuel), incrémentés à chaque `submit_sm`.
- `internal/recorder` : **tampon circulaire borné** (`pdu_buffer_size`) des `submit_sm` reçus, interrogeable.
- `internal/scenario` : moteur minimal, **profil `healthy` uniquement** (100 % succès, latence fixe basse) ; les autres profils sont des **STUB marqués** repliant sur `healthy`.
- **Surface read-only** (spec §5.2) : `GET /health`, `/virtual-smscs`, `/virtual-smscs/{id}/received-pdus` (filtres `sourceAddr`/`destAddr`/`since`, paginé), `/virtual-smscs/{id}/binds`, `/virtual-smscs/{id}/logical-clock`. **Aucun verbe mutant.**

**Nouvelles dépendances :** google/uuid (identifiants de session/PDU). *(Codec SMPP = interne.)*

**Hors périmètre :** un seul SMSC virtuel servi (multi-instance à `S6`) ; aucune injection de panne (latence fixe, jamais d'erreur/timeout/disconnect) ; pas de DLR, pas de MO, pas de transitions ; pas de TLS ; pas de `/metrics` ; pas de PDU malformées.

**Critères d'acceptation**

- Test bout-en-bout (client SMPP in-process) : bind → `submit_sm` → `ESME_ROK` ; `enquire_link` → `enquire_link_resp` ; `unbind` gracieux libère le bind (disparaît de `GET /binds`).
- La PDU soumise est visible via `GET /received-pdus` (adresses, contenu, TON/NPI, codage corrects).
- `per_bind_clock` et `logical_clock` incrémentent ; `GET /logical-clock` reflète le compte.
- **Invariant (c)** : un test vérifie qu'**aucun** endpoint HTTP n'accepte `POST`/`PATCH`/`PUT`/`DELETE` (405/404), et qu'aucun n'altère l'état.
- Round-trip du codec SMPP testé unitairement (encode∘decode = identité sur un corpus de PDU).

---

## 7. S3 — Moteur de scénario + injecteur de panne + déterminisme

**Objectif :** activer les 6 profils prédéfinis paramétrés et l'injection de panne (résultats pondérés, latences), avec un déterminisme graîné vérifiable.
**Dépend de :** S2.

**Livrables**

- `internal/scenario` : **catalogue figé** des 6 profils (`healthy`, `flaky-carrier`, `throttling-carrier`, `dead-carrier`, `slow-carrier`, `throughput-capped`) avec leurs paramètres exposés (spec §6.1) ; sélection de résultat **pondérée** (success / error+`errorCode` / timeout / disconnect) ancrée sur `(seed, per_bind_clock)`.
- `internal/fault` : **injecteur de panne** — distributions de latence `fixed`/`uniform`/`normal`(≥0)/`spike` (intervalle `spike` en ticks) ; **timeout** = rétention de `submit_sm_resp` ; **disconnect** = coupure TCP avant/après réponse selon config. Application du plafond `throughput_limit_per_sec` (→ `ESME_RTHROTTLED`).
- `internal/rng` : PRNG `math/rand/v2` **par SMSC virtuel / par bind**, graine dérivée de `seed`. Mode chaos si `seed` absent.

**Nouvelles dépendances :** aucune (`math/rand/v2` stdlib).

**Hors périmètre :** DLR (`S4`), MO et déconnexions/transitions **planifiées** (`S5`) ; le flush de quiescence (`S4`). Ici les pannes sont synchrones sur `submit_sm`.

**Critères d'acceptation**

- `throttling-carrier`/`throughput-capped` : au-delà du plafond → `ESME_RTHROTTLED` ; en deçà → succès.
- `dead-carrier` : selon `mode`, refuse le bind (`ESME_RBINDFAIL`) **ou** fait timeout sur chaque `submit_sm`.
- `slow-carrier` : latence bornée (2–4 s) appliquée, aucune erreur.
- `flaky-carrier` : mix succès/erreur dans la tolérance statistique sur N messages.
- **Invariant (a)** : à `seed` fixe, deux exécutions produisent la **même séquence** de résultats et de latences (en ticks) **par bind** (test de rejeu comparant deux runs). Sans `seed`, le mode chaos ne prétend qu'à la reproductibilité de séquence/contenu, pas de timing.

---

## 8. S4 — DLR asynchrones + horloge logique + flush de quiescence

**Objectif :** générer des DLR asynchrones corrélés, ancrés au `per_bind_clock`, et garantir qu'une planification au repos ne se fige pas.
**Dépend de :** S3.

**Livrables**

- `internal/schedule` : **Schedule Runner** par bind — `pending_logical_schedule` (ensemble ordonné d'événements dus à un tick futur), drainé (a) à l'atteinte du tick en fonctionnement normal, ou (b) par un **flush de quiescence** après `quiescence_flush_ms` (défaut 250 ms) sans nouveau `submit_sm`, dans l'ordre de tick déterministe.
- **DLR Scheduler** : pour un `submit_sm` « soumis » avec succès, planifie un `deliver_sm` DLR ; délai ancré au tick du `submit_sm` d'origine (`dlr.delay`) ; mix de résultats `delivered`/`failed`/`expired` (`outcome_weights`) ; **corrélation** au message d'origine (l'ID SMSC attribué au `submit_sm_resp` est référencé par le DLR).
- `GET /logical-clock` reste l'observable global (déjà à `S2`).

**Nouvelles dépendances :** aucune.

**Hors périmètre :** MO et déconnexions/transitions planifiées (`S5`) — bien qu'elles réutilisent le même Schedule Runner. Pas de TLS ni de métriques.

**Critères d'acceptation**

- **Invariant (d)** : soumettre un lot puis cesser le trafic → les DLR en attente sont **délivrés** après le flush de quiescence, dans l'ordre de tick (test : batch + silence, on observe les `deliver_sm` DLR).
- **Invariant (a) étendu** : à `seed` fixe, la séquence de résultats DLR (`delivered`/`failed`/`expired`) et leur ordre de tick sont reproductibles.
- Corrélation : chaque DLR référence l'ID SMSC du `submit_sm` d'origine (test de corrélation sur l'`smsc_msg_id`).
- Un DLR dont le message d'origine est inconnu/expiré est journalisé + compté, jamais émis en silence sur un mauvais mapping.

---

## 9. S5 — MO planifiés + déconnexions & transitions de scénario planifiées

**Objectif :** compléter les trois formes déclaratives d'événements temporels, toutes drainées par le Schedule Runner.
**Dépend de :** S4.

**Livrables**

- **MO Injector** : `mo_injection` mode `scheduled` (événements `at_tick` avec `source_addr`/`dest_addr`/`content`) ou `auto` (`rate_per_sec` + `content_template`) ; `deliver_sm` MO émis via le Schedule Runner ; `clock: logical` imposé si `seed`, `wallclock` seulement en chaos.
- **Déconnexions planifiées** : `scheduled_disconnects[]` (`at_tick`, `scope` = all|oldest|random, `when` = before_response|after_response) — coupe les binds au tick prévu.
- **Transitions de scénario planifiées** : `scheduled_transitions[]` (`at_tick`, `to_profile`) — `active_scenario` avance **uniquement** par ces transitions ; aucun chemin de mutation runtime. Le pattern `healthy → dead-carrier → healthy` (ouvrir puis refermer le disjoncteur de la passerelle) est ainsi reproductible.

**Nouvelles dépendances :** aucune.

**Hors périmètre :** TLS et métriques (`S6`) ; PDU malformées (`S7`).

**Critères d'acceptation**

- MO `scheduled` : un `deliver_sm` MO est émis au bon tick, contenu conforme, **reproductible** à `seed` fixe.
- MO `auto` : débit approximatif respecté ; en mode graîné, ancré aux ticks (pas d'horloge murale).
- Transition : un test « `healthy` (ticks 0–199) → `dead-carrier` (200–399) → `healthy` (400+) » produit exactement les résultats attendus à chaque plage, **reproductible**.
- Déconnexion planifiée : au tick prévu, les binds ciblés (`scope`) sont coupés selon `when` ; visible dans `GET /binds`.
- **Invariant (d)** : MO/transitions/déconnexions en attente au repos sont drainés par le flush de quiescence.

---

## 10. S6 — Multi-SMSC + TLS + métriques Prometheus

**Objectif :** un processus héberge plusieurs SMSC virtuels indépendants ; TLS optionnel par instance ; métriques exportées.
**Dépend de :** S5.

**Livrables**

- **Multi-instance** : le processus instancie tous les `virtual_smscs` du `.yml`, un listener SMPP par port, comportements/scénarios/horloges **indépendants** ; un crash d'un SMSC virtuel n'affecte pas les autres (isolement de goroutines, `recover` de dernier ressort par SMSC virtuel).
- **TLS par SMSC virtuel** : bloc `tls`, **génération intégrée d'un certificat auto-signé** si activé et aucun cert fourni (reflète `tls_enabled` du connecteur passerelle).
- `internal/metrics` + `GET /metrics` : compteurs/histogrammes Prometheus **par SMSC virtuel** (binds actifs, `submit_sm` reçus, résultats servis par type, scénario actif, latence servie), **labels bornés** (`virtual_smsc`, `bind_type`, `outcome`, `scenario`) — **jamais** de MSISDN/`message_id`/contenu en label.

**Nouvelles dépendances :** aucune (`crypto/tls`, `crypto/x509` stdlib ; prometheus déjà là).

**Hors périmètre :** PDU malformées et packaging CI/CD (`S7`).

**Critères d'acceptation**

- Un `.yml` à 3 SMSC virtuels → 3 listeners indépendants ; un scénario `dead-carrier` sur l'un n'affecte pas un `healthy` sur l'autre (test multi-instance).
- Bind TLS réussi avec certificat auto-signé généré ; bind non-TLS refusé si `tls.enabled` (et inversement).
- `GET /metrics` expose les compteurs par SMSC virtuel ; **test de garde** échouant si un label à cardinalité non bornée (MSISDN/`message_id`) apparaît.

---

## 11. S7 — Cas limites protocolaires + fuzz + packaging CI/CD + charge

**Objectif :** durcir le parsing, packager pour la CI, valider les NFR de débit et de démarrage.
**Dépend de :** S6.

**Livrables**

- **Injection de cas limites protocolaires** (opt-in, spec §6.1) : `protocol_edge_cases_enabled` active des PDU malformées (longueur invalide, `command_id` invalide, numéros de séquence hors ordre) — **désactivé par défaut**.
- **Fuzz** (`go test -fuzz`) sur le **décodeur PDU** : aucune panique, aucune allocation non bornée sur entrée hostile.
- **Packaging** : `Dockerfile` (binaire statique, base minimale, `.yml` monté), `docker-compose.yml` d'exemple câblant le simulateur comme `smsc_connectors` de la passerelle, modèle de **Job Kubernetes** (`deploy/`).
- **Charge & NFR** : profil `throughput-capped`/`healthy` à fort débit ; valider **≥ 15 000 msg/s par SMSC virtuel**, **démarrage à froid < 2 s**, **< 50 Mo** de base par SMSC virtuel au repos. Vérifier que le **déterminisme par bind tient sous charge multi-bind** (agrégation statistique conservée).

**Nouvelles dépendances :** aucune (un générateur de charge SMPP externe ou le harnais de la passerelle, hors `go.mod`).

**Hors périmètre :** rien de nouveau — c'est le jalon de durcissement/sortie.

**Critères d'acceptation**

- Le profil de cas limites n'injecte des PDU malformées **que** lorsque `protocol_edge_cases_enabled` ; sinon parsing strict.
- `go test -fuzz` sur le décodeur : aucune panique sur le corpus généré (durée bornée en CI).
- L'image Docker démarre à froid **< 2 s** et sert des binds ; `docker-compose up` branche le simulateur comme connecteur indistinguable d'un vrai SMSC.
- Charge : **≥ 15 000 msg/s** soutenu par SMSC virtuel avec latence servie conforme ; déterminisme **par bind** vérifié pendant un run multi-bind.

---

## 12. Graphe de dépendances

```
S0 ─► S1 ─► S2 ─► S3 ─► S4 ─► S5 ─► S6 ─► S7
                  │      └─────────────┐
                  └── déterminisme (a) posé, étendu à chaque jalon temporel (S4, S5)
```

`S2` est le point de bascule : avant, on outille (repo, config) ; après, chaque jalon épaissit un SMSC virtuel vivant. Le **codec SMPP** (`internal/smpp`) est livré à `S2` et étendu marginalement à `S7` (PDU malformées). `S4` et `S5` partagent le **Schedule Runner** (`internal/schedule`) : `S4` le pose pour les DLR, `S5` y greffe MO/déconnexions/transitions. Le déterminisme (invariant a) est posé à `S3` puis **ré-affirmé** à chaque jalon qui ajoute un mécanisme temporel.

---

## 13. Le test harness (transversal)

Le simulateur n'a **aucune dépendance d'infrastructure** : ses tests sont du **Go pur** + un **client SMPP in-process** (réutilisant `internal/smpp` en mode client pour piloter le serveur). Pas de `testcontainers`. Les piliers :

- **Tests unitaires** table-driven : codec PDU (round-trip), sélection de résultat pondérée, distributions de latence, validation de config.
- **Tests de déterminisme** (invariant a) : rejeu — deux exécutions à `seed` fixe, comparaison des séquences par bind. Le plus important du projet.
- **Tests de flush de quiescence** (invariant d) : batch + silence → drain observé.
- **Tests read-only** (invariant c) : aucun verbe mutant accepté.
- **Fuzz** (`go test -fuzz`) sur le décodeur PDU (surface d'entrée non fiable).
- **Tests d'intégration** de bout en bout : client SMPP in-process pilotant un ou plusieurs SMSC virtuels, assertions via la surface read-only.

Détail complet : `strategie-de-test-simulateur-smsc.md`.

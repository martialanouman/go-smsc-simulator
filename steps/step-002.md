# Step 002 — S2 · Squelette SMPP vertical (bind → submit_sm → healthy → recorder → inspection)

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §6.
> **Statut : ⏳ À FAIRE — jalon le plus important (le walking skeleton).**

## Objectif

Prouver l'architecture de bout en bout : un client SMPP se binde à un SMSC virtuel, soumet un `submit_sm`, reçoit `ESME_ROK` (profil `healthy`), et la PDU devient **inspectable** via la surface read-only. Tout le reste (S3+) s'y greffe.

## Dépend de

S0, S1.

## Nouvelles dépendances

`github.com/google/uuid` (identifiants de session/PDU, `uuid.NewV7`). Codec SMPP = **interne**, aucune lib externe.
→ Passer par `ctx7` (Context7) pour la version et l'API `uuid.NewV7` avant `go get`.

## Découpage en tâches (PR fines, vertical d'abord)

### T1 — `internal/smpp` : codec PDU côté serveur (SMPP v3.4)
- **Header PDU** : `command_length`, `command_id`, `command_status`, `sequence_number` (encode/decode).
- **Décodage** : `bind_transmitter` / `bind_receiver` / `bind_transceiver`, `submit_sm`, `enquire_link`, `unbind`.
- **Encodage** : `bind_*_resp`, `submit_sm_resp`, `deliver_sm`, `enquire_link_resp`, `unbind_resp`, `generic_nack`.
- **C-Octet strings** (system_id, password, addresses), champs TON/NPI, short_message, **TLV** et **UDH**, payload > 254 o (`message_payload` TLV).
- Constantes `command_id` et `command_status` (`ESME_ROK`, `ESME_RTHROTTLED`, `ESME_RBINDFAIL`, `ESME_RINVCMDLEN`, `ESME_RINVCMDID`…).
- **Test round-trip** : `decode ∘ encode = identité` sur un corpus de PDU (table-driven).

### T2 — `internal/smsc` : SMPP Server Engine
- Un **listener TCP par SMSC virtuel** (S2 : un seul servi ; multi à S6).
- Par connexion : **une goroutine lecture + une goroutine écriture**, communiquant par canaux. L'état de session est **possédé par une seule goroutine** (pas de verrou sur la fenêtre — règle d'or CLAUDE.md).
- **Machine à états** : `open → bound → unbinding → closed`.
- **Auth de bind** : comparaison `system_id`/`password` du `.yml` en **temps constant** (`crypto/subtle.ConstantTimeCompare`).
- `enquire_link` → `enquire_link_resp` ; `unbind` gracieux → `unbind_resp` + fermeture.
- `context.Context` en 1er paramètre partout ; **condition d'arrêt** sur chaque goroutine (fermeture propre sur ctx annulé / SIGTERM).

### T3 — Horloges déterministes
- `per_bind_clock` : compteur monotone de `submit_sm` **par session de bind**.
- `logical_clock` : compteur **par SMSC virtuel** (observable d'assertion).
- Les deux incrémentés à chaque `submit_sm`.

### T4 — `internal/recorder` : tampon circulaire
- Ring buffer **borné** par `pdu_buffer_size` des `submit_sm` reçus.
- API de lecture (snapshot) avec filtres `sourceAddr` / `destAddr` / `since`, pagination.
- Thread-safe (lecture depuis les handlers HTTP, écriture depuis la goroutine de session).

### T5 — `internal/scenario` : moteur minimal
- **Profil `healthy` uniquement** : 100 % succès, latence fixe basse.
- Les 5 autres profils = **STUB marqué** repliant sur `healthy` :
  `// STUB S3: <profile> falls back to healthy until the scenario engine lands. See plan §7.`
- STUB **déterministe** et couvert par les tests d'invariant.

### T6 — Surface read-only (câblée dans `internal/observability`, spec §5.2)
Endpoints **GET uniquement** (le préfixe `GET ` du `ServeMux` fait le 405 structurel) :
- `GET /health` (déjà là)
- `GET /v1/virtual-smscs`
- `GET /v1/virtual-smscs/{id}/received-pdus` (filtres `sourceAddr`/`destAddr`/`since`, paginé)
- `GET /v1/virtual-smscs/{id}/binds`
- `GET /v1/virtual-smscs/{id}/logical-clock`
- Câbler l'Engine (T2) dans `main.go:run`, **après le boot gate**, pour instancier le SMSC virtuel `healthy` d'`examples/healthy.yml` et l'exposer à la surface.

## Hors périmètre (STUB explicites)

- Un seul SMSC virtuel servi (multi → S6).
- **Aucune injection de panne** : latence fixe, jamais d'erreur/timeout/disconnect (→ S3).
- Pas de DLR (S4), pas de MO ni transitions (S5), pas de TLS (S6), pas de `/metrics` (S6), pas de PDU malformées (S7).

## Critères d'acceptation (tests)

- [ ] **E2E** (client SMPP in-process réutilisant `internal/smpp` en mode client) : bind → `submit_sm` → `ESME_ROK` ; `enquire_link` → `enquire_link_resp` ; `unbind` gracieux libère le bind (disparaît de `GET /binds`).
- [ ] La PDU soumise est visible via `GET /received-pdus` (adresses, contenu, TON/NPI, codage corrects).
- [ ] `per_bind_clock` et `logical_clock` incrémentent ; `GET /logical-clock` reflète le compte.
- [ ] **Invariant (c)** : test que **aucun** endpoint n'accepte `POST/PATCH/PUT/DELETE` (405/404) et n'altère l'état.
- [ ] Round-trip codec testé unitairement (`encode∘decode = identité`).
- [ ] `go test -race ./...` vert (aucune data race sur le recorder / les horloges).

## Risques & points d'attention

- **Data races** sur le recorder et les horloges : le point le plus sensible du jalon (`-race` obligatoire). Préférer un design par canaux plutôt que des mutex éparpillés.
- **Fuites de goroutine** : chaque goroutine lecture/écriture doit sortir sur ctx annulé ET sur fermeture de connexion — tester avec `goleak` ou un compteur.
- **Décision §1.1** (codec interne vs module partagé) : à trancher **avant** T1. Le plan suppose l'option interne retenue.
- Modèle client in-process : réutiliser `internal/smpp` en encodage/décodage inversé — pas de vraie stack réseau externe.

## Definition of Done

Voir §0.4 du plan : gofmt/goimports, golangci-lint 0 alerte, `go test -race` vert, govulncheck vert, critères couverts par tests, godoc sur l'exporté, PR focalisée. Mettre à jour `CLAUDE.md` (carte d'architecture) si un package nouveau apparaît.

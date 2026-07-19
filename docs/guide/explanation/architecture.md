# Architecture du simulateur

> **Catégorie Diátaxis : Explication.** Cette page décrit *comment le simulateur est
> structuré* et *pourquoi* — la carte mentale des composants et le flux d'un
> `submit_sm`. Pour lancer le simulateur, voir
> [tutorials/01-premier-carrier.md](../tutorials/01-premier-carrier.md).

## Un binaire, N SMSC virtuels

Le simulateur est un **binaire Go unique** (`cmd/smsc-simulator`) qui héberge **N SMSC
virtuels** décrits dans le `.yml`, plus une **surface HTTP en lecture seule** pour
l'observabilité. Chaque SMSC virtuel :

- écoute sur **son propre port** SMPP ;
- a ses propres identifiants de bind, TON/NPI, TLS, profil de scénario et `seed` ;
- tourne **indépendamment** des autres (horloges, scénarios, état isolés).

Du point de vue de la passerelle sous test, chaque SMSC virtuel est **indistinguable
d'un vrai connecteur opérateur** : elle s'y binde comme à un vrai SMSC, sans aucun
changement de code.

![Vue d'ensemble : un binaire unique héberge N SMSC virtuels (chacun avec SMPP Server
Engine, Scenario Engine, Fault Injector, Schedule Runner, per_bind_clock, PDU Recorder)
plus une API d'observabilité HTTP en lecture seule ; la passerelle sous test s'y binde en
SMPP, un bind par port.](images/architecture-overview.svg)

<sub>Source éditable : [`images/architecture-overview.mmd`](images/architecture-overview.mmd) (Mermaid).</sub>

## Les composants (`internal/`)

Tout le code métier vit sous `internal/` (jamais importable hors du module). Chaque
package a une responsabilité nette :

| Package | Responsabilité |
|---|---|
| `config` | Chargement + **validation fail-fast** du `.yml`. Aucune API de mutation. |
| `smpp` | **Codec PDU** côté serveur (décodage `bind_*`/`submit_sm`/`enquire_link`/`unbind`, encodage `*_resp`/`deliver_sm`) + machine à états de session. |
| `smsc` | **SMPP Server Engine** : listener TCP + goroutines par connexion. |
| `scenario` | Catalogue **figé** des 6 profils + sélection de résultat pondérée. |
| `fault` | Injection de latence, timeout, disconnect. |
| `schedule` | **Schedule Runner** : DLR/MO/déconnexions/transitions par tick + flush de quiescence. |
| `recorder` | Tampon circulaire borné des `submit_sm` reçus. |
| `rng` | PRNG graîné par bind. |
| `tlscert` | Certificat TLS auto-signé/chargé par instance, au boot. |
| `metrics` | Collecteurs Prometheus par SMSC virtuel, labels bornés. |
| `observability` | `slog` + serveur HTTP en lecture seule. |

## Le modèle de concurrence SMPP

Chaque connexion suit un modèle simple et sans verrou sur le chemin chaud :

- **une goroutine de lecture** + **une goroutine d'écriture** par connexion, communiquant
  par canaux ;
- l'**état de session** (fenêtre, séquence, `per_bind_clock`) est **possédé par une seule
  goroutine** — pas de verrou sur la fenêtre ;
- machine à états `open → bound → unbinding → closed` ;
- arrêt gracieux : sur `SIGTERM`, `unbind` propre des binds avant fermeture.

Cette discipline — *un seul propriétaire de l'état par session* — est ce qui garantit
`go test -race ./...` vert et le déterminisme par bind.

## Le flux d'un `submit_sm`

Voici le chemin complet d'une soumission, du décodage à l'enregistrement :

![Flux d'un submit_sm : décodage PDU → Scenario Engine (incrémente per_bind_clock et
logical_clock) → sélection de résultat pondérée graînée par (seed, per_bind_clock) →
Fault Injector → submit_sm_resp, qui déclenche à la fois un DLR planifié dans
pending_logical_schedule et l'enregistrement de la PDU dans le ring
buffer.](images/submit-sm-flow.svg)

<sub>Source éditable : [`images/submit-sm-flow.mmd`](images/submit-sm-flow.mmd) (Mermaid).</sub>

Aucune base externe n'intervient : **tout est en mémoire, borné, éphémère**. L'isolation
entre tests s'obtient en relançant le processus (démarrage < 2 s).

## Ce qui n'existe pas — par conception

Comprendre l'architecture, c'est aussi savoir ce qu'elle exclut délibérément :

- **Aucune API de configuration.** Un seul `.yml` au boot. Voir
  [pourquoi-config-declarative.md](pourquoi-config-declarative.md).
- **Aucun endpoint HTTP mutant.** La surface d'observabilité est en lecture seule
  (invariant c).
- **Aucune horloge murale sur un chemin déterministe** (sauf le plafond de débit). Voir
  [determinisme.md](determinisme.md).
- **Aucun scénario arbitraire.** Catalogue figé de 6 profils. Voir
  [scenarios-predefinis.md](scenarios-predefinis.md).
- **Aucune dépendance d'infrastructure** — pas de Kafka/Postgres/Redis/ClickHouse. Go
  pur, en mémoire.

## Voir aussi

- [reference/api-observabilite.md](../reference/api-observabilite.md) — les endpoints HTTP en détail.
- [explanation/determinisme.md](determinisme.md) — le cœur du produit.
- Documents de conception (source de vérité) :
  [specification-technique-simulateur-smsc.md](../../specification-technique-simulateur-smsc.md),
  [plan-execution-simulateur-smsc.md](../../plan-execution-simulateur-smsc.md).

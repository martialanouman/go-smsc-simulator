# Référence — API d'observabilité HTTP

> **Catégorie Diátaxis : Référence.** La surface HTTP est **strictement en lecture
> seule** : uniquement des `GET`. Tout verbe mutant (`POST`/`PUT`/`PATCH`/`DELETE`)
> reçoit **405**. C'est l'invariant (c). Le serveur n'existe que si le bloc
> `observability` figure dans le `.yml` ; l'omettre désactive entièrement le HTTP
> (mode « boîte noire »).

## Base et conventions

- Base : `http://<host>:<observability.http_port>` (convention : `9000`).
- `/health` et `/metrics` sont **nus** (sans préfixe). Les endpoints d'inspection sont
  préfixés **`/v1`** et **toujours scopés** par SMSC virtuel :
  `/v1/virtual-smscs/{id}/…`. Il n'existe pas de route d'inspection à la racine.
- `{id}` = le champ `name` du SMSC virtuel dans le `.yml`.
- Réponses d'inspection : `Content-Type: application/json`.
- Timeouts serveur : ReadHeader 5 s, Read 10 s, Write 10 s, Idle 60 s.

## Endpoints

| Méthode + chemin | Disponible si | Réponse |
|---|---|---|
| `GET /health` | toujours | `{"status":"ok"}` |
| `GET /v1/virtual-smscs` | inspection activée | tableau de `VirtualSMSCView` |
| `GET /v1/virtual-smscs/{id}` | inspection activée | un `VirtualSMSCView` (404 si `{id}` inconnu) |
| `GET /v1/virtual-smscs/{id}/received-pdus` | inspection activée | tableau de `RecordedPDUView` |
| `GET /v1/virtual-smscs/{id}/binds` | inspection activée | tableau de `BindView` |
| `GET /v1/virtual-smscs/{id}/logical-clock` | inspection activée | `{"logical_clock": <uint64>}` |
| `GET /metrics` | registre Prometheus actif | exposition Prometheus (texte) |

## `GET /health`

Liveness. Toujours `200` avec `{"status":"ok"}`.

## `GET /v1/virtual-smscs`

Liste des SMSC virtuels configurés et leur vue courante.

**`VirtualSMSCView`**

| Champ | Type | Sens |
|---|---|---|
| `name` | string | Nom du SMSC virtuel. |
| `port` | int | Port SMPP. |
| `active_profile` | string | Profil actuellement actif (avance via `scheduled_transitions`). |
| `bind_count` | int | Nombre de binds actifs. |
| `logical_clock` | uint64 | Compteur global de `submit_sm` traités. |
| `recorded_pdus` | int | Nombre de PDU actuellement dans le tampon. |

## `GET /v1/virtual-smscs/{id}`

Même `VirtualSMSCView` pour un seul SMSC virtuel. `404` avec
`{"error":"unknown virtual smsc: <id>"}` si `{id}` est inconnu.

## `GET /v1/virtual-smscs/{id}/received-pdus`

Journal des `submit_sm` reçus (tampon circulaire de taille `pdu_buffer_size`).

**Query params** (tous optionnels ; une valeur non parsable est traitée comme absente,
jamais rejetée) :

| Param | Type | Effet |
|---|---|---|
| `sourceAddr` | string | Filtre exact sur l'adresse source. |
| `destAddr` | string | Filtre exact sur l'adresse destination. |
| `since` | uint64 | Ne renvoie que les PDU dont le `per_bind_clock` est ≥ cette valeur. |
| `limit` | int | Nombre max de PDU. Défaut et plafond : **1000** (valeur ≤ 0 ou > 1000 → 1000). |

**`RecordedPDUView`**

| Champ | Type | Sens |
|---|---|---|
| `index` | int | Rang dans le tampon. |
| `message_id` | string | ID SMSC attribué (corrélation DLR). |
| `source_addr` | string | Adresse source. |
| `source_ton` / `source_npi` | int | TON/NPI source. |
| `dest_addr` | string | Adresse destination. |
| `dest_ton` / `dest_npi` | int | TON/NPI destination. |
| `data_coding` | int | Codage. |
| `short_message` | string (base64) | Contenu brut — un `[]byte` rendu en **base64** par le JSON. |
| `per_bind_clock` | uint64 | Tick du bind au moment de la réception. |

> Le contenu (`short_message`) est **volontairement** conservé — c'est la fonctionnalité
> d'assertion. Voir [how-to/inspecter-les-pdu-recues.md](../how-to/inspecter-les-pdu-recues.md).

## `GET /v1/virtual-smscs/{id}/binds`

Sessions de bind actuellement actives.

**`BindView`**

| Champ | Type | Sens |
|---|---|---|
| `id` | string | Identifiant de session. |
| `system_id` | string | `system_id` du client bindé. |
| `bind_type` | string | `transmitter` \| `receiver` \| `transceiver`. |
| `connected_at` | string (RFC 3339) | Instant de connexion. |

## `GET /v1/virtual-smscs/{id}/logical-clock`

`{"logical_clock": <uint64>}` — le compteur global de PDU traitées.

> Observable d'assertion **uniquement**. Ce n'est **pas** la référence de planification
> déterministe (celle-ci est `per_bind_clock`, par session). Voir
> [explanation/determinisme.md](../explanation/determinisme.md).

## `GET /metrics`

Format d'exposition Prometheus. Voir
[reference/metriques-prometheus.md](metriques-prometheus.md).

## Voir aussi

- [how-to/inspecter-les-pdu-recues.md](../how-to/inspecter-les-pdu-recues.md)
- [how-to/scraper-les-metriques.md](../how-to/scraper-les-metriques.md)
- [explanation/pourquoi-config-declarative.md](../explanation/pourquoi-config-declarative.md) — pourquoi read-only.

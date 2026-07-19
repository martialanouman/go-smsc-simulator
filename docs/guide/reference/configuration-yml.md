# Référence — Configuration `.yml`

> **Catégorie Diátaxis : Référence.** Description exhaustive et neutre du schéma de
> configuration. Le `.yml` est l'**unique** entrée de configuration, chargé et validé
> **une seule fois au démarrage** ; il n'existe aucune reconfiguration runtime. Pour
> *comprendre pourquoi*, voir
> [explanation/pourquoi-config-declarative.md](../explanation/pourquoi-config-declarative.md).
> Pour *apprendre à écrire une première config*, voir
> [tutorials/01-premier-carrier.md](../tutorials/01-premier-carrier.md).

## Règles générales de décodage

- Le décodeur YAML rejette **toute clé inconnue** (`KnownFields(true)`). Une faute de
  frappe dans un nom de champ est une erreur de chargement, pas un champ ignoré en
  silence. (Exception : les clés de *map*, comme les codes dans `error_mix`.)
- Un fichier **vide** est un `Config` valide (aucun SMSC virtuel, aucune observabilité).
- La validation agrège **toutes** les erreurs détectées et les renvoie ensemble,
  préfixées par `validate config <chemin>: …`.
- Les clés YAML sont en `snake_case`.

## Structure de haut niveau

```yaml
observability:            # bloc optionnel ; omis => aucun serveur HTTP (mode « boîte noire »)
  http_port: 9000

virtual_smscs:            # liste ; peut être vide
  - name: carrier-a
    # ... (voir ci-dessous)
```

| Champ | Type | Défaut | Notes |
|---|---|---|---|
| `observability` | objet \| absent | absent | Absent → pas de serveur HTTP. |
| `observability.http_port` | int | — | ∈ [0, 65535]. `0` = port éphémère choisi par l'OS. Ne doit pas entrer en collision avec un port de SMSC virtuel. |
| `virtual_smscs` | liste d'objets | `[]` | Chaque entrée décrit un SMSC virtuel. |

## Un SMSC virtuel (`virtual_smscs[]`)

| Champ | Type | Défaut | Contraintes |
|---|---|---|---|
| `name` | string | — | Identifiant du SMSC virtuel (utilisé comme `{id}` dans l'API et en label `virtual_smsc`). |
| `port` | int | — | Port SMPP d'écoute. ∈ [1, 65535]. **Unique** dans le fichier (et ≠ `http_port`). |
| `bind_credentials` | objet | — | Voir ci-dessous. |
| `addr_ton` | int | — | ∈ [0, 255]. |
| `addr_npi` | int | — | ∈ [0, 255]. |
| `address_range` | string | `""` | Regexp RE2 ; compilée à la validation (regexp invalide → erreur). |
| `tls` | objet | désactivé | Voir [TLS](#tls). |
| `seed` | uint64 \| absent | absent | Présent → **mode déterministe**. Absent → **mode chaos**. |
| `pdu_buffer_size` | int | **aucun** | **Requis**, ≥ 1. Capacité du tampon circulaire de PDU. |
| `throughput_limit_per_sec` | int \| absent | absent | ≥ 1. Plafond de débit du SMSC virtuel (temps réel). Interdit avec un `seed` sur un profil non-throughput (voir [validation](#validation-fail-fast)). |
| `quiescence_flush_ms` | uint64 \| absent | `250` | ∈ [1, 600000]. Fenêtre d'inactivité avant flush des événements planifiés. |
| `scenario` | objet | — | Le profil et ses réglages. Voir ci-dessous. |
| `mo_injection` | objet \| absent | absent | Injection de MO planifiés/auto. |
| `scheduled_disconnects` | liste | `[]` | Coupures de bind planifiées. |
| `scheduled_transitions` | liste | `[]` | Transitions de profil planifiées. |

### `bind_credentials`

| Champ | Type | Notes |
|---|---|---|
| `system_id` | string | Identifiant SMPP attendu au bind. |
| `password` | string | Mot de passe attendu. **Rédigé** dans les logs (`slog`). |

### TLS

```yaml
tls:
  enabled: false
  cert_file: ""   # chemin PEM ; à fournir avec key_file
  key_file: ""    # chemin PEM
```

| Champ | Type | Notes |
|---|---|---|
| `enabled` | bool | `true` active TLS sur le listener SMPP. |
| `cert_file` | string | Chemin d'un certificat PEM. **Doit** être fourni avec `key_file` (sinon erreur). |
| `key_file` | string | Chemin de la clé privée PEM. |

- `enabled: true` **sans** `cert_file`/`key_file` → certificat **auto-signé** généré en
  mémoire au boot (SAN loopback : `localhost`, `127.0.0.1`, `::1`) — donc **loopback-only**.
- `cert_file`/`key_file` sans `enabled` → erreur.
- Un seul des deux fichiers → erreur. Fichier introuvable → erreur. Tout est vérifié
  **au boot**, avant d'ouvrir le moindre port.

Voir [how-to/configurer-tls.md](../how-to/configurer-tls.md).

### `scenario`

```yaml
scenario:
  profile: throttling-carrier
  params:
    throughput_cap_per_sec: 5000
    error_code: ESME_RTHROTTLED
  latency:
    distribution: fixed
    params: { ms: 40 }
  dlr:                              # optionnel
    delay: { distribution: fixed, ticks: 5 }
    outcome_weights: { delivered: 90, failed: 8, expired: 2 }
    clock: logical
  protocol_edge_cases_enabled: false
  protocol_edge_cases:             # optionnel, valide seulement si *_enabled
    inject_every_ticks: 5
    kinds: [bad_length, unknown_command_id, bad_sequence]
```

| Champ | Type | Notes |
|---|---|---|
| `profile` | enum | L'un des 6 profils. Voir [reference/profils-de-scenario.md](profils-de-scenario.md). |
| `params` | objet | **Seuls** les knobs exposés par le profil choisi sont acceptés. Voir profils. |
| `latency` | objet | Distribution de latence appliquée avant la réponse. |
| `dlr` | objet \| absent | Génération de DLR. Voir ci-dessous. |
| `protocol_edge_cases_enabled` | bool | Active l'injection de PDU malformées (défaut `false`). |
| `protocol_edge_cases` | objet \| absent | Réglages de l'injection ; valide seulement si `_enabled: true`. |

#### `latency`

| `distribution` | `params` attendus |
|---|---|
| `fixed` | `ms` |
| `uniform` | `min_ms`, `max_ms` (`min_ms` ≤ `max_ms`) |
| `normal` | `mean_ms`, `stddev_ms` (résultat borné ≥ 0) |
| `spike` | `base_ms`, `spike_ms`, `interval_ticks` (≥ 1) |

Tous les champs de `params` sont des `uint64`. Les latences positionnelles sont
plafonnées à **600000 ms** (10 min). Pour `slow-carrier`, la latence est **contrainte à
[2000, 4000] ms**.

#### `dlr`

| Champ | Type | Notes |
|---|---|---|
| `delay.distribution` | enum | `fixed` (seul supporté actuellement). |
| `delay.ticks` | uint64 | Délai en ticks `per_bind_clock`. ≥ 1. |
| `outcome_weights.delivered` | uint | Poids du résultat `delivered`. |
| `outcome_weights.failed` | uint | Poids du résultat `failed`. |
| `outcome_weights.expired` | uint | Poids du résultat `expired`. Somme des trois ≥ 1. |
| `clock` | enum | `logical` (défaut) \| `wallclock`. `wallclock` **interdit** avec un `seed`. |

Voir [how-to/planifier-des-dlr.md](../how-to/planifier-des-dlr.md).

#### `protocol_edge_cases`

| Champ | Type | Défaut | Notes |
|---|---|---|---|
| `inject_every_ticks` | uint64 \| absent | `1` | Malforme une réponse tous les N ticks. |
| `kinds` | liste d'enum | les 3 | `bad_length`, `unknown_command_id`, `bad_sequence` (en rotation). Vide/absent → les trois. |

Voir [how-to/injecter-des-cas-limites-protocolaires.md](../how-to/injecter-des-cas-limites-protocolaires.md).

### `mo_injection`

```yaml
mo_injection:
  mode: scheduled          # scheduled | auto | disabled
  clock: logical           # logical (défaut) | wallclock (chaos seulement)
  events:                  # mode: scheduled
    - at_tick: 100
      source_addr: "33600000001"
      dest_addr: "33700000002"
      content: "MO probe A"
  # mode: auto              # utilise à la place :
  # rate_per_sec: 5
  # content_template: "..."
```

| Champ | Type | Notes |
|---|---|---|
| `mode` | enum | `scheduled` \| `auto` \| `disabled`. |
| `clock` | enum | `logical` (défaut) \| `wallclock` (interdit avec `seed`). |
| `events[]` | liste | Mode `scheduled`. Chaque événement : `at_tick` (uint64), `source_addr`, `dest_addr`, `content`. |
| `rate_per_sec` | int \| absent | Mode `auto`. |
| `content_template` | string \| absent | Mode `auto`. |

Voir [how-to/injecter-des-mo.md](../how-to/injecter-des-mo.md).

### `scheduled_disconnects[]`

| Champ | Type | Notes |
|---|---|---|
| `at_tick` | uint64 | Tick `per_bind_clock` de la coupure. |
| `scope` | enum | `all` \| `oldest` \| `random`. |
| `when` | enum | `before_response` \| `after_response`. |

### `scheduled_transitions[]`

| Champ | Type | Notes |
|---|---|---|
| `at_tick` | uint64 | Tick de la bascule. |
| `to_profile` | enum | Profil cible (doit exister). **Seule** voie de mutation du profil actif. |

Voir [how-to/planifier-deconnexions-et-transitions.md](../how-to/planifier-deconnexions-et-transitions.md).

## Enums

| Enum | Valeurs |
|---|---|
| `profile` / `to_profile` | `healthy`, `flaky-carrier`, `throttling-carrier`, `dead-carrier`, `slow-carrier`, `throughput-capped` |
| `clock` | `logical`, `wallclock` |
| Codes SMPP (`error_code`, clés de `error_mix`) | `ESME_ROK`, `ESME_RTHROTTLED`, `ESME_RSUBMITFAIL`, `ESME_RINVDSTADR`, `ESME_RSYSERR`, `ESME_RMSGQFUL`, `ESME_RINVSRCADR` |
| `mode` (dead-carrier) | `reject_bind`, `timeout_all` |
| `latency.distribution` | `fixed`, `uniform`, `normal`, `spike` |
| `mo_injection.mode` | `scheduled`, `auto`, `disabled` |
| `scope` | `all`, `oldest`, `random` |
| `when` | `before_response`, `after_response` |
| `kinds` | `bad_length`, `unknown_command_id`, `bad_sequence` |

## Validation fail-fast

Toute la validation s'exécute **avant** l'ouverture du moindre port. Erreurs
principales :

| Erreur | Déclencheur |
|---|---|
| `no config path given` | `--config` absent. |
| `unknown scenario profile` | `profile`/`to_profile` hors catalogue. |
| `wallclock clock requires no seed` | `clock: wallclock` alors qu'un `seed` est défini (dlr ou mo). |
| `throughput_limit_per_sec requires no seed on a deterministic profile` | `seed` + `throughput_limit_per_sec` sur un profil non-throughput (idem transition seedée vers un profil throughput). Exempts : `throttling-carrier`, `throughput-capped`. |
| `duplicate virtual smsc port` | Deux SMSC virtuels (ou collision avec `http_port`) sur le même port. |
| `scenario parameter out of bounds` | Un paramètre hors de ses bornes. |
| `unknown scheduled transition profile` | `to_profile` inconnu. |
| *param non exposé / manquant / enum invalide / address_range invalide* | Voir messages dédiés. |
| *TLS : cert/key dépareillés, cert sans `enabled`, fichier introuvable* | Voir [TLS](#tls). |

Bornes numériques utiles : `port` ∈ [1, 65535] ; `addr_ton`/`addr_npi` ∈ [0, 255] ;
`pdu_buffer_size` ≥ 1 ; `quiescence_flush_ms` ∈ [1, 600000] ; `throughput_cap_per_sec` ∈
[1, 1000000] ; `success_rate` ∈ [0, 1] ; `dlr.delay.ticks` / `disconnect_interval_ticks`
/ `interval_ticks` ≥ 1.

## Voir aussi

- [reference/profils-de-scenario.md](profils-de-scenario.md) — les knobs par profil.
- [reference/cli.md](cli.md) — comment fournir le fichier.
- Les fixtures [`examples/`](../../../examples/) — une par profil, toutes valides.

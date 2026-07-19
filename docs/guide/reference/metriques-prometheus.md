# Référence — Métriques Prometheus

> **Catégorie Diátaxis : Référence.** Les métriques exposées sur `GET /metrics`.
> Enregistrées sur un registre dédié (non global), par SMSC virtuel. Les labels sont
> **strictement bornés** : jamais de MSISDN, `message_id` ni contenu (cardinalité non
> bornée = fuite mémoire + fuite de données).

## Les 5 métriques

| Nom | Type | Labels |
|---|---|---|
| `smsc_active_binds` | Gauge | `virtual_smsc`, `bind_type` |
| `smsc_submit_sm_received_total` | Counter | `virtual_smsc` |
| `smsc_submit_sm_outcome_total` | Counter | `virtual_smsc`, `outcome` |
| `smsc_active_scenario` | Gauge | `virtual_smsc`, `scenario` |
| `smsc_served_latency_seconds` | Histogram | `virtual_smsc`, `scenario` |

## Détail

### `smsc_active_binds`
Nombre de binds actifs. `bind_type` ∈ {`transmitter`, `receiver`, `transceiver`}.

### `smsc_submit_sm_received_total`
Compteur de `submit_sm` reçus par SMSC virtuel.

### `smsc_submit_sm_outcome_total`
Compteur de résultats servis. `outcome` ∈ {`success`, `error`, `timeout`, `disconnect`,
`unknown`}.

### `smsc_active_scenario`
Vaut `1` pour le profil actuellement actif d'un SMSC virtuel, `0` pour le profil qu'il
remplace (utile pour tracer une `scheduled_transitions`).

### `smsc_served_latency_seconds`
Histogramme de la latence servie. Buckets exponentiels
`ExponentialBuckets(0.001, 2, 15)` — de ~1 ms à ~16 s.

## Valeurs de labels (bornées)

| Label | Valeurs possibles |
|---|---|
| `virtual_smsc` | le `name` de chaque SMSC virtuel |
| `bind_type` | `transmitter`, `receiver`, `transceiver` |
| `outcome` | `success`, `error`, `timeout`, `disconnect`, `unknown` |
| `scenario` | le nom d'un des 6 profils |

## Exemple de scrape Prometheus

```yaml
scrape_configs:
  - job_name: smsc-simulator
    static_configs:
      - targets: ["localhost:9000"]
```

## Voir aussi

- [reference/api-observabilite.md](api-observabilite.md) — l'endpoint `/metrics`.
- [how-to/scraper-les-metriques.md](../how-to/scraper-les-metriques.md)

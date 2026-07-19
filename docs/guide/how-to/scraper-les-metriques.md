# How-to — Scraper les métriques Prometheus

> **Catégorie Diátaxis : Guide pratique.** Objectif : collecter les métriques du
> simulateur dans Prometheus / Grafana. Liste des métriques :
> [reference/metriques-prometheus.md](../reference/metriques-prometheus.md).

## Activer l'endpoint

Les métriques sont exposées sur `GET /metrics` **si** le bloc `observability` figure
dans le `.yml` :

```yaml
observability:
  http_port: 9000
```

Omettre ce bloc désactive tout le HTTP (mode « boîte noire »), y compris `/metrics`.

## Vérifier à la main

```bash
curl -s http://localhost:9000/metrics | grep '^smsc_'
```

Vous obtenez les 5 familles : `smsc_active_binds`, `smsc_submit_sm_received_total`,
`smsc_submit_sm_outcome_total`, `smsc_active_scenario`, `smsc_served_latency_seconds`.

## Configurer Prometheus

```yaml
scrape_configs:
  - job_name: smsc-simulator
    static_configs:
      - targets: ["localhost:9000"]
```

En cluster, la cible est le Service `smsc-simulator` sur son port `observability`
(9000) — voir [how-to/deployer-avec-docker.md](deployer-avec-docker.md).

## Requêtes utiles

```promql
# Répartition des résultats servis par SMSC virtuel
sum by (virtual_smsc, outcome) (rate(smsc_submit_sm_outcome_total[1m]))

# Profil actif (1 = actif) — utile pour visualiser une transition
smsc_active_scenario

# Latence servie p95
histogram_quantile(0.95, sum by (le, virtual_smsc) (rate(smsc_served_latency_seconds_bucket[5m])))

# Binds actifs par type
smsc_active_binds
```

## Labels bornés

Les labels sont **strictement bornés** (`virtual_smsc`, `bind_type`, `outcome`,
`scenario`) : aucun MSISDN, `message_id` ni contenu n'apparaît en label. Vous pouvez
donc agréger sans crainte d'explosion de cardinalité.

## Voir aussi

- [reference/metriques-prometheus.md](../reference/metriques-prometheus.md)
- [reference/api-observabilite.md](../reference/api-observabilite.md)

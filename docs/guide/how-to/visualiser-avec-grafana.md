# How-to — Visualiser l'activité avec Grafana

> **Catégorie Diátaxis : Guide pratique.** Objectif : suivre en temps réel l'activité des
> SMSC virtuels (débit, résultats servis, latence, binds, profil actif) dans un dashboard
> Grafana. Prérequis : les métriques sont exposées — voir
> [scraper-les-métriques](scraper-les-metriques.md).

Le dépôt fournit un dashboard prêt à l'emploi
([`monitoring/grafana/dashboards/smsc-simulator.json`](../../../monitoring/grafana/dashboards/smsc-simulator.json))
et deux façons de l'alimenter : une stack Docker Compose clé-en-main (local/CI) ou un
`ConfigMap` en cluster Kubernetes.

## Option A — Stack Compose (clé-en-main)

Le `docker-compose.yml` embarque Prometheus et Grafana sous un **profil opt-in** : le
`docker compose up` par défaut reste un simple carrier, le monitoring ne démarre qu'avec
le profil `monitoring`.

```bash
docker compose --profile monitoring up
```

Cela lance :

- le simulateur (SMPP `2775`, observabilité `9000`) ;
- **Prometheus** (`9090`), qui scrape `smsc-simulator:9000` toutes les 5 s ;
- **Grafana** (`3000`), pré-provisionné avec la datasource Prometheus **et** le dashboard.

Ouvrez **http://localhost:3000** (login anonyme activé, rôle Viewer) : le dashboard
**« SMSC Simulator — activité temps réel »** est déjà chargé, rafraîchi toutes les 5 s.
Envoyez du trafic `submit_sm` vers `localhost:2775` depuis la passerelle sous test et les
panels se remplissent.

Vérifiez au besoin que la cible est bien scrappée : **http://localhost:9090/targets**
(job `smsc-simulator`, état `UP`).

## Option B — Kubernetes

Le manifeste [`deploy/grafana-dashboard.yaml`](../../../deploy/grafana-dashboard.yaml) est un
`ConfigMap` portant le label `grafana_dashboard: "1"`, auto-découvert par le **sidecar
Grafana** (chart Grafana / kube-prometheus-stack). Il suppose un Grafana déjà présent dans
le cluster.

```bash
kubectl apply -f deploy/          # applique aussi le ConfigMap du dashboard
```

Côté scrape, le `Service` porte les annotations `prometheus.io/scrape`, `.../port: "9000"`
et `.../path: "/metrics"` — suffisant pour un Prometheus configuré en découverte par
annotations. Si vous utilisez le **Prometheus Operator**, préférez un `ServiceMonitor`
ciblant le port `observability` (9000) plutôt que ces annotations.

Le JSON embarqué dans le `ConfigMap` est une **copie générée** de la source de vérité
`monitoring/grafana/dashboards/smsc-simulator.json`. Après modification du dashboard,
régénérez le bloc `data` :

```bash
kubectl create configmap smsc-simulator-grafana-dashboard \
  --from-file=smsc-simulator.json=monitoring/grafana/dashboards/smsc-simulator.json \
  --dry-run=client -o yaml
```

puis reportez les `labels` du fichier existant.

## Option C — Import manuel

Dans n'importe quel Grafana déjà branché sur un Prometheus qui scrape le simulateur :
**Dashboards → New → Import**, puis collez le contenu de
`monitoring/grafana/dashboards/smsc-simulator.json` et choisissez votre datasource
Prometheus quand la variable `Datasource` le demande.

## Ce que montre le dashboard

| Panel | Requête (résumé) |
|---|---|
| Total submit_sm reçus | `sum(smsc_submit_sm_received_total)` |
| Taux d'erreur (5 min) | part des `outcome!="success"` sur le total |
| Débit total / par carrier | `rate(smsc_submit_sm_received_total[1m])` |
| Résultats servis par type | `rate(smsc_submit_sm_outcome_total[1m])` par `outcome` |
| Latence servie p50/p95/p99 | `histogram_quantile(…, smsc_served_latency_seconds_bucket)` |
| Binds actifs | `smsc_active_binds` par `bind_type` |
| Profil de scénario actif | `smsc_active_scenario == 1` (visualise les transitions) |

Une variable **SMSC virtuel** (multi-sélection) filtre tous les panels. Les requêtes ne
s'appuient que sur les labels bornés `{virtual_smsc, bind_type, outcome, scenario}` — cf.
[métriques Prometheus](../reference/metriques-prometheus.md).

## Voir aussi

- [how-to/scraper-les-métriques](scraper-les-metriques.md)
- [how-to/déployer (Docker, Compose, Kubernetes)](deployer-avec-docker.md)
- [reference/métriques Prometheus](../reference/metriques-prometheus.md)
- [reference/API d'observabilité](../reference/api-observabilite.md)

# How-to — Héberger plusieurs SMSC virtuels

> **Catégorie Diátaxis : Guide pratique.** Objectif : décrire une **topologie
> multi-connecteurs** — plusieurs SMSC virtuels indépendants dans un seul processus —
> pour tester routage multi-connecteurs, distribution et bascule.

## Le principe

Un binaire héberge **N** SMSC virtuels. Chacun a son **propre port**, ses identifiants,
son `seed`, son profil et ses horloges. Ils sont **isolés** : un `dead-carrier` sur l'un
n'affecte pas un `healthy` sur l'autre.

## Exemple à trois carriers

```yaml
observability:
  http_port: 9000

virtual_smscs:
  - name: carrier-primary
    port: 2775
    bind_credentials: { system_id: smppclient1, password: secret }
    addr_ton: 1
    addr_npi: 1
    seed: 1
    pdu_buffer_size: 10000
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }

  - name: carrier-backup
    port: 2776
    bind_credentials: { system_id: smppclient2, password: secret }
    addr_ton: 1
    addr_npi: 1
    seed: 2
    pdu_buffer_size: 10000
    scenario:
      profile: slow-carrier
      latency: { distribution: uniform, params: { min_ms: 2000, max_ms: 4000 } }

  - name: carrier-flaky
    port: 2777
    bind_credentials: { system_id: smppclient3, password: secret }
    addr_ton: 1
    addr_npi: 1
    seed: 3
    pdu_buffer_size: 10000
    scenario:
      profile: flaky-carrier
      params: { success_rate: 0.8, error_mix: { ESME_RSYSERR: 1 }, disconnect_interval_ticks: 500 }
      latency: { distribution: fixed, params: { ms: 50 } }
```

## Règles à respecter

- **Ports uniques.** Deux SMSC virtuels sur le même port — ou une collision avec
  `observability.http_port` — est une erreur de validation (`duplicate virtual smsc
  port`). Le port `0` n'est jamais enregistré.
- **`seed` par instance.** Donnez un `seed` distinct à chacun si vous voulez des
  séquences distinctes mais chacune reproductible.
- **Une seule surface d'observabilité** pour tout le processus ; chaque SMSC virtuel y
  est adressé par son `name` : `/v1/virtual-smscs/{name}/…`.

## Observer l'ensemble

```bash
curl -s http://localhost:9000/v1/virtual-smscs | jq '.[] | {name, port, active_profile, bind_count}'
```

## Capacité

Un processus héberge confortablement **10–20+** SMSC virtuels. Un crash d'un SMSC
virtuel ne fait pas tomber les autres (isolement de goroutines, `recover` de dernier
ressort par instance).

## Voir aussi

- [explanation/architecture.md](../explanation/architecture.md)
- [how-to/deployer-avec-docker.md](deployer-avec-docker.md)

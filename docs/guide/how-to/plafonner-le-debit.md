# How-to — Plafonner le débit (throttling)

> **Catégorie Diátaxis : Guide pratique.** Objectif : faire renvoyer `ESME_RTHROTTLED`
> au-delà d'un débit, pour exercer le throttling adaptatif de la passerelle. Attention :
> le plafond est le **seul** mécanisme temps réel — il est **exempt** du rejeu
> déterministe.

## Deux leviers

| Levier | Portée | Où |
|---|---|---|
| `throughput_cap_per_sec` | paramètre de profil | `scenario.params` (profils `throttling-carrier`, `throughput-capped`) |
| `throughput_limit_per_sec` | SMSC virtuel entier | racine du `virtual_smscs[]` |

Les deux appliquent une **fenêtre glissante d'une seconde sur l'horloge murale**, y
compris en mode graîné, car un débit « par seconde » n'a pas d'équivalent en ticks.

## Profil `throttling-carrier`

```yaml
virtual_smscs:
  - name: carrier-throttled
    port: 2775
    throughput_limit_per_sec: 5000     # plafond du vSMSC (optionnel)
    scenario:
      profile: throttling-carrier
      params:
        throughput_cap_per_sec: 5000   # requis, ∈ [1, 1000000]
        error_code: ESME_RTHROTTLED    # code renvoyé au-delà
      latency: { distribution: fixed, params: { ms: 40 } }
```

Sous le plafond : succès. Au-delà : `error_code`. Voir `examples/throttling-carrier.yml`.

## Profil `throughput-capped`

```yaml
scenario:
  profile: throughput-capped
  params: { throughput_cap_per_sec: 8000 }
```

## Conséquence sur le déterminisme

Ces profils sont **hors du corpus de rejeu** de l'invariant (a). Leur reproductibilité
est celle des tests de charge : déterminisme **par bind** + **agrégation statistique**.
Assertez donc **par seuil** (« ~X % de `ESME_RTHROTTLED` sur N messages à débit Y »),
jamais par rejeu octet-pour-octet.

Corollaire de validation : `throughput_limit_per_sec` avec un `seed` est **refusé** sur
un profil non-throughput (erreur `throughput_limit_per_sec requires no seed…`). Les
profils throughput en sont exemptés.

## Observer

```bash
curl -s http://localhost:9000/metrics \
  | grep 'smsc_submit_sm_outcome_total.*outcome="error"'
```

## Voir aussi

- [explanation/determinisme.md](../explanation/determinisme.md) — la seule exception assumée.
- [reference/profils-de-scenario.md](../reference/profils-de-scenario.md)

# Référence — Profils de scénario

> **Catégorie Diátaxis : Référence.** Les 6 profils prédéfinis, leurs comportements et
> les paramètres (`scenario.params`) que chacun expose. Le catalogue est **figé dans le
> code** : le `.yml` sélectionne et paramètre, il ne définit pas de règles arbitraires.
> Pour *comprendre pourquoi*, voir
> [explanation/scenarios-predefinis.md](../explanation/scenarios-predefinis.md).

## Catalogue

| Profil | Comportement | Knobs `params` exposés |
|---|---|---|
| `healthy` | 100 % succès, latence fixe basse | *(aucun ; `latency` seul)* |
| `flaky-carrier` | mix succès/erreurs/timeouts + déconnexions périodiques | `success_rate`, `error_mix`, `disconnect_interval_ticks` |
| `throttling-carrier` | `error_code` au-delà d'un débit | `throughput_cap_per_sec` (requis), `error_code` |
| `dead-carrier` | refuse les binds **ou** fait timeout sur tout `submit_sm` | `mode` |
| `slow-carrier` | latence haute bornée, aucune erreur | *(aucun ; `latency` contraint à [2000, 4000] ms)* |
| `throughput-capped` | applique son propre plafond, throttle au-delà | `throughput_cap_per_sec` (requis) |

Tous les profils acceptent un bloc `latency` (sauf contrainte de `slow-carrier`) et un
bloc `dlr` optionnel.

## `healthy`

- **Comportement** : chaque `submit_sm` réussit (`ESME_ROK`). Latence = bloc `latency`.
- **Paramètres** : aucun knob dans `params`.
- **Exerce** : le chemin nominal / la référence de comparaison.

```yaml
scenario:
  profile: healthy
  latency: { distribution: fixed, params: { ms: 20 } }
```

## `flaky-carrier`

- **Comportement** : succès avec probabilité `success_rate` ; sinon une erreur tirée du
  mélange pondéré `error_mix` ; déconnexions périodiques tous les
  `disconnect_interval_ticks` ticks.
- **Décisions déterministes** : tirées du PRNG graîné `(seed, per_bind_clock)` → c'est
  **le** profil sur lequel l'invariant (a) se prouve.

| Knob | Type | Défaut | Contraintes |
|---|---|---|---|
| `success_rate` | float | `1.0` | ∈ [0, 1]. |
| `error_mix` | map `code → poids` | — | Clés = codes SMPP valides ; somme des poids > 0. |
| `disconnect_interval_ticks` | uint64 | — | ≥ 1. |

```yaml
scenario:
  profile: flaky-carrier
  params:
    success_rate: 0.8
    error_mix: { ESME_RSYSERR: 1, ESME_RSUBMITFAIL: 1 }
    disconnect_interval_ticks: 500
  latency: { distribution: normal, params: { mean_ms: 60, stddev_ms: 15 } }
```

## `throttling-carrier`

- **Comportement** : succès sous le plafond ; au-delà de `throughput_cap_per_sec`
  (fenêtre glissante d'1 s, **temps réel**), renvoie `error_code`.
- **Déterminisme** : **hors** du corpus de rejeu de l'invariant (a) — le plafond est un
  mécanisme temps réel (voir [determinisme](../explanation/determinisme.md)).

| Knob | Type | Contraintes |
|---|---|---|
| `throughput_cap_per_sec` | int | **Requis**, ∈ [1, 1000000]. |
| `error_code` | enum SMPP | Code renvoyé au-delà du plafond (typiquement `ESME_RTHROTTLED`). |

```yaml
scenario:
  profile: throttling-carrier
  params:
    throughput_cap_per_sec: 5000
    error_code: ESME_RTHROTTLED
  latency: { distribution: fixed, params: { ms: 40 } }
```

## `dead-carrier`

- **Comportement** : selon `mode` —
  - `reject_bind` : refuse tout bind (`ESME_RBINDFAIL`) ;
  - `timeout_all` : accepte le bind mais fait **timeout** sur chaque `submit_sm`.

| Knob | Type | Valeurs |
|---|---|---|
| `mode` | enum | `reject_bind` \| `timeout_all`. |

- **Exerce** : disjoncteur ouvert, repli de routage, auto-reconnexion. Souvent utilisé
  au milieu d'un `scheduled_transitions` (healthy → dead-carrier → healthy).

```yaml
scenario:
  profile: dead-carrier
  params: { mode: reject_bind }
  latency: { distribution: fixed, params: { ms: 0 } }
```

## `slow-carrier`

- **Comportement** : latence haute bornée à **[2000, 4000] ms**, aucune erreur. Une
  `latency` hors de cette fenêtre est une erreur de validation.
- **Paramètres** : aucun knob dans `params` ; on règle le bloc `latency`.
- **Exerce** : les timeouts de réponse, la taille de fenêtre, la durée de span.

```yaml
scenario:
  profile: slow-carrier
  latency: { distribution: uniform, params: { min_ms: 2000, max_ms: 4000 } }
```

## `throughput-capped`

- **Comportement** : applique son propre plafond `throughput_cap_per_sec` et throttle
  au-delà. Comme `throttling-carrier`, **hors** du corpus de rejeu de l'invariant (a).

| Knob | Type | Contraintes |
|---|---|---|
| `throughput_cap_per_sec` | int | **Requis**, ∈ [1, 1000000]. |

```yaml
scenario:
  profile: throughput-capped
  params: { throughput_cap_per_sec: 8000 }
  latency: { distribution: spike, params: { base_ms: 30, spike_ms: 250, interval_ticks: 1000 } }
```

## Codes d'erreur SMPP acceptés

`ESME_ROK`, `ESME_RTHROTTLED`, `ESME_RSUBMITFAIL`, `ESME_RINVDSTADR`, `ESME_RSYSERR`,
`ESME_RMSGQFUL`, `ESME_RINVSRCADR`.

## Voir aussi

- [reference/configuration-yml.md](configuration-yml.md) — le schéma complet.
- [explanation/scenarios-predefinis.md](../explanation/scenarios-predefinis.md) — le pourquoi.
- [tutorials/02-tester-la-resilience.md](../tutorials/02-tester-la-resilience.md) — un profil de panne en pratique.

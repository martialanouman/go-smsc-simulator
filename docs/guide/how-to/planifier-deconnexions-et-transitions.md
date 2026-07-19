# How-to — Planifier déconnexions et transitions de scénario

> **Catégorie Diátaxis : Guide pratique.** Objectif : scénariser des **coupures de bind**
> et des **bascules de profil** à des ticks précis, pour exercer le disjoncteur et
> l'auto-reconnexion de la passerelle de façon reproductible.

## Déconnexions planifiées

Coupez des binds à un tick donné :

```yaml
scheduled_disconnects:
  - at_tick: 300
    scope: all                 # all | oldest | random
    when: before_response      # before_response | after_response
  - at_tick: 600
    scope: oldest
    when: after_response
```

| Champ | Valeurs | Sens |
|---|---|---|
| `at_tick` | uint64 | Tick `per_bind_clock` de la coupure. |
| `scope` | `all` \| `oldest` \| `random` | Quels binds couper. |
| `when` | `before_response` \| `after_response` | Couper avant ou après le `submit_sm_resp` du tick. |

`when: before_response` coupe **avant** de répondre (le client voit une transaction
interrompue) ; `after_response` coupe juste après. Vérifiez l'effet via :

```bash
curl -s http://localhost:9000/v1/virtual-smscs/carrier-a/binds | jq
```

## Transitions de scénario

Faites basculer le profil actif à des ticks précis — le pattern « panne puis reprise » :

```yaml
scenario:
  profile: healthy               # profil de départ
scheduled_transitions:
  - at_tick: 200
    to_profile: dead-carrier
  - at_tick: 400
    to_profile: healthy
```

Points clés :

- Le profil actif **n'avance que** par ces transitions — il n'existe aucune mutation
  runtime. Deux exécutions de la même fixture produisent la **même** séquence de
  bascules.
- `to_profile` doit être l'un des 6 profils (sinon `unknown scheduled transition
  profile`).
- Contrainte déterminisme : une transition **seedée** vers un profil à plafond de débit
  (`throttling-carrier`/`throughput-capped`) est refusée si un `throughput_limit_per_sec`
  incompatible est en jeu — voir la validation.

Observez la bascule :

```bash
curl -s http://localhost:9000/v1/virtual-smscs | jq '.[0].active_profile'
curl -s http://localhost:9000/metrics | grep smsc_active_scenario
```

## Drainage au repos

Déconnexions et transitions planifiées sont, comme les DLR/MO, drainées par le **flush
de quiescence** si le trafic cesse avant leur tick (invariant d).

## Fixtures d'exemple

- `examples/scenario-transitions.yml` — les 3 mécanismes S5 ensemble (MO, disconnect,
  transitions).
- `examples/dead-carrier.yml` — `dead-carrier` avec une transition de retour vers
  `healthy`.

## Voir aussi

- [explanation/scenarios-predefinis.md](../explanation/scenarios-predefinis.md)
- [tutorials/02-tester-la-resilience.md](../tutorials/02-tester-la-resilience.md)
- [reference/configuration-yml.md](../reference/configuration-yml.md#scheduled_disconnects)

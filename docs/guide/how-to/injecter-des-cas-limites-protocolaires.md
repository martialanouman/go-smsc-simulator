# How-to — Injecter des cas limites protocolaires

> **Catégorie Diátaxis : Guide pratique.** Objectif : émettre des PDU **malformées** pour
> tester la robustesse du parsing de la passerelle. **Opt-in**, désactivé par défaut.

## Activer l'injection

Deux clés : un interrupteur, puis un réglage optionnel.

```yaml
scenario:
  profile: healthy
  latency: { distribution: fixed, params: { ms: 20 } }
  protocol_edge_cases_enabled: true    # l'interrupteur (défaut false)
  protocol_edge_cases:                 # réglage ; valide seulement si _enabled
    inject_every_ticks: 5              # malforme une réponse tous les 5 ticks (défaut 1)
    kinds: [bad_length, unknown_command_id, bad_sequence]
```

Sans `protocol_edge_cases_enabled: true`, l'encodage reste **strict** — aucune PDU
malformée. Le bloc `protocol_edge_cases` n'est valide que si l'interrupteur est armé.

## Les types de malformation

| `kind` | Effet |
|---|---|
| `bad_length` | Longueur de PDU invalide. |
| `unknown_command_id` | `command_id` invalide. |
| `bad_sequence` | Numéro de séquence hors ordre. |

Les `kinds` déclarés sont appliqués **en rotation**. Une liste vide ou absente ⇒ les
trois types en rotation.

## Cadence

`inject_every_ticks: N` malforme une réponse tous les N ticks `per_bind_clock` (défaut
`1` = chaque réponse). C'est déterministe à `seed` fixe.

## Fixture d'exemple

`examples/edge-cases.yml` — profil `healthy` avec injection tous les 5 ticks des trois
types.

## Ce que cela teste

Que la passerelle **ne panique pas** et gère proprement une PDU corrompue (rejet,
resynchronisation, log). Combinez avec le fuzzing interne du décodeur (`make fuzz`) pour
couvrir l'entrée hostile côté simulateur lui-même.

## Voir aussi

- [reference/configuration-yml.md](../reference/configuration-yml.md#protocol_edge_cases)
- [reference/commandes-make.md](../reference/commandes-make.md) — `make fuzz`.

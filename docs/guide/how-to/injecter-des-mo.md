# How-to — Injecter des messages MO

> **Catégorie Diátaxis : Guide pratique.** Objectif : faire émettre au simulateur des
> `deliver_sm` MO (mobile-originated) non sollicités, soit à des ticks précis
> (déterministe), soit à un débit (test de charge du chemin MO).

## Mode `scheduled` — MO déterministes

Des MO ancrés à des ticks logiques, donc reproductibles :

```yaml
mo_injection:
  mode: scheduled
  clock: logical                 # imposé en mode graîné ; wallclock seulement en chaos
  events:
    - at_tick: 100
      source_addr: "33600000001"
      dest_addr: "33700000002"
      content: "MO probe A"
    - at_tick: 250
      source_addr: "33600000003"
      dest_addr: "33700000002"
      content: "MO probe B"
```

Chaque événement est émis quand le `per_bind_clock` atteint `at_tick`. À `seed` fixe,
c'est reproductible au bit près.

## Mode `auto` — MO à un débit

Pour saturer le chemin MO (tests de charge/endurance) :

```yaml
mo_injection:
  mode: auto
  rate_per_sec: 5
  content_template: "auto MO {{seq}}"
```

En mode graîné, le débit reste ancré aux ticks ; en mode chaos, il suit l'horloge
murale.

## Mode `disabled`

```yaml
mo_injection:
  mode: disabled
```

Équivalent à omettre le bloc.

## Le flush de quiescence s'applique aussi

Comme les DLR, les MO planifiés au repos sont drainés par le flush de quiescence
(`quiescence_flush_ms`, défaut 250 ms) — un lot soumis puis silence délivre quand même
les MO en attente, dans l'ordre de tick.

## Observer les MO

Les MO sont des `deliver_sm` reçus **côté client** (l'ESME) sur son bind
receiver/transceiver. Le simulateur les émet ; c'est le client qui les constate.

## Voir aussi

- [reference/configuration-yml.md](../reference/configuration-yml.md#mo_injection)
- [how-to/planifier-des-dlr.md](planifier-des-dlr.md)
- [how-to/planifier-deconnexions-et-transitions.md](planifier-deconnexions-et-transitions.md)

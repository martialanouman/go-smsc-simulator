# Tutoriel — Tester la résilience avec un carrier qui tombe

> **Catégorie Diátaxis : Tutoriel.** Suite du [tutoriel 01](01-premier-carrier.md). Vous
> allez faire jouer à un SMSC virtuel le scénario « l'opérateur tombe, puis revient » —
> le pattern qui exerce le disjoncteur de la passerelle — et l'observer via l'API et les
> métriques.

## Ce que vous allez apprendre

- Utiliser un profil de panne (`dead-carrier`).
- Scénariser une panne **puis** une reprise avec `scheduled_transitions`.
- Lire les métriques Prometheus pour voir le comportement changer.

Durée : ~15 minutes.

## Prérequis

- Avoir fait le [tutoriel 01](01-premier-carrier.md).

## Étape 1 — Regarder la fixture de transition

Ouvrez `examples/scenario-transitions.yml`. Le cœur :

```yaml
scenario:
  profile: healthy                # au départ : sain
scheduled_transitions:
  - at_tick: 200
    to_profile: dead-carrier       # à 200 ticks : l'opérateur tombe
  - at_tick: 400
    to_profile: healthy            # à 400 ticks : il revient
```

Rappel clé : un **tick** n'est pas une seconde, c'est un `submit_sm` traité **sur le
bind**. La bascule vers `dead-carrier` survient après le 200ᵉ message de ce bind — pas
après 200 secondes. C'est ce qui rend le scénario **reproductible** : rejouez la même
séquence d'entrée, les mêmes bascules tombent aux mêmes points. Voir
[explanation/determinisme.md](../explanation/determinisme.md).

## Étape 2 — Lancer

```bash
make run CONFIG=examples/scenario-transitions.yml
```

Dans un second terminal :

```bash
curl -s http://localhost:9000/v1/virtual-smscs | jq '.[0].active_profile'
```

```
"healthy"
```

## Étape 3 — Observer la bascule

Faites envoyer des `submit_sm` par votre client SMPP (l'ESME). Surveillez le profil
actif :

```bash
watch -n1 'curl -s http://localhost:9000/v1/virtual-smscs/$(curl -s http://localhost:9000/v1/virtual-smscs | jq -r ".[0].name")/logical-clock'
```

Quand le `per_bind_clock` du bind franchit 200, le profil actif passe à
`dead-carrier` : selon son `mode`, les binds sont refusés ou les `submit_sm` timeoutent.
Franchi 400, il redevient `healthy`. Vérifiez :

```bash
curl -s http://localhost:9000/v1/virtual-smscs | jq '.[0].active_profile'
```

## Étape 4 — Lire les métriques

```bash
curl -s http://localhost:9000/metrics | grep -E 'smsc_active_scenario|smsc_submit_sm_outcome_total'
```

Vous verrez `smsc_active_scenario` valoir `1` sur le profil courant et `0` sur celui
qu'il a remplacé, et `smsc_submit_sm_outcome_total` ventiler les résultats par `outcome`
(`success`, `timeout`, `disconnect`…). C'est la preuve, côté simulateur, que le scénario
s'est déroulé comme prévu.

## Étape 5 — Ce que la passerelle, elle, devrait faire

Ce tutoriel observe le **simulateur**. Dans un vrai test de résilience, vous asserteriez
**en plus** contre la passerelle sous test :

- son **disjoncteur** s'ouvre-t-il pendant la fenêtre `dead-carrier` ?
- se **referme**-t-il et le trafic **reprend**-il après le retour à `healthy` ?

Le simulateur fournit l'adversaire déterministe ; vos assertions vivent des deux côtés.

## Étape 6 — Essayer un carrier instable

Rejouez avec `examples/flaky-carrier.yml` (~80 % succès, erreurs intermittentes,
déconnexions périodiques, DLR). Observez `smsc_submit_sm_outcome_total` : le mélange
succès/erreur doit tenir dans une tolérance statistique. C'est le profil sur lequel le
**déterminisme par bind** (invariant a) se prouve : à `seed` fixe, deux exécutions
produisent la **même** séquence.

## Ce que vous avez appris

- Un profil de panne se sélectionne dans le `.yml`, comme un profil sain.
- `scheduled_transitions` scénarise panne **puis** reprise, ancré aux ticks, reproductible.
- Les métriques Prometheus donnent la preuve observable du déroulé.

## Et ensuite

- [How-to — Planifier déconnexions et transitions](../how-to/planifier-deconnexions-et-transitions.md)
- [How-to — Reproduire un test à graine fixe](../how-to/reproduire-un-test.md)
- [Explication — Pourquoi un catalogue figé de scénarios](../explanation/scenarios-predefinis.md)

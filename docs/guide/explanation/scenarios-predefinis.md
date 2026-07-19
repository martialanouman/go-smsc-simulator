# Pourquoi un catalogue figé de scénarios

> **Catégorie Diátaxis : Explication.** Cette page justifie le choix d'un **catalogue
> figé de 6 profils** plutôt que des règles de réponse arbitraires dans le `.yml`, et
> relie chaque profil au mécanisme de résilience qu'il exerce **côté passerelle**. Pour
> les paramètres exacts de chaque profil, voir
> [reference/profils-de-scenario.md](../reference/profils-de-scenario.md).

## La contrainte

Le comportement d'un SMSC virtuel face à un `submit_sm` est choisi parmi un **catalogue
figé de 6 profils nommés**, codé en dur dans le simulateur. Le `.yml` **sélectionne**
un profil par son nom et n'en **règle que les paramètres exposés**. Il ne peut **pas**
définir de `response_rules` sur mesure (« si le MSISDN commence par 336, alors renvoyer
telle erreur avec telle probabilité… »).

## Pourquoi refuser les règles arbitraires

Un moteur de règles générique est séduisant sur le papier, mais il coûte cher là où ça
compte pour un outil de test :

- **Lisibilité des fixtures.** Une fixture qui dit `profile: flaky-carrier` se lit d'un
  coup d'œil. Une fixture de 40 lignes de règles conditionnelles imbriquées demande une
  relecture attentive à chaque test — et devient elle-même une source de bugs.
- **Comportement borné et testé.** Les 6 profils sont **testés** (invariants,
  distributions, déterminisme). Des règles arbitraires ouvrent un espace de
  comportements infini, impossible à couvrir — on ne saurait plus si un test échoue à
  cause de la passerelle ou d'une règle mal écrite.
- **Intention claire.** On ne teste pas « un SMSC qui fait X quand Y ». On teste « la
  passerelle survit-elle à un opérateur *flaky* / *mort* / *saturé* / *lent* ? ». Les
  profils nomment ces intentions directement.

Ce qu'on renonce — des scénarios totalement sur mesure — est jugé **inutile** pour
exercer les mécanismes de résilience ciblés, et **nuisible** à la lisibilité. C'est un
compromis assumé.

## Les 6 profils et ce qu'ils exercent

Chaque profil est construit pour déclencher un mécanisme de résilience **précis** de la
passerelle sous test. C'est là tout l'intérêt : le simulateur n'est pas un miroir
passif, il est l'**adversaire** conçu contre les promesses que la passerelle fait sur
elle-même.

| Profil | Comportement | Mécanisme de passerelle exercé |
|---|---|---|
| `healthy` | 100 % succès, latence fixe basse | chemin nominal / référence |
| `flaky-carrier` | ~80 % succès, ~20 % erreurs/timeouts, déconnexions périodiques | **disjoncteur**, retry / dead-letter |
| `throttling-carrier` | `ESME_RTHROTTLED` au-delà d'un débit | **throttling adaptatif** |
| `dead-carrier` | refuse les binds, ou fait timeout sur chaque `submit_sm` | **disjoncteur ouvert**, repli de routage, auto-reconnexion |
| `slow-carrier` | latence haute bornée (2–4 s), aucune erreur | timeouts de réponse, taille de fenêtre, durée de span |
| `throughput-capped` | applique son propre plafond, throttle au-delà | boucle de throttling adaptatif bout-en-bout |

## Les transitions : scénariser une panne *puis* une reprise

Un test de résilience intéressant n'est pas statique — il veut voir la passerelle
**réagir à un changement** : l'opérateur tombe, le disjoncteur s'ouvre, l'opérateur
revient, le disjoncteur se referme, le trafic reprend.

Le simulateur exprime cela sans aucune mutation à chaud, via `scheduled_transitions` :
une liste de bascules de profil ancrées à des ticks. Le profil actif d'un SMSC virtuel
**n'avance que** par ces transitions planifiées — il n'existe aucun chemin de mutation
runtime. Deux exécutions de la même fixture produisent donc **exactement la même
séquence de bascules**, aux mêmes ticks. Le pattern canonique :

```yaml
scenario:
  profile: healthy          # ticks 0–199
scheduled_transitions:
  - at_tick: 200
    to_profile: dead-carrier # ticks 200–399 : l'opérateur "tombe"
  - at_tick: 400
    to_profile: healthy      # ticks 400+  : l'opérateur "revient"
```

## Le lien avec le déterminisme

Le catalogue figé et la configuration déclarative se renforcent. Parce que les profils
sont bornés et que les transitions sont ancrées à des ticks, l'ensemble du comportement
d'un SMSC virtuel — quel résultat pour quel `submit_sm`, quand une transition survient
— est **entièrement déterminé par `(seed, séquence d'entrée)`**. C'est ce qui permet à
l'invariant (a) de tenir.

## Voir aussi

- [reference/profils-de-scenario.md](../reference/profils-de-scenario.md) — les paramètres exacts de chaque profil.
- [explanation/pourquoi-config-declarative.md](pourquoi-config-declarative.md) — pourquoi les transitions sont déclaratives.
- [tutorials/02-tester-la-resilience.md](../tutorials/02-tester-la-resilience.md) — mettre un profil de panne en pratique.

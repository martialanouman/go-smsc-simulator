# Pourquoi une configuration 100 % déclarative

> **Catégorie Diátaxis : Explication.** Cette page justifie le choix d'architecture le
> plus structurant du simulateur : **un seul fichier `.yml`, chargé au démarrage,
> comme unique entrée de configuration** — aucune API de configuration à chaud. Pour
> le schéma exact, voir [reference/configuration-yml.md](../reference/configuration-yml.md).

## La contrainte, formulée nettement

Toute la configuration du simulateur vient d'**un** fichier `.yml`, lu et validé **une
seule fois au démarrage**. Il n'existe :

- **aucune** API HTTP de configuration ;
- **aucun** endpoint mutant (`POST`/`PATCH`/`PUT`/`DELETE`) — la surface HTTP est
  *strictement en lecture seule* ;
- **aucun** chemin de reconfiguration runtime.

Reconfigurer = **éditer le fichier et relancer** le processus. C'est une contrainte
délibérée, pas une limitation qu'on comblera plus tard.

## Trois bénéfices qui se renforcent

### 1. Une source de vérité unique

Avec une API de configuration à chaud, l'état réel du simulateur est le fichier
**plus** la somme des patches appliqués depuis le démarrage. Diagnostiquer un test qui
échoue devient une enquête : « le `.yml` dit X, mais quelqu'un a-t-il patché Y à
l'exécution ? ». Cette divergence *fichier vs état patché* est une source classique de
tests non reproductibles.

En interdisant toute mutation, l'état d'exécution est **exactement le reflet immuable
du `.yml`**. Ce que vous lisez dans la fixture versionnée est ce qui tourne. Point.

### 2. Zéro entrée temporelle non déterministe

C'est le lien profond avec le [déterminisme](determinisme.md). Une API « injecte un MO
maintenant » ou « bascule le scénario maintenant » dépendrait de **l'instant d'appel**,
mesuré à l'horloge murale. Elle réintroduirait exactement le non-déterminisme que le
tick logique élimine.

En exigeant que *tout* événement temporel soit **déclaré dans le `.yml` et ancré à un
tick**, le simulateur garantit qu'il n'existe **aucune** entrée temporelle externe. Les
trois formes déclaratives d'action temporelle —

- `mo_injection` (messages MO planifiés),
- `scheduled_disconnects` (coupures de bind planifiées),
- `scheduled_transitions` (transitions de scénario planifiées),

— sont toutes ancrées à un tick, donc toutes reproductibles. Le pattern classique
« `healthy` → `dead-carrier` → `healthy` » (ouvrir puis refermer le disjoncteur de la
passerelle et vérifier la reprise) se déclare via `scheduled_transitions` — le *seul*
cas d'usage qu'une API à chaud aurait servi, ici couvert de façon *reproductible*.

### 3. Fail-fast : échouer avant d'ouvrir un port

La validation complète du `.yml` (profil connu, cohérence `seed`/`clock`, unicité des
ports, bornes des paramètres, références de transition valides) s'exécute **avant**
d'ouvrir le moindre port SMPP. Un `.yml` invalide → **sortie non nulle** avec un
message nommant le champ fautif, plutôt qu'un démarrage dans un état ambigu qu'on
découvrirait au milieu d'un test.

C'est l'**invariant (b)** du projet. `log.Fatal` n'est toléré qu'ici, au boot.

## Le coût, et pourquoi il est neutralisé

Le coût apparent : pas de mutation à chaud. On ne peut pas « ajuster un scénario » sur
une instance qui tourne. Mais ce coût est **neutralisé par une autre NFR** : le
démarrage à froid est **< 2 s**. Un nouveau scénario n'est pas un patch, c'est une
**nouvelle instance**. En CI, l'isolation entre tests s'obtient de la façon la plus
propre qui soit : **relancer** le simulateur avec le `.yml` de la fixture — ce qui
repart d'un tampon de PDU vide et d'un `per_bind_clock` à zéro, bien plus déterministe
qu'un reset à chaud.

## Ce que la surface HTTP a le droit de faire

Écarter une API *de configuration* n'oblige pas à écarter toute surface HTTP. Le
simulateur expose une surface **en lecture seule** :

- inspection des PDU reçues (`GET /v1/virtual-smscs/{id}/received-pdus`) et des binds
  (`GET /v1/virtual-smscs/{id}/binds`) ;
- compteur logique par SMSC virtuel (`GET /v1/virtual-smscs/{id}/logical-clock`) ;
- santé (`GET /health`) et métriques Prometheus (`GET /metrics`).

Ces endpoints servent les assertions d'**état vivant** (« le disjoncteur s'est-il
ouvert en moins de N secondes ? », « combien de binds actifs ? ») et le scraping
Prometheus — bien plus ergonomiques en HTTP qu'en fichier — **sans jamais** modifier
l'état. Un verbe mutant sur cette surface est un **bug** (invariant c). Le bloc
`observability` peut même être omis pour désactiver entièrement le HTTP (mode « boîte
noire »).

## En résumé

| Choix | Ce qu'on gagne | Ce qu'on renonce | Pourquoi c'est le bon compromis |
|---|---|---|---|
| `.yml` unique vs API de config runtime | source de vérité unique + zéro entrée temporelle non déterministe | mutation à chaud | neutralisé par le démarrage < 2 s ; le seul cas utile (transition) est couvert de façon reproductible |
| HTTP read-only vs aucune surface HTTP | assertions d'état vivant + scraping Prometheus ergonomiques | un petit serveur HTTP | désactivable en omettant `observability` ; ne viole pas « aucune API de config » |
| Fail-fast au boot vs validation paresseuse | erreurs explicites avant d'ouvrir un port | — | un `.yml` invalide ne démarre jamais dans un état ambigu |

## Voir aussi

- [explanation/determinisme.md](determinisme.md) — pourquoi « zéro entrée temporelle » est vital.
- [explanation/scenarios-predefinis.md](scenarios-predefinis.md) — l'autre grande contrainte déclarative.
- [reference/configuration-yml.md](../reference/configuration-yml.md) — le schéma complet.

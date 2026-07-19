# Le déterminisme, expliqué honnêtement

> **Catégorie Diátaxis : Explication.** Cette page éclaire *pourquoi* le simulateur
> est déterministe de la façon dont il l'est, et *ce que* cette garantie couvre —
> et ne couvre pas. Pour *faire* (obtenir un rejeu reproductible), voir
> [how-to/reproduire-un-test.md](../how-to/reproduire-un-test.md). Pour les champs
> exacts, voir [reference/configuration-yml.md](../reference/configuration-yml.md).

## Pourquoi le déterminisme est le cœur du produit

Un simulateur SMSC sert à écrire des **assertions de test**. Une assertion n'a de
valeur que si elle est **reproductible** : le même test, rejoué en CI, doit produire
le même verdict — sinon il devient « flaky » et on finit par l'ignorer.

Le piège, c'est qu'un simulateur de panne manipule par nature du **temps** : « livre
le DLR 5 unités après la soumission », « coupe la connexion au bout d'un moment »,
« passe le débit sous le plafond ». Si ces mécanismes lisent l'**horloge murale**
(`time.Now()`), ils ne peuvent **pas** être reproductibles : la gigue CI, les pauses
du ramasse-miettes et l'ordonnancement réseau décalent chaque exécution. Deux runs
du même test divergent, et l'assertion devient instable.

Le simulateur résout cela par une règle simple et absolue :

> **En mode graîné, aucun mécanisme temporel planifié ne lit l'horloge murale.**
> Tout est ancré sur un **compteur logique de PDU**, jamais sur le temps réel.

## Le tick logique, pas l'horloge murale

Quand un `seed` est présent dans le `.yml`, le simulateur bascule en **mode
déterministe**. La référence de temps n'est alors plus la seconde qui passe, mais un
**compteur de `submit_sm` traités** — le *tick logique*.

Un DLR planifié « 5 ticks après la soumission d'origine » se déclenche donc après que
**5 `submit_sm` de plus** ont été traités sur ce bind — quelle que soit la durée réelle
écoulée. Rejouez le test : les 5 mêmes soumissions se produisent dans le même ordre,
donc le DLR tombe exactement au même point de la séquence. Reproductible par
construction.

C'est aussi ce qui rend le PRNG reproductible : chaque décision pondérée (succès /
erreur / timeout / disconnect, latence) est tirée d'un générateur graîné par une
fonction de `(seed, tick)`. Même graine + même séquence d'entrée = mêmes tirages.

## Pourquoi *par bind*, et pas globalement

Voici la nuance que le simulateur assume ouvertement, là où un outil naïf mentirait.

La passerelle sous test **n'ouvre pas une seule connexion** vers un connecteur : elle
en ouvre un **pool** (plusieurs binds parallèles pour scaler le débit). Ces binds sont
des flux TCP concurrents, et l'ordre dans lequel leurs PDU s'entrelacent dépend de
l'ordonnanceur du système — **ce n'est pas reproductible**, et aucune astuce ne peut
le rendre reproductible sans sérialiser tout le trafic (ce qui tuerait le débit).

Le simulateur tient donc **deux compteurs distincts**, et la distinction est
essentielle :

| Compteur | Portée | Rôle |
|---|---|---|
| `per_bind_clock` | par session de bind | **Référence de planification déterministe.** Tout mécanisme temporel (DLR, `spike`, MO, déconnexions, transitions) y est ancré. Reproductible au sein d'un bind. |
| `logical_clock` | par SMSC virtuel (global) | **Observable d'assertion seulement.** Exposé en lecture seule pour compter les PDU traitées. Son ordre entre binds concurrents n'est **pas** reproductible — ne jamais l'utiliser comme référence de planification. |

La garantie honnête est donc : **le déterminisme est scopé par session de bind, pas
globalement.** Au sein d'un bind (flux TCP ordonné), la séquence et le contenu des
résultats, latences, DLR et MO sont reproductibles au bit près. Entre binds
concurrents, seul l'agrégat statistique l'est.

**Conséquence pratique :** un test qui asserte un **ordre global précis** doit épingler
`bind_pool_size = 1` côté passerelle (un seul bind → ordre global = ordre par bind).
La majorité des tests fonctionnels CI (comportement précis à faible volume) sont dans
ce cas. Les tests de charge multi-bind, eux, conservent le déterminisme par bind et
s'appuient sur l'agrégation statistique.

## Le flush de quiescence : ne pas se figer au repos

Le tick logique n'avance qu'avec le **trafic entrant**. Or le cas CI majoritaire est :
soumettre un lot de messages, **puis attendre** les DLR ou une transition de scénario.
Dès que le trafic cesse, le compteur se fige… et un DLR planifié « 5 ticks plus tard »
ne se déclencherait jamais, parce que ces 5 ticks n'arrivent jamais. Le test resterait
bloqué.

C'est le rôle du **flush de quiescence**. Chaque bind tient un ensemble ordonné
d'événements planifiés à venir (`pending_logical_schedule`). Cet ensemble est drainé :

1. **en fonctionnement normal**, quand `per_bind_clock` atteint le tick dû ;
2. **au repos**, par un *flush* déclenché après `quiescence_flush_ms` (défaut 250 ms)
   sans nouveau `submit_sm`, qui vide les événements en attente **dans l'ordre de tick
   déterministe**.

Ce que le flush préserve : la **séquence et le contenu** des événements (l'ordre des
DLR, leurs résultats, leur corrélation). Ce qu'il abandonne, sciemment : la **latence
murale absolue** d'un événement au repos — un DLR « à 5 ticks » sera drainé ~250 ms
après le silence, pas à un instant mural garanti. Sans conséquence pour une assertion
qui vérifie *ce qui* a été livré et *dans quel ordre*, jamais *à quelle milliseconde
exacte*.

C'est l'**invariant (d)** du projet, testé et bloquant.

## La seule exception assumée : le plafond de débit

Un mécanisme échappe volontairement au tick logique : le **plafond de débit**
(`throughput_cap_per_sec` d'un profil, `throughput_limit_per_sec` d'un SMSC virtuel).

Un débit « par seconde » n'a tout simplement **pas de sens** sur un compteur de PDU :
il mesure des PDU **par unité de temps réel**. Le plafond est donc le seul mécanisme
**réactif temps réel** — une fenêtre glissante d'une seconde sur l'horloge murale,
*y compris en mode graîné*. C'est indispensable pour exercer le throttling adaptatif
temps réel de la passerelle, qui raisonne lui aussi en messages par seconde.

Conséquence sur les garanties : les profils `throttling-carrier` et
`throughput-capped` sont **hors du corpus de rejeu** de l'invariant (a). Leur
reproductibilité est celle des tests de charge — déterminisme par bind + **agrégation
statistique** (asserter par seuil : « ~X % throttlés sur N messages »), pas un rejeu
octet-pour-octet. L'invariant (a) au sens strict se **prouve sur `flaky-carrier`**.

## Le mode chaos, pour l'exploration

Omettre le `seed` bascule en **mode chaos** : PRNG non graîné, `clock: wallclock`
autorisé et par défaut pour les mécanismes périodiques. Aucune prétention de
reproductibilité — c'est le mode des tests exploratoires (« est-ce que quelque chose
casse sous un mélange imprévisible ? »), pas des assertions CI. Le mode graîné reste
le mode principal ; le chaos est le secondaire.

## En résumé

Le simulateur ne promet pas une reproductibilité magique et globale qu'il ne pourrait
pas tenir. Il promet exactement ce qui est vrai :

- **séquence et contenu reproductibles par session de bind**, à graine fixe ;
- via un **tick logique** (`per_bind_clock`), jamais l'horloge murale, sur les chemins
  déterministes ;
- **drainés au repos** par le flush de quiescence, sans figer le test ;
- avec **deux exceptions explicitement documentées** : les profils à plafond de débit
  (temps réel par nature) et le mode chaos (sans graine).

Cette honnêteté est le trait distinctif du produit : une assertion CI bâtie dessus
repose sur une garantie *réellement vraie*, y compris en topologie multi-bind, en
période de silence, et à travers des transitions de scénario en cours de test.

## Voir aussi

- [explanation/architecture.md](architecture.md) — comment les composants réalisent ce déterminisme.
- [explanation/pourquoi-config-declarative.md](pourquoi-config-declarative.md) — pourquoi tout événement temporel vient du `.yml`.
- [how-to/reproduire-un-test.md](../how-to/reproduire-un-test.md) — la recette concrète.

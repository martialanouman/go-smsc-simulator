# How-to — Reproduire un test à graine fixe

> **Catégorie Diátaxis : Guide pratique.** Objectif : obtenir un comportement
> **identique** d'une exécution à l'autre, pour des assertions CI stables. Pour
> *comprendre* les garanties (et leurs limites), voir
> [explanation/determinisme.md](../explanation/determinisme.md).

## Activer le mode déterministe

Définissez un `seed` sur le SMSC virtuel :

```yaml
virtual_smscs:
  - name: carrier-a
    seed: 42            # présent => mode déterministe
    scenario:
      profile: flaky-carrier
      params: { success_rate: 0.8, error_mix: { ESME_RSYSERR: 1 }, disconnect_interval_ticks: 500 }
```

Avec un `seed`, chaque décision (succès/erreur/timeout/disconnect, latence, résultat de
DLR, contenu de MO) est tirée d'un PRNG graîné par `(seed, per_bind_clock)`. Même graine
+ même séquence d'entrée = même sortie.

## Garantir un ordre global reproductible

Le déterminisme est scopé **par session de bind**. Si votre assertion dépend d'un
**ordre global précis** entre messages, épinglez côté passerelle :

```
bind_pool_size = 1
```

Un seul bind ⇒ ordre global = ordre par bind ⇒ pleinement reproductible. La plupart des
tests fonctionnels à faible volume sont dans ce cas.

## Ne jamais mélanger horloge murale et graine

La validation **refuse** `clock: wallclock` en présence d'un `seed` (erreur
`wallclock clock requires no seed`). En mode déterministe, laissez `clock: logical`
(le défaut) pour `dlr` et `mo_injection`.

De même, `throughput_limit_per_sec` est **interdit** avec un `seed` sur un profil
non-throughput (erreur `throughput_limit_per_sec requires no seed…`) : le plafond de
débit est temps réel et casserait le rejeu. Les profils `throttling-carrier` /
`throughput-capped` en sont exemptés, mais s'asserttent **par seuil statistique**, pas
par rejeu octet-pour-octet.

## Isoler chaque test : relancer, pas réinitialiser

Il n'existe aucun reset à chaud (aucune API mutante). L'isolation propre s'obtient en
**relançant** le simulateur avec la fixture du test :

```bash
smsc-simulator --config fixtures/mon-test.yml
```

Chaque démarrage (< 2 s) repart d'un tampon de PDU vide et d'un `per_bind_clock` à zéro.

## Vérifier la reproductibilité

Exécutez le même scénario deux fois et comparez les PDU/DLR observés via
`GET /v1/virtual-smscs/{id}/received-pdus`. À `seed` fixe et `bind_pool_size = 1`, les
deux séquences sont identiques.

## Voir aussi

- [explanation/determinisme.md](../explanation/determinisme.md)
- [how-to/planifier-des-dlr.md](planifier-des-dlr.md)
- [reference/configuration-yml.md](../reference/configuration-yml.md)

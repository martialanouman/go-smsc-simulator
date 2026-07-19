# Documentation du simulateur SMSC

Cette documentation suit le framework **[Diátaxis](https://diataxis.fr/)**, qui organise
le contenu selon le besoin du lecteur : *apprendre*, *accomplir une tâche*, *chercher une
information*, ou *comprendre*.

| Vous voulez… | Allez vers… |
|---|---|
| **apprendre** en faisant, guidé pas à pas | **[Tutoriels](#tutoriels)** |
| **accomplir** une tâche précise | **[Guides pratiques (how-to)](#guides-pratiques-how-to)** |
| **chercher** un champ, un endpoint, une commande | **[Référence](#référence)** |
| **comprendre** un choix de conception | **[Explication](#explication)** |

> Cette section (`docs/guide/`) est la documentation **utilisateur**. Les documents de
> **conception** (spec, plan d'exécution, style, stratégie de test) restent à la racine
> de `docs/` et font foi en cas de doute.

---

## Tutoriels

*Orientés apprentissage. Un parcours guidé, garanti de réussir, pour prendre en main
l'outil.*

1. [Votre premier carrier simulé](tutorials/01-premier-carrier.md) — lancer un SMSC
   virtuel `healthy` et l'observer.
2. [Tester la résilience avec un carrier qui tombe](tutorials/02-tester-la-resilience.md)
   — scénariser une panne puis une reprise.

## Guides pratiques (how-to)

*Orientés tâche. Chaque guide résout un problème concret.*

- [Reproduire un test à graine fixe](how-to/reproduire-un-test.md)
- [Inspecter les PDU reçues](how-to/inspecter-les-pdu-recues.md)
- [Scraper les métriques Prometheus](how-to/scraper-les-metriques.md)
- [Configurer TLS](how-to/configurer-tls.md)
- [Planifier des DLR asynchrones](how-to/planifier-des-dlr.md)
- [Injecter des messages MO](how-to/injecter-des-mo.md)
- [Planifier déconnexions et transitions](how-to/planifier-deconnexions-et-transitions.md)
- [Plafonner le débit (throttling)](how-to/plafonner-le-debit.md)
- [Injecter des cas limites protocolaires](how-to/injecter-des-cas-limites-protocolaires.md)
- [Héberger plusieurs SMSC virtuels](how-to/heberger-plusieurs-carriers.md)
- [Déployer (Docker, Compose, Kubernetes)](how-to/deployer-avec-docker.md)

## Référence

*Orientée information. Descriptions exhaustives et neutres.*

- [Configuration `.yml`](reference/configuration-yml.md) — le schéma complet.
- [Profils de scénario](reference/profils-de-scenario.md) — les 6 profils et leurs knobs.
- [API d'observabilité HTTP](reference/api-observabilite.md) — les endpoints read-only.
- [Métriques Prometheus](reference/metriques-prometheus.md) — les 5 métriques et labels.
- [Ligne de commande](reference/cli.md) — flags, codes de sortie, arrêt.
- [Commandes Make](reference/commandes-make.md) — les cibles de développement.

## Explication

*Orientée compréhension. Le pourquoi derrière les choix.*

- [Architecture](explanation/architecture.md) — la carte des composants.
- [Le déterminisme, expliqué honnêtement](explanation/determinisme.md) — le cœur du produit.
- [Pourquoi une configuration 100 % déclarative](explanation/pourquoi-config-declarative.md)
- [Pourquoi un catalogue figé de scénarios](explanation/scenarios-predefinis.md)

---

## Les 4 invariants (garanties du produit)

Ces quatre propriétés sont testées et bloquantes — la documentation ci-dessus s'y réfère
constamment :

- **(a) Déterminisme par bind** — à `seed` fixe, la même fixture produit la même séquence
  par bind.
- **(b) Config fail-fast** — toute config invalide échoue au chargement, avant d'ouvrir un
  port ; aucune reconfiguration runtime.
- **(c) HTTP read-only** — aucun endpoint mutant.
- **(d) Flush de quiescence** — une planification au repos est drainée, jamais figée.

## Documents de conception (source de vérité)

- [Spécification technique](../specification-technique-simulateur-smsc.md) (v3.0)
- [Plan d'exécution](../plan-execution-simulateur-smsc.md) (jalons S0–S7)
- [Convention de style Go](../convention-style-go-simulateur-smsc.md)
- [Stratégie de test](../strategie-de-test-simulateur-smsc.md)

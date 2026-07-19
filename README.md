# Simulateur SMSC

Un **serveur SMPP** en Go qui se fait passer pour un SMSC opérateur, afin de tester une
passerelle SMS (le système sous test). Il accepte les binds, répond aux `submit_sm`
selon un **scénario prédéfini paramétré**, émet DLR et MO, et injecte des pannes
(latence, erreurs, timeouts, déconnexions) — le tout **déterministe et reproductible** à
graine fixe.

> **Outil de test / CI, jamais un composant de production.** Ce n'est pas un vrai SMSC,
> pas un service déployé en prod, pas un service reconfigurable à chaud.

## Les 3 idées directrices

1. **Configuration 100 % déclarative.** Un fichier `.yml` chargé et validé au démarrage
   est la **seule** entrée de configuration. Reconfigurer = éditer le fichier et relancer
   (démarrage < 2 s). Aucune mutation runtime.
2. **Scénarios prédéfinis.** Un catalogue **figé** de 6 profils nommés (`healthy`,
   `flaky-carrier`, `throttling-carrier`, `dead-carrier`, `slow-carrier`,
   `throughput-capped`). Le `.yml` sélectionne un profil et règle ses paramètres exposés.
3. **Déterminisme honnête.** À `seed` fixe, tout est reproductible **par session de
   bind**, ancré sur un compteur logique de PDU (`per_bind_clock`), jamais sur l'horloge
   murale.

## Démarrage rapide

```bash
make build                                   # compile bin/smsc-simulator
make run CONFIG=examples/healthy.yml         # lance un carrier healthy
curl -s http://localhost:9000/health         # {"status":"ok"}
curl -s http://localhost:9000/v1/virtual-smscs | jq
```

Ou en conteneur :

```bash
docker compose up                            # carrier plaintext (2775 SMPP + 9000 observabilité)
```

Nouveau sur l'outil ? Suivez le
**[tutoriel « Votre premier carrier simulé »](docs/guide/tutorials/01-premier-carrier.md)**.

## Documentation

La documentation complète est organisée selon **[Diátaxis](https://diataxis.fr/)** dans
**[`docs/guide/`](docs/guide/README.md)** :

| Besoin | Point d'entrée |
|---|---|
| Apprendre en faisant | [Tutoriels](docs/guide/tutorials/01-premier-carrier.md) |
| Accomplir une tâche | [Guides pratiques](docs/guide/README.md#guides-pratiques-how-to) |
| Chercher une info | [Référence](docs/guide/README.md#référence) |
| Comprendre un choix | [Explication](docs/guide/README.md#explication) |

Documents de conception (source de vérité) : [`docs/`](docs/) — spécification, plan
d'exécution, style Go, stratégie de test.

## Commandes

```bash
make build     # compile le binaire
make test      # go test -race ./...  (obligatoire avant toute PR)
make lint      # golangci-lint
make vuln      # govulncheck
make fuzz      # fuzz borné du décodeur PDU
make run CONFIG=examples/<fixture>.yml
make docker    # image de distribution (scratch)
```

Détail : [référence des commandes Make](docs/guide/reference/commandes-make.md).

## Fixtures d'exemple

Le dossier [`examples/`](examples/) contient une fixture **valide** par profil et par
capacité — `healthy`, `flaky-carrier`, `throttling-carrier`, `dead-carrier`,
`slow-carrier`, `throughput-capped`, `scenario-transitions`, `edge-cases`,
`tls-carrier`, `minimal`. Toutes démarrent avec `make run CONFIG=examples/<nom>.yml`.

## Déploiement

En conteneur pour la CI ou un environnement de test partagé :

```bash
docker compose up                            # carrier plaintext clé-en-main (2775 + 9000)
```

En cluster Kubernetes, le dossier [`deploy/`](deploy/) fournit un manifeste complet —
un `ConfigMap` (votre `.yml`), un `Deployment` (1 réplique, `runAsNonRoot`,
`readinessProbe` TCP) et un `Service` ClusterIP exposant `smpp` (2775) et
`observability` (9000) :

```bash
kubectl apply -f deploy/
```

Une seule réplique volontairement : le déterminisme est scopé **par bind**, donc scaler
donnerait à chaque pod son propre état graîné indépendant. Pour changer de scénario,
éditez le `ConfigMap` et **relancez** le pod (aucune reconfiguration à chaud ; démarrage
< 2 s).

Détail complet (image, Compose, isolation en CI) :
[guide « Déployer (Docker, Compose, Kubernetes) »](docs/guide/how-to/deployer-avec-docker.md).

## Les 4 invariants

**(a)** Déterminisme par bind • **(b)** Config fail-fast (aucune reconfiguration runtime)
• **(c)** HTTP strictement read-only • **(d)** Flush de quiescence. Ces garanties sont
testées et bloquantes.

## Architecture en un coup d'œil

Un **binaire unique** héberge N SMSC virtuels (un listener SMPP par port), plus une
**surface HTTP read-only** d'observabilité. Aucune dépendance d'infrastructure — Go pur,
tout en mémoire. Voir [l'explication de l'architecture](docs/guide/explanation/architecture.md).

# How-to — Déployer (Docker, Compose, Kubernetes)

> **Catégorie Diátaxis : Guide pratique.** Objectif : packager et lancer le simulateur
> en conteneur, puis en cluster, pour la CI ou un environnement de test partagé.

## Construire l'image

```bash
make docker VERSION=v1.2.0
# équivaut à : docker build --build-arg VERSION=v1.2.0 -t smsc-simulator:dev .
```

L'image est **minimale** : build multi-stage, image finale `scratch`, binaire statique
(`CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X main.version=…"`), exécutée en
`USER 65534:65534` (nobody). Ports exposés : `2775` (SMPP) et `9000` (observabilité).
`ENTRYPOINT` = `/smsc-simulator`, `CMD` = `["--config", "/etc/smsc/config.yml"]`.

## Lancer un conteneur

Le `.yml` est monté sur `/etc/smsc/config.yml` :

```bash
docker run --rm \
  -p 2775:2775 -p 9000:9000 \
  -v "$PWD/examples/healthy.yml:/etc/smsc/config.yml:ro" \
  smsc-simulator:dev
```

## Docker Compose (clé-en-main)

Le dépôt fournit un `docker-compose.yml` prêt à l'emploi (carrier plaintext) :

```bash
docker compose up
```

Il build l'image (`VERSION: compose`), monte `examples/healthy.yml` en lecture seule sur
`/etc/smsc/config.yml`, et publie `2775` (SMPP) et `9000` (observabilité). Câblez la
passerelle sous test pour qu'elle traite ce port comme un `smsc_connector` — c'est
indistinguable d'une vraie connexion opérateur.

> Le compose est en **plaintext volontairement** : le certificat auto-signé est
> loopback-only et ne couvrirait pas le nom de service `docker-compose`. Pour du TLS
> inter-conteneurs, fournissez `cert_file`/`key_file` — voir
> [how-to/configurer-tls.md](configurer-tls.md).

## Kubernetes

Le dossier `deploy/` fournit un manifeste complet :

```bash
kubectl apply -f deploy/
```

Il contient :

- **`configmap.yaml`** — `ConfigMap` `smsc-simulator-config`, clé `config.yml` (un
  carrier `healthy` sur 2775, observabilité sur 9000). C'est ici que vit votre `.yml`.
- **`deployment.yaml`** — `Deployment` (1 réplique), args
  `["--config", "/etc/smsc/config.yml"]`, ports nommés `smpp` (2775) et `observability`
  (9000), `runAsNonRoot` (65534), requests 64Mi/100m, limits 128Mi/500m,
  `readinessProbe` TCP sur le port `smpp`. Le ConfigMap est monté en lecture seule sur
  `/etc/smsc`.
- **`service.yaml`** — `Service` ClusterIP exposant `smpp` (2775) et `observability`
  (9000).

Pour changer de scénario : éditez le `ConfigMap` et **relancez** le pod (rappel : aucune
reconfiguration à chaud — un nouveau scénario est une nouvelle instance ; démarrage
< 2 s).

## Isolation entre tests en CI

Relancez le conteneur/pod avec la fixture du test plutôt que de tenter un reset à chaud
(il n'en existe pas) : chaque démarrage repart d'un tampon vide et d'un `per_bind_clock`
à zéro.

## Voir aussi

- [reference/cli.md](../reference/cli.md)
- [reference/commandes-make.md](../reference/commandes-make.md)
- [how-to/scraper-les-metriques.md](scraper-les-metriques.md)

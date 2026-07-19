# Référence — Ligne de commande

> **Catégorie Diátaxis : Référence.** Le binaire `smsc-simulator` : ses flags, ses codes
> de sortie et son comportement d'arrêt. Le `.yml` passé à `--config` est la **seule**
> entrée ; il n'existe **aucune** variable d'environnement de configuration.

## Usage

```
smsc-simulator --config <chemin.yml>
smsc-simulator --version
```

## Flags

| Flag | Type | Défaut | Description |
|---|---|---|---|
| `--config` | string | `""` | Chemin du fichier YAML de configuration (**requis**). |
| `--version` | bool | `false` | Affiche la version et quitte. |

Il n'existe **que** ces deux flags.

- `--version` est traité **en premier**, avant tout chargement de config. Sortie :
  `smsc-simulator <version>`. La version vaut `dev` par défaut, injectée au build via
  `-ldflags "-X main.version=…"` (voir `make build`, `Dockerfile`).
- `--config` absent → erreur `no config path given`, l'usage est affiché, sortie **1**.

## Codes de sortie

| Code | Cas |
|---|---|
| `0` | Arrêt propre (SIGTERM/SIGINT) ou `--version`. |
| `1` | Toute erreur : config absente/illisible/invalide, conflit de port, échec de démarrage. Message sur `stderr` préfixé `smsc-simulator: `. |

La validation complète du `.yml` s'exécute **avant** d'ouvrir le moindre port : un
fichier invalide ne laisse jamais de port SMPP à moitié ouvert (invariant b).

## Arrêt (SIGTERM / SIGINT)

À réception de **SIGTERM** ou **SIGINT** (`Ctrl-C`) :

1. le simulateur logue `shutting down` ;
2. il draine le moteur SMPP (unbind propre des binds), puis la surface d'observabilité ;
3. chaque drain est borné par un délai de **5 s** ; un dépassement est **loggé en
   Warn**, sans faire échouer l'arrêt (le teardown CI ne casse jamais sur un drain lent).

## Fournir le fichier

- **En local** : `smsc-simulator --config examples/healthy.yml`
  (ou `make run CONFIG=examples/healthy.yml`).
- **En conteneur** : le `.yml` est monté sur `/etc/smsc/config.yml` et l'image le passe
  via `CMD ["--config", "/etc/smsc/config.yml"]`. Voir
  [how-to/deployer-avec-docker.md](../how-to/deployer-avec-docker.md).

## Voir aussi

- [reference/commandes-make.md](commandes-make.md) — les cibles de développement.
- [reference/configuration-yml.md](configuration-yml.md) — le schéma du fichier.

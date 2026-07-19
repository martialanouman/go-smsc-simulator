# Tutoriel — Votre premier carrier simulé

> **Catégorie Diátaxis : Tutoriel.** Un parcours guidé, pas à pas, garanti de réussir.
> À la fin, vous aurez lancé un SMSC virtuel `healthy` et vous l'aurez observé via son
> API en lecture seule. Aucune connaissance préalable du code n'est requise — seulement
> Go et `curl`.

## Ce que vous allez apprendre

- Lancer le simulateur à partir d'une fixture.
- Comprendre ce qu'est un « SMSC virtuel ».
- Observer son état via la surface HTTP en lecture seule.

Durée : ~10 minutes.

## Prérequis

- **Go 1.26+** installé (`go version`).
- **`curl`** (ou tout client HTTP).
- Le dépôt cloné, vous êtes à sa racine.

## Étape 1 — Regarder la fixture

Ouvrez `examples/healthy.yml`. C'est la configuration **complète** du simulateur — il
n'y en a pas d'autre. Vous y lisez, en clair :

```yaml
observability:
  http_port: 9000          # la surface d'observabilité (lecture seule)

virtual_smscs:
  - name: carrier-healthy
    port: 2775             # le port SMPP où un client se bindera
    bind_credentials:
      system_id: smppclient1
      password: secret
    addr_ton: 1
    addr_npi: 1
    address_range: "^33.*"
    seed: 42               # mode déterministe
    pdu_buffer_size: 10000
    scenario:
      profile: healthy      # 100 % succès, latence fixe basse
      latency: { distribution: fixed, params: { ms: 20 } }
```

Un **SMSC virtuel**, c'est cela : un port SMPP + des identifiants + un profil de
comportement. Le simulateur peut en héberger plusieurs — ici, un seul.

## Étape 2 — Lancer le simulateur

```bash
make run CONFIG=examples/healthy.yml
```

Vous devriez voir des logs `slog` JSON indiquant le démarrage, l'ouverture du listener
SMPP sur `2775` et du serveur d'observabilité sur `9000`. Laissez-le tourner ; ouvrez un
**second terminal** pour la suite.

> Le démarrage est **< 2 s**. Si le `.yml` était invalide, le processus refuserait de
> démarrer **avant** d'ouvrir un port, avec un message nommant le champ fautif — essayez
> plus tard en changeant `profile: healthy` en `profile: inexistant`.

## Étape 3 — Vérifier qu'il est vivant

```bash
curl -s http://localhost:9000/health
```

```json
{"status":"ok"}
```

## Étape 4 — Lister les SMSC virtuels

```bash
curl -s http://localhost:9000/v1/virtual-smscs | jq
```

```json
[
  {
    "name": "carrier-healthy",
    "port": 2775,
    "active_profile": "healthy",
    "bind_count": 0,
    "logical_clock": 0,
    "recorded_pdus": 0
  }
]
```

Lisez cette vue : le profil actif est `healthy`, personne n'est encore bindé
(`bind_count: 0`), aucune PDU reçue. C'est l'état de départ propre — exactement le reflet
du `.yml`.

## Étape 5 — Connecter un client SMPP

Le simulateur attend maintenant qu'un **ESME** (le client SMPP — dans la vraie vie, la
passerelle sous test) se binde sur `localhost:2775` avec `system_id: smppclient1` /
`password: secret`, puis envoie des `submit_sm`.

Dès qu'un client est bindé, réinterrogez :

```bash
curl -s http://localhost:9000/v1/virtual-smscs/carrier-healthy/binds | jq
```

Vous verrez la session apparaître (`system_id`, `bind_type`, `connected_at`). Après
quelques `submit_sm`, le tampon se remplit :

```bash
curl -s http://localhost:9000/v1/virtual-smscs/carrier-healthy/received-pdus | jq '.[0]'
```

Chaque PDU enregistrée montre ce que le client a **réellement** envoyé (adresses,
TON/NPI, codage, et `short_message` en base64). C'est la fonctionnalité d'assertion du
simulateur : vérifier ce que la passerelle a émis.

> Vous n'avez pas de client SMPP sous la main ? Ce n'est pas grave pour ce tutoriel —
> l'essentiel est le modèle : *le simulateur écoute, observe, et expose tout en lecture
> seule*. Pour brancher la passerelle, voir
> [how-to/deployer-avec-docker.md](../how-to/deployer-avec-docker.md).

## Étape 6 — Arrêter proprement

Dans le premier terminal, `Ctrl-C`. Le simulateur logue `shutting down`, débind
proprement les sessions et rend la main. Relancez-le : il repart d'un état **vierge**
(tampon vide, horloge à zéro). C'est ainsi qu'on isole les tests — relancer, pas
réinitialiser.

## Ce que vous avez appris

- Le `.yml` est l'unique configuration, lisible d'un coup d'œil.
- Un SMSC virtuel = port + identifiants + profil.
- La surface HTTP est **en lecture seule** : elle observe, ne modifie jamais.
- Un redémarrage donne un état propre (< 2 s).

## Et ensuite

- [Tutoriel 02 — Tester la résilience](02-tester-la-resilience.md) : passer d'un carrier
  sain à un carrier en panne, et l'observer.
- [Explication — Architecture](../explanation/architecture.md) : la carte des composants.
- [Référence — Configuration `.yml`](../reference/configuration-yml.md) : tous les champs.

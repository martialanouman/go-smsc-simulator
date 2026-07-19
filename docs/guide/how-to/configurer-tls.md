# How-to — Configurer TLS sur un SMSC virtuel

> **Catégorie Diátaxis : Guide pratique.** Objectif : chiffrer le listener SMPP d'un
> SMSC virtuel, pour refléter un connecteur `tls_enabled`. Schéma complet :
> [reference/configuration-yml.md](../reference/configuration-yml.md#tls).

## Option 1 — Certificat auto-signé (loopback)

Le plus simple pour un test local :

```yaml
virtual_smscs:
  - name: carrier-tls
    port: 2775
    tls:
      enabled: true       # aucun cert fourni => auto-signé généré en mémoire au boot
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
```

Au démarrage, un certificat **auto-signé** est généré en mémoire, avec des SAN
**loopback** : `localhost`, `127.0.0.1`, `::1`.

**Conséquence importante :** ce certificat n'est valable qu'en **loopback**. Un client
qui vérifie le nom d'hôte ne l'atteindra que via `localhost`/`127.0.0.1`. Un client
non-loopback — par exemple un service `docker-compose` joignant le simulateur par son
**nom de service** — échouera la vérification. Dans ce cas, passez à l'option 2.

Voir la fixture `examples/tls-carrier.yml`.

## Option 2 — Certificat fourni

Pour un nom d'hôte non-loopback, fournissez votre propre paire PEM :

```yaml
tls:
  enabled: true
  cert_file: /etc/smsc/tls/carrier.crt   # SAN couvrant le nom d'hôte visé
  key_file:  /etc/smsc/tls/carrier.key
```

Règles de validation (fail-fast, au boot) :

- `cert_file` et `key_file` vont **ensemble** — l'un sans l'autre est une erreur
  (`ErrTLSCertKeyMismatch`).
- Un fichier introuvable est une erreur (`ErrTLSCertNotFound`).
- Fournir un cert **sans** `enabled: true` est une erreur (`ErrTLSCertWithoutEnabled`).

Assurez-vous que les SAN du certificat couvrent le nom par lequel le client se connecte.

## Vérifier

```bash
openssl s_client -connect localhost:2775 -servername localhost </dev/null 2>/dev/null \
  | openssl x509 -noout -subject -ext subjectAltName
```

## Note déterminisme

La génération/lecture du certificat a lieu **une seule fois au boot**, hors de tout
chemin déterministe — elle n'affecte ni le `per_bind_clock` ni le rejeu.

## Voir aussi

- [reference/configuration-yml.md](../reference/configuration-yml.md#tls)
- [how-to/deployer-avec-docker.md](deployer-avec-docker.md)

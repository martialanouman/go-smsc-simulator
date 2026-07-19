# How-to — Inspecter les PDU reçues

> **Catégorie Diátaxis : Guide pratique.** Objectif : vérifier ce que la passerelle a
> **réellement** envoyé (adresses, TON/NPI, codage, contenu). Endpoint de référence :
> [api-observabilite.md](../reference/api-observabilite.md).

## Lister les `submit_sm` reçus

```bash
curl -s http://localhost:9000/v1/virtual-smscs/carrier-a/received-pdus | jq
```

Chaque entrée (`RecordedPDUView`) contient `message_id`, `source_addr`, `dest_addr`, les
TON/NPI, `data_coding`, `short_message` (**base64**) et le `per_bind_clock`.

> `{id}` (`carrier-a`) est le champ `name` du SMSC virtuel dans le `.yml`.

## Décoder le contenu

`short_message` est un `[]byte` rendu en base64 par le JSON. Pour le lire :

```bash
curl -s http://localhost:9000/v1/virtual-smscs/carrier-a/received-pdus \
  | jq -r '.[0].short_message' | base64 -d
```

## Filtrer

| Paramètre | Effet |
|---|---|
| `sourceAddr` | Filtre exact sur l'adresse source. |
| `destAddr` | Filtre exact sur l'adresse destination. |
| `since` | Ne renvoie que les PDU dont le `per_bind_clock` ≥ la valeur. |
| `limit` | Nombre max (défaut et plafond : **1000**). |

```bash
# Les messages vers 33700000002, à partir du tick 50, max 100
curl -s 'http://localhost:9000/v1/virtual-smscs/carrier-a/received-pdus?destAddr=33700000002&since=50&limit=100' | jq
```

Une valeur non parsable (ex. `since=abc`) est **ignorée**, jamais rejetée.

## Dimensionner le tampon

Le journal est un **tampon circulaire** de taille `pdu_buffer_size` (requis, ≥ 1). À
fort débit, il boucle vite : à 15 000 msg/s, un tampon de 10 000 boucle en < 1 s.
Augmentez-le si vous devez inspecter tout un test de charge :

```yaml
pdu_buffer_size: 20000
```

## Sur les logs

Le contenu **n'est pas** déversé au niveau `info` dans les logs `slog` — inspectez-le
via cet endpoint, pas via les logs. Le tampon retient volontairement le contenu : c'est
la fonctionnalité d'assertion, pas une fuite.

## Voir aussi

- [reference/api-observabilite.md](../reference/api-observabilite.md)
- [how-to/scraper-les-metriques.md](scraper-les-metriques.md)

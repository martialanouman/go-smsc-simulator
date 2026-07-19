# How-to — Planifier des DLR asynchrones

> **Catégorie Diátaxis : Guide pratique.** Objectif : faire émettre au simulateur des
> accusés de livraison (`deliver_sm` DLR) corrélés aux `submit_sm`, avec un mélange de
> résultats et un délai déterministe.

## Déclarer un bloc `dlr`

Ajoutez un bloc `dlr` au `scenario` :

```yaml
scenario:
  profile: flaky-carrier
  params: { success_rate: 0.9, error_mix: { ESME_RSYSERR: 1 }, disconnect_interval_ticks: 500 }
  latency: { distribution: fixed, params: { ms: 30 } }
  dlr:
    delay: { distribution: fixed, ticks: 5 }          # 5 ticks après le submit_sm d'origine
    outcome_weights: { delivered: 90, failed: 8, expired: 2 }
    clock: logical                                     # défaut ; wallclock interdit avec un seed
```

Pour chaque `submit_sm` **accepté**, un DLR est planifié `delay.ticks` ticks plus tard,
sur le même bind, avec un résultat tiré du mélange pondéré `outcome_weights`.

## Comprendre le délai en ticks

`delay.ticks: 5` signifie **5 `submit_sm` de plus** traités sur ce bind, pas 5 secondes.
C'est ce qui rend le DLR reproductible à `seed` fixe. Actuellement seule la distribution
`fixed` est supportée pour `delay`.

## Corrélation

Le DLR référence le `message_id` (ID SMSC) attribué au `submit_sm_resp` d'origine. Vous
retrouvez ce `message_id` dans le journal des PDU :

```bash
curl -s http://localhost:9000/v1/virtual-smscs/carrier-a/received-pdus | jq '.[].message_id'
```

## Le flush de quiescence : ne pas rester bloqué

Si vous soumettez un lot **puis cessez tout trafic**, le `per_bind_clock` se fige et les
DLR planifiés « 5 ticks plus tard » n'auraient jamais leurs 5 ticks. Le **flush de
quiescence** les draine après `quiescence_flush_ms` (défaut 250 ms) de silence, dans
l'ordre de tick :

```yaml
quiescence_flush_ms: 100    # draine plus vite après le silence
```

C'est l'invariant (d). L'ordre et le contenu des DLR sont préservés ; seule leur latence
murale absolue au repos varie. Voir
[explanation/determinisme.md](../explanation/determinisme.md).

## Observer les DLR

Les DLR sont des `deliver_sm` envoyés au client (ESME) sur son bind receiver/transceiver
— observez-les côté client. Côté simulateur, `smsc_submit_sm_outcome_total` et le
journal des PDU tracent l'activité de soumission qui les a déclenchés.

## Voir aussi

- [reference/configuration-yml.md](../reference/configuration-yml.md#dlr)
- [how-to/injecter-des-mo.md](injecter-des-mo.md)
- [how-to/reproduire-un-test.md](reproduire-un-test.md)

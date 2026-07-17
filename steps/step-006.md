# Step 006 — S6 · Multi-SMSC + TLS par instance + métriques Prometheus

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §10.
> **Statut : ⏳ À FAIRE.**

## Objectif

Un processus héberge **plusieurs SMSC virtuels indépendants** ; **TLS optionnel** par instance ; **métriques Prometheus** exportées par SMSC virtuel avec labels bornés.

## Dépend de

S5.

## Nouvelles dépendances

Aucune (`crypto/tls`, `crypto/x509` stdlib ; `prometheus/client_golang` déjà présent).

## Découpage en tâches

### T1 — Multi-instance
- Le processus instancie **tous** les `virtual_smscs` du `.yml`, un listener SMPP par port (S2 n'en servait qu'un).
- Comportements / scénarios / horloges / PRNG **indépendants** par instance.
- **Isolement de goroutines** : un crash d'un SMSC virtuel n'affecte pas les autres. `recover` de **dernier ressort par SMSC virtuel** (jamais un `recover` global masquant).
- Câbler dans `main.go:run`, après le boot gate, en remplacement de l'instanciation unique de S2.

### T2 — TLS par SMSC virtuel
- Étendre `config.TLSConfig` (aujourd'hui `{ Enabled bool }`) : champs certificat/clé optionnels + validation §3.1 associée (⚠️ toucher `internal/config` = mettre à jour struct + validation + fixture + spec §3.1).
- **Génération intégrée d'un certificat auto-signé** si `tls.enabled` et aucun cert fourni (reflète `tls_enabled` du connecteur passerelle).
- Le listener SMPP de l'instance passe en TLS (`crypto/tls`) quand activé.

### T3 — `internal/metrics` + `GET /metrics`
- Compteurs / histogrammes Prometheus **par SMSC virtuel** : binds actifs, `submit_sm` reçus, résultats servis par type, scénario actif, latence servie.
- **Labels bornés** : `virtual_smsc`, `bind_type`, `outcome`, `scenario`. **JAMAIS** de MSISDN / `message_id` / contenu en label (cardinalité non bornée = fuite mémoire + règle d'or).
- Créer et threader le **registre Prometheus** (le STUB S6 de `main.go` mentionne exactement ce point) ; exposer `GET /metrics` (chemin **nu**, sans `/v1`).

## Hors périmètre (→ S7)

PDU malformées et packaging CI/CD.

## Critères d'acceptation (tests)

- [ ] Un `.yml` à **3 SMSC virtuels** → 3 listeners indépendants ; un `dead-carrier` sur l'un n'affecte pas un `healthy` sur l'autre (test multi-instance).
- [ ] Bind **TLS** réussi avec certificat auto-signé généré ; bind non-TLS refusé si `tls.enabled` (et inversement).
- [ ] `GET /metrics` expose les compteurs par SMSC virtuel.
- [ ] **Test de garde** : échoue si un label à cardinalité non bornée (MSISDN / `message_id`) apparaît.
- [ ] `go test -race` vert sous charge multi-instance.

## Risques & points d'attention

- **Recover par instance** : un `recover` mal placé peut masquer un bug de déterminisme. Le limiter à l'isolement d'instance, logguer bruyamment, et re-panic en test si possible.
- **API Prometheus** : passer par `ctx7` pour `prometheus/client_golang` (constructeurs de vecteurs, histogrammes, registre custom) — ne pas deviner la signature.
- **Génération cert auto-signé** : durée de validité, SAN `localhost`/127.0.0.1 pour que le client de test se connecte ; **jamais** en chemin déterministe (génération au boot, pas par PDU).
- **Cardinalité des labels** : le test de garde est un invariant de fait — l'implémenter en listant les label values observées et en refusant tout ce qui n'est pas dans l'ensemble borné.
- Changement de schéma `.yml` (TLS) : recette CLAUDE.md « Changer le schéma » — struct + validation + spec §3.1 + fixture.

## Definition of Done

§0.4 du plan + mise à jour `CLAUDE.md` (nouveau package `internal/metrics`) et spec §3.1 (champs TLS).

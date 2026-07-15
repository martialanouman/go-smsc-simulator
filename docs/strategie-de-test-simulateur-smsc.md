# Stratégie de test — Simulateur SMSC

**Composant :** Simulateur SMSC configurable (Go) — outil de test/CI
**Spécification de référence :** `specification-technique-simulateur-smsc.md` (v3.0)
**Statut :** Stratégie de test v1.0

> *La prose est en français ; le code et les commentaires de code sont en anglais.*

Le simulateur est **lui-même** un outil de test — il doit donc être irréprochable là où on lui fait confiance : le **déterminisme**, la **conformité SMPP**, et la **fidélité de ses assertions**. Particularité clé : il n'a **aucune dépendance d'infrastructure** (pas de Kafka/Postgres/Redis/ClickHouse — spec §1.2). Ses tests sont donc du **Go pur** + un **client SMPP in-process**, sans `testcontainers`, rapides et hermétiques.

---

## 1. Pyramide de test

De la base au sommet :

1. **Unitaires (majorité).** Logique de domaine pure et table-driven : codec PDU (round-trip), sélection de résultat pondérée, distributions de latence, calcul des ticks, validation de config. Rapides, sans I/O.
2. **Déterminisme (rejeu) — le cœur.** À `seed` fixe, deux exécutions de la même fixture produisent la même séquence par bind. Le test le plus important du projet (§3).
3. **Intégration bout-en-bout.** Un **client SMPP in-process** pilote un ou plusieurs SMSC virtuels ; assertions via la surface read-only. Vérifie le comportement observable réel.
4. **Fuzz.** `go test -fuzz` sur le décodeur PDU — surface d'entrée non fiable (§5).
5. **Charge/NFR (au jalon S7).** Débit ≥ 15 000 msg/s par SMSC virtuel, démarrage < 2 s, empreinte < 50 Mo au repos (§6).

**[MUST]** `go test -race ./...` en CI ; aucune fusion sans CI vert. Détection de course activée — un test qui passe sans `-race` mais échoue avec a trouvé un bug.

---

## 2. Le client SMPP in-process (pair de test)

Le simulateur est un serveur SMPP. Pour le tester de bout en bout, on a besoin d'un **client SMPP** qui se binde, soumet, reçoit les réponses et les `deliver_sm` (DLR/MO).

**[MUST]** Le client de test réutilise le **codec `internal/smpp`** en mode client (encoder `bind_*`/`submit_sm`/`enquire_link`/`unbind`, décoder `*_resp`/`deliver_sm`). Pas de bibliothèque SMPP externe : le codec est déjà là, et le tester en boucle client↔serveur le valide doublement.

**[SHOULD]** Un helper de test (`internal/smpptest` ou `testsupport`) expose une API ergonomique : `Bind(t, addr, creds) *Client`, `client.Submit(dest, body) Resp`, `client.NextDeliver(timeout) PDU`, `client.EnquireLink()`, `client.Unbind()`. Les tests lisent alors comme des scénarios, pas comme de la plomberie TCP.

**[MUST]** Les tests ouvrent le serveur sur un **port éphémère** (`:0`) pour la parallélisation, jamais un port fixe codé en dur. La surface read-only écoute aussi sur `:0` en test.

---

## 3. Tests de déterminisme (invariant a) — priorité absolue

C'est la propriété que tout le reste vend. Elle se teste par **rejeu**.

**[MUST]** Test de rejeu canonique : charger une fixture avec `seed` défini, exécuter une séquence fixe de `submit_sm` sur un bind, **capturer** la séquence de résultats observés (codes de réponse, latences en ticks, résultats DLR, MO émis). Rejouer à l'identique. **Les deux captures doivent être égales, octet pour octet.**

```go
// TestDeterminism_Replay asserts that a seeded scenario is reproducible per bind.
// Two runs of the same fixture + same submit sequence yield identical outcome streams.
func TestDeterminism_Replay(t *testing.T) {
    first := runScenario(t, "examples/flaky.yml", submitSeq)
    second := runScenario(t, "examples/flaky.yml", submitSeq)
    if diff := cmp.Diff(first, second); diff != "" {
        t.Fatalf("non-deterministic outcome stream (-first +second):\n%s", diff)
    }
}
```

**[MUST]** Couvrir chaque mécanisme temporel par un test de rejeu : résultats de `submit_sm`, latences (`spike` compris), résultats DLR, MO planifiés, transitions de scénario. Chaque jalon qui ajoute un mécanisme temporel (S3, S4, S5) **étend** ce corpus.

**[MUST]** **Test de garde anti-horloge-murale** : un test (ou un lint `gocritic`/revive custom + revue) vérifie qu'aucun `time.Now()`/`time.Since()`/PRNG global n'est appelé sur un chemin de décision graîné. En pratique : injecter une horloge de test qui **panique si lue** en mode graîné, et vérifier qu'un run graîné ne la lit jamais.

**[SHOULD]** **Portée par bind, pas globale.** Un test explicite documente que l'ordre *global* entre binds concurrents n'est **pas** garanti (spec §6.3) : deux binds parallèles peuvent entrelacer différemment leur `logical_clock`, mais chacun reste reproductible sur son `per_bind_clock`. Ce test évite qu'on « corrige » un faux bug de non-déterminisme global.

---

## 4. Tests par capacité (alignés sur les jalons)

**[MUST]** **Validation de config (invariant b, S1).** Table-driven : chaque `examples/*.yml` valide charge ; chaque fixture invalide **échoue au chargement** avec l'erreur attendue (profil inconnu, `clock: wallclock` + `seed`, port en doublon, `to_profile` inconnu, paramètre hors bornes). Un test vérifie qu'**aucun port n'est ouvert** quand la config est invalide.

**[MUST]** **Codec PDU (S2).** Round-trip `encode∘decode = identité` sur un corpus de PDU (bind types, `submit_sm` avec TLV/UDH, payload > 254 o, `deliver_sm` DLR et MO). Cas d'erreur : longueur incohérente, `command_id` inconnu → erreur, pas de panique.

**[MUST]** **Surface read-only (invariant c, S2).** Un test énumère les endpoints et vérifie que tout verbe ≠ `GET` renvoie 404/405 et **ne modifie pas** l'état. Un test vérifie que le bloc `observability` omis ⇒ aucun serveur HTTP.

**[MUST]** **Profils (S3).** Un test par profil vérifie le comportement caractéristique : `throttling-carrier` → `ESME_RTHROTTLED` au-delà du plafond ; `dead-carrier` → refus de bind ou timeout ; `slow-carrier` → latence bornée ; `flaky-carrier` → mix succès/erreur dans la tolérance statistique sur N ; `healthy` → 100 % succès ; `throughput-capped` → plafond respecté.

**[MUST]** **Flush de quiescence (invariant d, S4).** Soumettre un lot puis cesser le trafic ; vérifier que les DLR/MO en attente sont **drainés** (émis) après `quiescence_flush_ms`, dans l'ordre de tick. Ce test garde la propriété qui rend le simulateur utilisable dans le cas CI majoritaire (batch + attente).

**[MUST]** **Transitions planifiées (S5).** Fixture `healthy → dead-carrier → healthy` sur des plages de ticks ; vérifier le résultat attendu à chaque plage et la reproductibilité. C'est le test qui prouve qu'on peut scénariser « ouverture puis reprise du disjoncteur » sans API runtime.

**[MUST]** **Multi-SMSC & isolation (S6).** Un `.yml` à ≥ 3 SMSC virtuels ; vérifier que les comportements sont indépendants (un `dead-carrier` n'affecte pas un `healthy` voisin) et qu'un `recover` par SMSC virtuel isole les crashes.

**[MUST]** **TLS (S6).** Bind TLS réussi avec certificat auto-signé généré ; incohérence TLS/non-TLS refusée.

**[MUST]** **Métriques (S6).** `GET /metrics` expose les compteurs par SMSC virtuel ; **test de garde** échouant si un label à cardinalité non bornée (MSISDN/`message_id`/contenu) apparaît.

---

## 5. Fuzz

**[MUST]** `go test -fuzz` sur le **décodeur PDU** (`internal/smpp`) : aucune panique, aucune allocation non bornée, aucune boucle infinie sur entrée hostile (longueurs négatives/énormes, TLV tronqués, `command_length` mensonger). Un corpus de graines couvre les PDU réelles et des cas dégénérés.

**[SHOULD]** Fuzz du **chargeur de config** sur des `.yml` malformés : il doit toujours échouer proprement (fail-fast), jamais paniquer ni démarrer dans un état ambigu.

**[SHOULD]** Le profil de **cas limites protocolaires** (S7, `protocol_edge_cases_enabled`) a ses propres tests : les PDU malformées ne sont injectées **que** lorsqu'il est activé ; désactivé (défaut), le parsing est strict.

---

## 6. Charge & NFR (jalon S7)

**[MUST]** Campagne de charge avec un générateur de binds/`submit_sm` (le harnais de la passerelle ou un outil SMPP externe, hors `go.mod`) :

- **Débit** : ≥ 15 000 msg/s soutenu **par SMSC virtuel** (profil `throughput-capped`/`healthy`), latence servie conforme.
- **Démarrage à froid** : < 2 s du lancement à l'acceptation du premier bind.
- **Empreinte** : < 50 Mo de mémoire de base par SMSC virtuel au repos.
- **Déterminisme sous charge multi-bind** : la reproductibilité **par bind** tient ; l'agrégation statistique (mix de résultats) reste dans la tolérance.

**[SHOULD]** Benchmarks (`testing.B`) sur le chemin chaud : décodage PDU, sélection de résultat, tirage de latence. Comparer avec `benchstat` entre versions pour détecter une régression.

---

## 7. Ce qu'on ne teste PAS (et pourquoi)

- **Pas de `testcontainers`.** Le simulateur n'a aucune dépendance d'infrastructure ; monter des conteneurs serait du poids mort. Tout est en mémoire.
- **Pas de tests de reconfiguration runtime.** Il n'existe aucun chemin de mutation runtime (invariant b) — il n'y a rien à tester de ce côté, sinon l'**absence** de ce chemin (couverte par le test read-only c et l'immuabilité de la config).
- **Pas de mocks lourds.** Fakes écrits à la main (une struct implémentant l'interface consommateur) ou le vrai composant in-process. Le simulateur est petit et hermétique — le vrai code est souvent plus simple à instancier qu'un mock.

---

## 8. Definition of Done — volet test (chaque PR)

`go test -race ./...` vert • critères d'acceptation du jalon couverts par des tests • les invariants concernés (a déterminisme, b fail-fast, c read-only, d quiescence) restent verts • fuzz du décodeur exécuté (durée bornée en CI) sur les PR touchant `internal/smpp` • aucun endpoint mutant introduit • aucun label Prometheus non borné introduit • aucune source de non-déterminisme sur un chemin graîné.

---

## 9. Récapitulatif — la matrice invariant × jalon

| Invariant | Posé à | Étendu à | Test gardien |
|---|---|---|---|
| (a) Déterminisme par bind | S3 | S4, S5, S7 | rejeu à `seed` fixe + garde anti-horloge-murale |
| (b) Config fail-fast | S1 | — | validation table-driven ; aucun port ouvert si invalide |
| (c) HTTP read-only | S2 | S6 | tout verbe ≠ GET refusé ; état inchangé |
| (d) Flush de quiescence | S4 | S5 | batch + silence → drain observé dans l'ordre de tick |

Ces quatre lignes restent **vertes à vie**. Une PR qui en casse une ne fusionne pas.

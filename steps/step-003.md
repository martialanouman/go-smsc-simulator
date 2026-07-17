# Step 003 — S3 · Moteur de scénario + injecteur de panne + déterminisme graîné

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §7.
> **Statut : ⏳ À FAIRE.** Pose l'**invariant (a) — déterminisme par bind**.

## Objectif

Activer les **6 profils prédéfinis paramétrés** et l'**injection de panne** (résultats pondérés, latences), avec un déterminisme graîné **vérifiable par rejeu**. Les STUB de S2 disparaissent.

## Dépend de

S2.

## Nouvelles dépendances

Aucune (`math/rand/v2` stdlib).

## Découpage en tâches

### T1 — `internal/rng` : PRNG déterministe
- `math/rand/v2` **par SMSC virtuel / par bind**, graine dérivée de `seed` (ex. `seed ⊕ hash(bind_index)` ou dérivation `rand/v2`).
- **Jamais** `time.Now()` ni PRNG non graîné sur un chemin de décision.
- Mode **chaos** si `seed` absent (PRNG non graînée ; `clock: wallclock` alors autorisé).
- La séquence de tirages est fonction de `(seed, per_bind_clock)` — pas de l'ordre d'arrivée réseau.

### T2 — `internal/scenario` : catalogue comportemental figé (6 profils)
Catalogue **distinct** du catalogue de validation (`internal/config/profiles.go`). Pour chaque profil, la logique de sélection de résultat pondérée ancrée sur `(seed, per_bind_clock)` :
- `healthy` — 100 % succès.
- `flaky-carrier` — mix succès/erreur selon `success_rate` + `error_mix` ; `disconnect_interval_ticks` optionnel.
- `throttling-carrier` — `ESME_RTHROTTLED` au-delà de `throughput_cap_per_sec` (+ `error_code`).
- `dead-carrier` — selon `mode` : `reject_bind` (`ESME_RBINDFAIL`) **ou** `timeout_all`.
- `slow-carrier` — latence bornée (2–4 s), aucune erreur.
- `throughput-capped` — plafond `throughput_cap_per_sec`.
- **Sélection pondérée** : `outcome ∈ {success, error+errorCode, timeout, disconnect}` tirée du PRNG au tick courant.

### T3 — `internal/fault` : injecteur de panne
- **Distributions de latence** : `fixed`, `uniform`, `normal` (tronquée à ≥ 0), `spike` (intervalle en **ticks** `per_bind_clock`, pas en horloge murale).
- **Timeout** = rétention du `submit_sm_resp` (jamais renvoyé).
- **Disconnect** = coupure TCP **avant** ou **après** réponse selon config.
- **Plafond `throughput_limit_per_sec`** → `ESME_RTHROTTLED`.
- Toutes les valeurs (latence en ticks, choix de panne) proviennent du PRNG graîné.

### T4 — Câblage dans le flux `submit_sm`
Ordre du flux (CLAUDE.md) : décodage → Scenario Engine (incrémente `per_bind_clock`/`logical_clock`) → sélection pondérée `(seed, per_bind_clock)` → Fault Injector (latence/erreur/timeout/disconnect) → `submit_sm_resp`. Remplacer les STUB `healthy` de S2.

## Hors périmètre (→ jalons suivants)

- DLR (S4), MO et déconnexions/transitions **planifiées** (S5), flush de quiescence (S4).
- Ici les pannes sont **synchrones** sur `submit_sm` (pas d'événement asynchrone planifié).

## Critères d'acceptation (tests)

- [ ] `throttling-carrier` / `throughput-capped` : au-delà du plafond → `ESME_RTHROTTLED` ; en deçà → succès.
- [ ] `dead-carrier` : selon `mode`, refuse le bind (`ESME_RBINDFAIL`) **ou** timeout sur chaque `submit_sm`.
- [ ] `slow-carrier` : latence bornée (2–4 s) appliquée, aucune erreur.
- [ ] `flaky-carrier` : mix succès/erreur dans la **tolérance statistique** sur N messages.
- [ ] **Invariant (a)** : à `seed` fixe, deux exécutions de la même fixture produisent la **même séquence** de résultats + latences (en ticks) **par bind** (test de rejeu comparant deux runs). Le test de déterminisme est **le plus important du projet**.
- [ ] Mode chaos (`seed` absent) : reproductibilité de **séquence/contenu** seulement, pas de timing — le test ne doit pas exiger de timing reproductible sans seed.

## Risques & points d'attention

- **Fuite d'horloge murale** : le piège central. Le `spike` et toute latence doivent lire `per_bind_clock`, jamais `time.Now()`. Ajouter un test/garde qui échoue si un chemin de décision appelle l'horloge en mode graîné.
- **Dérivation de graine par bind** : documenter la formule ; deux binds concurrents ne doivent pas partager de flux PRNG (déterminisme scopé par bind, pas global).
- **Tolérance statistique** : choisir N assez grand et une marge honnête pour `flaky-carrier` (test non-flaky sur un générateur pseudo-aléatoire → utiliser le seed fixe, pas une vraie proba).
- Le mapping latence-en-ticks → application réelle : à S3 la latence est **servie** (délai avant `submit_sm_resp`) ; garder l'ancrage tick pour que S4/S5 le réutilisent.

## Definition of Done

§0.4 du plan. Réaffirmer l'invariant (a) dans la suite de tests de déterminisme (elle sera étendue à S4 et S5).

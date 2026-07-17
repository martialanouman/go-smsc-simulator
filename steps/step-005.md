# Step 005 — S5 · MO planifiés + déconnexions & transitions de scénario planifiées

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §9.
> **Statut : ⏳ À FAIRE.** Complète les **trois formes déclaratives d'événements temporels**, toutes drainées par le Schedule Runner de S4.

## Objectif

Ajouter MO planifiés, déconnexions planifiées et transitions de scénario planifiées — le tout ancré sur `per_bind_clock` et drainé par le Schedule Runner (invariant d). Rendre reproductible le pattern `healthy → dead-carrier → healthy` (ouverture/fermeture du disjoncteur de la passerelle).

## Dépend de

S4 (réutilise `internal/schedule`).

## Nouvelles dépendances

Aucune.

## Découpage en tâches

### T1 — MO Injector (`mo_injection`)
- Mode **`scheduled`** : événements `at_tick` avec `source_addr` / `dest_addr` / `content` → `deliver_sm` MO émis via le Schedule Runner.
- Mode **`auto`** : `rate_per_sec` + `content_template` → génération de MO. En mode graîné, **ancré aux ticks** (`clock: logical` imposé si `seed`) ; `wallclock` **seulement** en chaos.
- Émission `deliver_sm` MO (message montant, pas un receipt) sur un bind RX/TRX.

### T2 — Déconnexions planifiées (`scheduled_disconnects[]`)
- `at_tick` + `scope` (`all` | `oldest` | `random`) + `when` (`before_response` | `after_response`).
- Au tick prévu, couper les binds ciblés selon `scope`, au moment relatif à la réponse selon `when`.
- `random` tire du PRNG graîné → reproductible.
- Le changement d'état de bind est visible dans `GET /binds`.

### T3 — Transitions de scénario planifiées (`scheduled_transitions[]`)
- `at_tick` + `to_profile`. `active_scenario` avance **UNIQUEMENT** par ces transitions — **aucun** chemin de mutation runtime (règle d'or CLAUDE.md + spec §6.1).
- Le profil actif change au tick prévu ; le moteur de scénario (S3) lit le profil courant à chaque `submit_sm`.
- Exposer le profil actif courant dans un observable read-only (ex. champ de `GET /virtual-smscs/{id}`), pour l'assertion.

### T4 — Câblage commun
Les trois formes s'enregistrent dans le **même** Schedule Runner (S4) : même mécanisme de tick, même flush de quiescence, même ordre déterministe.

## Hors périmètre (→ S6/S7)

TLS et métriques (S6) ; PDU malformées (S7).

## Critères d'acceptation (tests)

- [ ] **MO `scheduled`** : un `deliver_sm` MO émis au **bon tick**, contenu conforme, **reproductible** à `seed` fixe.
- [ ] **MO `auto`** : débit approximatif respecté ; en mode graîné, ancré aux ticks (pas d'horloge murale).
- [ ] **Transition** : test « `healthy` (ticks 0–199) → `dead-carrier` (200–399) → `healthy` (400+) » produit exactement les résultats attendus à chaque plage, **reproductible**.
- [ ] **Déconnexion planifiée** : au tick prévu, les binds ciblés (`scope`) sont coupés selon `when` ; visible dans `GET /binds`.
- [ ] **Invariant (d)** : MO/transitions/déconnexions en attente au repos sont drainés par le flush de quiescence.
- [ ] **Invariant (a)** réaffirmé sur ces trois mécanismes (rejeu à seed fixe).

## Risques & points d'attention

- **Transitions et déterminisme par bind** : `active_scenario` est-il scopé SMSC virtuel ou bind ? La transition est déclarée globalement (§3.1) mais l'horloge de référence est `per_bind_clock` — clarifier quel tick déclenche la transition quand plusieurs binds coexistent (probablement : par bind, chaque bind franchit `at_tick` indépendamment). À trancher et documenter avant T3.
- **MO `auto` en mode graîné** : « débit par seconde » ancré en ticks — définir la conversion tick↔débit sans lire l'horloge murale.
- **`scope: random`** doit tirer du PRNG graîné du bind concerné pour rester reproductible.
- Réutiliser strictement le Schedule Runner de S4 (ne pas dupliquer une file d'événements).

## Definition of Done

§0.4 du plan. Étendre les suites d'invariants (a) et (d) aux trois nouveaux mécanismes.

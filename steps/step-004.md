# Step 004 — S4 · DLR asynchrones + horloge logique + flush de quiescence

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §8.
> **Statut : ⏳ À FAIRE.** Pose l'**invariant (d) — flush de quiescence** et le **Schedule Runner** (réutilisé à S5).

## Objectif

Générer des **DLR asynchrones corrélés**, ancrés au `per_bind_clock`, et garantir qu'une planification laissée au repos **ne se fige pas** (drain par quiescence).

## Dépend de

S3.

## Nouvelles dépendances

Aucune.

## Découpage en tâches

### T1 — `internal/schedule` : Schedule Runner (par bind)
- `pending_logical_schedule` : ensemble **ordonné** d'événements dus à un tick futur (`per_bind_clock`).
- Deux voies de drain :
  - **(a) normale** : à l'atteinte du tick en fonctionnement (nouveau `submit_sm` fait avancer l'horloge).
  - **(b) flush de quiescence** : après `quiescence_flush_ms` (**défaut 250 ms**) sans nouveau `submit_sm`, les événements en attente sont drainés **dans l'ordre de tick déterministe**.
- Le runner est **par bind** (scope du déterminisme). Condition d'arrêt propre sur ctx/unbind.
- ⚠️ Le timer de quiescence est le **seul** usage d'horloge murale toléré ici, et il ne décide **jamais** du contenu/ordre (déterministe) — il ne fait que déclencher le drain d'événements déjà planifiés en ticks.

### T2 — DLR Scheduler
- Pour un `submit_sm` **soumis avec succès**, planifier un `deliver_sm` DLR.
- **Délai** ancré au tick du `submit_sm` d'origine + `dlr.delay` (distribution `fixed` ticks ; activer `uniform` min/max ticks déjà modélisé dans `DLRDelay`).
- **Mix de résultats** `delivered` / `failed` / `expired` selon `outcome_weights` (tiré du PRNG graîné → reproductible).
- **Corrélation** : l'ID SMSC attribué au `submit_sm_resp` (`smsc_msg_id`) est référencé par le DLR. Émission via `deliver_sm` (champ `receipted_message_id` / short_message DLR format).

### T3 — Câblage
- Brancher le DLR Scheduler sur le Schedule Runner (T1), lui-même alimenté par le flux `submit_sm` de S3.
- `GET /logical-clock` reste l'observable global (déjà à S2, inchangé).
- Journaliser + compter tout DLR dont le message d'origine est **inconnu/expiré** (jamais d'émission silencieuse sur mauvais mapping).

## Hors périmètre (→ S5)

MO et déconnexions/transitions planifiées — **réutiliseront** le même Schedule Runner. Pas de TLS ni métriques.

## Critères d'acceptation (tests)

- [ ] **Invariant (d)** : soumettre un lot puis cesser le trafic → les DLR en attente sont **délivrés** après le flush de quiescence, dans l'ordre de tick (test : batch + silence, observation des `deliver_sm` DLR).
- [ ] **Invariant (a) étendu** : à `seed` fixe, la séquence de résultats DLR (`delivered`/`failed`/`expired`) et leur ordre de tick sont **reproductibles**.
- [ ] **Corrélation** : chaque DLR référence l'`smsc_msg_id` du `submit_sm` d'origine (test de corrélation).
- [ ] Un DLR au message d'origine inconnu/expiré est **journalisé + compté**, jamais émis en silence.
- [ ] `go test -race` vert : le Schedule Runner (timer quiescence + goroutine de session) sans data race.

## Risques & points d'attention

- **Le flush de quiescence est subtil** : le timer ne doit ni réordonner ni inventer d'événements ; il draine ce qui est déjà planifié, en ordre de tick. Bien séparer « quand drainer » (peut dépendre du wallclock en quiescence) de « quoi/dans quel ordre drainer » (strictement déterministe en ticks).
- **Corrélation `smsc_msg_id`** : format du receipt DLR (SMPP v3.4 : short_message texte `id:... stat:... err:...`) — vérifier via `ctx7`/spec le format attendu par la passerelle sous test.
- **Émission asynchrone vs bind receiver/transceiver** : un DLR (`deliver_sm`) ne peut partir que sur un bind capable de recevoir (RX/TRX). Gérer le cas où le bind d'origine est TX seul.
- Interaction avec S3 `disconnect`/`timeout` : un `submit_sm` qui n'a pas « réussi » ne planifie pas de DLR.

## Definition of Done

§0.4 du plan. Étendre la suite de déterminisme (invariant a) au canal DLR et ajouter la suite d'invariant (d).

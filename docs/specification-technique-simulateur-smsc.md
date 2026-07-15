# Simulateur SMSC — Spécification Technique
**Modèle :** RESHADED (Requirements → Estimation → Storage Schema → High-Level Design → API Design → Detailed Design → Evaluation → Distinctive Component)
**Composant :** Simulateur SMSC configurable (Go) — outil de test/CI
**Documents compagnons :** `specification-technique-passerelle-sms.md` (système sous test), `specification-technique-tableau-de-bord.md`
**Statut :** v3.0

*Note de convention : les blocs de code (schémas, config YAML, endpoints, diagrammes, JSON, y compris leurs commentaires) restent en anglais. Seul le texte narratif est en français.*

---

## 1. Exigences (Requirements)

### 1.1 Exigences fonctionnelles

- **Émulation de serveur SMPP** — accepte les binds SMPP (`bind_transmitter`/`bind_receiver`/`bind_transceiver`) exactement comme un vrai SMSC opérateur, pour que `connector-pool-svc` de la passerelle (§4.1 compagnon) s'y connecte comme à un vrai connecteur, sans changement de code côté passerelle.
- **Plusieurs SMSC virtuels simultanés** — un seul processus héberge plusieurs SMSC virtuels configurés indépendamment dans le `.yml` (ports, identifiants, TON/NPI, TLS, profils de comportement), pour exercer plusieurs connecteurs (routes multi-connecteurs, distribution, bascule) contre un seul simulateur.
- **Résultats de `submit_sm` configurables** — par SMSC virtuel via le profil de scénario paramétré dans le `.yml` : mélange pondéré de succès, code d'erreur SMPP spécifique (`ESME_RTHROTTLED`, `ESME_RSUBMITFAIL`, `ESME_RINVDSTADR`…), timeout, ou coupure de connexion en cours de transaction.
- **Injection de latence configurable** — par profil, une distribution (fixe, plage uniforme, normale, ou rafales périodiques « spike ») appliquée avant de répondre, paramétrée dans le `.yml`.
- **Génération de DLR configurable** — `deliver_sm` DLR asynchrones avec distribution de délai et mélange de résultats (livré/échec/expiré), corrélés au `submit_sm` d'origine ; paramètres dans le `.yml`.
- **Injection de messages MO planifiée** — `deliver_sm` non sollicités, déclarés dans le `.yml` soit comme **événements ponctuels ancrés à un tick logique** (tests déterministes), soit auto-planifiés à un débit configuré (tests de charge/endurance du chemin MO). L'ordre et le moment d'un MO sont fixés par le fichier, donc reproductibles.
- **Simulation d'instabilité de connexion** — coupures de bind configurables (probabilité/intervalle en ticks, ou déconnexions planifiées à des ticks précis) pour exercer le disjoncteur (§6.15 compagnon) et la reconnexion automatique (§6.13 compagnon).
- **Simulation de plafond de débit** — un SMSC virtuel applique son propre plafond (paramètre du profil), retournant `ESME_RTHROTTLED` au-delà, pour exercer le throttling adaptatif de la passerelle (§6.4 compagnon).
- **Profils de scénario prédéfinis** — catalogue figé de préréglages nommés (`healthy`, `flaky-carrier`, `throttling-carrier`, `dead-carrier`, `slow-carrier`, `throughput-capped`) sélectionnés et paramétrés par SMSC virtuel dans le `.yml`. Une **transition de scénario en cours de test** (ex. `healthy` → `dead-carrier` → `healthy` pour tester ouverture puis reprise du disjoncteur) se déclare comme une **transition planifiée sur tick logique** dans le `.yml`, ce qui la rend reproductible.
- **Mode déterministe/à graine** — une graine PRNG fixe par SMSC virtuel (champ `seed` du `.yml`) produit toujours la même séquence de résultats et de latences, pour des assertions CI reproductibles ; un mode « chaos » (`seed` absent) est disponible pour les tests exploratoires. Le déterminisme du mode à graine s'appuie sur un compteur logique de PDU par session de bind, jamais sur l'horloge murale (§6.3).
- **Enregistrement de PDU** — journal borné et interrogeable des `submit_sm` reçus par SMSC virtuel, pour vérifier ce que la passerelle a réellement envoyé (adresses, contenu, TON/NPI, codage). Exposé **en lecture seule** via la surface d'observabilité (§5.1).
- **Support TLS** — optionnel par SMSC virtuel (bloc `tls` du `.yml`), avec génération intégrée de certificat auto-signé, reflétant `tls_enabled` du connecteur (§3.1 compagnon).
- **Injection de cas limites protocolaires** — opt-in par profil dans le `.yml`, PDU malformées (longueur invalide, `command_id` invalide, numéros de séquence hors ordre) pour tester la robustesse du parsing — désactivé par défaut.
- **Export de métriques** — métriques Prometheus par SMSC virtuel (binds, `submit_sm` reçus, résultats servis, scénario actif) exposées en **lecture seule** sur la surface d'observabilité, pour vérifier le trafic observé côté simulateur.
- **Intégration CI/CD** — une image Docker unique, un `.yml` monté comme unique entrée de configuration, un `docker-compose.yml` d'exemple câblant le simulateur comme connecteur(s) SMSC, et un modèle de Job Kubernetes pour les pipelines.

### 1.2 Exigences non fonctionnelles

| Catégorie | Cible |
|---|---|
| Débit par instance | Chaque SMSC virtuel soutient au moins la part de trafic de pic par connecteur (jusqu'à 15 000+ msg/s pour les tests de charge dédiés) |
| Temps de démarrage | < 2 s à froid, config lue depuis le `.yml` — un démarrage/arrêt peu coûteux par exécution de test remplace toute mutation runtime |
| Empreinte | Binaire Go statique unique ; < 50 Mo de mémoire de base par SMSC virtuel au repos |
| Déterminisme (séquence/contenu) | Reproductible **par session de bind** pour une graine et une séquence d'entrée données (codes d'erreur, choix de DLR, contenu MO, transitions planifiées). L'ordre global entre binds concurrents n'est pas garanti (§6.3) |
| Déterminisme (timing) | Reproductible uniquement pour les mécanismes exprimés en ticks logiques ; les mécanismes en horloge murale (mode chaos) ne prétendent qu'à la reproductibilité de séquence/contenu |
| Source de vérité unique | La config n'a **qu'une** origine — le `.yml` chargé au démarrage. Aucune mutation runtime possible, donc aucune divergence « fichier vs état patché » à diagnostiquer |
| Progression des planifications au repos | Les planifications en ticks logiques (DLR, MO auto, `spike`, transitions) ne se figent pas quand le trafic cesse : un flush de quiescence les draine dans l'ordre de tick (§6.3) |
| Isolation | Aucune dépendance envers l'infrastructure de la passerelle (Kafka, Postgres, Redis, ClickHouse) |
| Concurrence | Un processus héberge confortablement 10–20+ SMSC virtuels pour les topologies multi-connecteurs |

### 1.3 Contraintes

- **Configuration exclusivement par fichier `.yml`** — aucune API HTTP de configuration. Le simulateur lit son `.yml` au démarrage et ne peut être reconfiguré qu'en le relançant avec un autre fichier. Cohérent avec la NFR de démarrage < 2 s : un nouveau scénario = une nouvelle instance, pas un patch à chaud.
- **Scénarios prédéfinis** — le catalogue de profils est figé dans le code (§6.1). Le `.yml` en sélectionne et en règle les paramètres exposés ; il ne définit pas de règles de réponse arbitraires.
- Langage : Go — correspond à la passerelle (§1.3 compagnon), permettant de partager le codec de PDU SMPP et les patterns de connexion.
- En mémoire par défaut : aucun magasin externe. Config chargée du `.yml`, journaux de PDU et état d'exécution en mémoire processus, bornés, non censés survivre à un redémarrage.
- Pas un composant de production : outil de test/CI uniquement, jamais déployé aux côtés de la passerelle de production.

---

## 2. Estimation

- **Échelle de test de charge** : dimensionné pour égaler ou dépasser la cible de pic de la passerelle (15 000 msg/s) par SMSC virtuel.
- **Échelle CI/fonctionnel typique** : dizaines à quelques centaines de msg/s par test (la plupart valident la correction, pas le débit brut).
- **Multi-instance** : une topologie de test réaliste exécute 3–10 SMSC virtuels simultanément, tous décrits dans un même `.yml`.
- **Enregistrement PDU** : tampon circulaire par défaut ~10 000 PDU par SMSC virtuel (`pdu_buffer_size` du `.yml`). À 15 000+ msg/s il boucle en moins d'une seconde — suffisant pour des assertions ciblées à faible volume, à augmenter pour inspecter un test de charge complet.
- **Fichiers de config** : un `.yml` par topologie de test, versionné comme fixture aux côtés des tests qu'il sert.

---

## 3. Schéma de stockage (Storage Schema)

Volontairement minimal — outil sans état entre exécutions. La configuration d'exécution est le **reflet immuable du `.yml` chargé au démarrage**.

### 3.1 Fichier de configuration (`.yml`, unique entrée de config, chargé au démarrage)

Structure déclarative. Chaque SMSC virtuel choisit un profil prédéfini et n'en règle que les paramètres exposés. Exemple annoté :

```yaml
# smsc-simulator.yml — the SINGLE source of configuration. Loaded once at startup.
# No HTTP config API exists; to change anything, edit this file and restart.

observability:
  http_port: 9000          # read-only assertion + Prometheus surface; omit the block to disable HTTP entirely

virtual_smscs:
  - name: carrier-a
    port: 2775
    bind_credentials:
      system_id: smppclient1
      password: secret
    addr_ton: 1
    addr_npi: 1
    address_range: "^33.*"
    tls:
      enabled: false        # if true and no cert supplied, a self-signed cert is auto-generated
    seed: 42                 # set => deterministic mode; omit => unseeded/chaos mode
    pdu_buffer_size: 10000   # ring buffer capacity; raise for full load-test PDU inspection
    throughput_limit_per_sec: 5000   # nullable; enforced independently of the profile

    scenario:
      profile: throttling-carrier      # MUST be one of the built-in profiles (see §6.1). No arbitrary rules.
      params:                          # only the knobs the chosen profile exposes
        throughput_cap_per_sec: 5000
        error_code: ESME_RTHROTTLED
      latency:
        distribution: fixed            # fixed | uniform | normal | spike
        params: { ms: 40 }             # spike interval, when used, is expressed in ticks (per_bind_clock)
      dlr:
        delay: { distribution: fixed, ticks: 5 }   # anchored to the origin submit_sm's per-bind tick
        outcome_weights: { delivered: 90, failed: 8, expired: 2 }
        clock: logical                 # logical | wallclock (wallclock only valid when seed is absent)
      protocol_edge_cases_enabled: false           # opt-in malformed-PDU injection

    # Scheduled MO injection. Deterministic by tick.
    mo_injection:
      mode: scheduled                  # scheduled | auto | disabled
      clock: logical                   # logical enforced when seed is set; wallclock only in chaos
      events:
        - at_tick: 100
          source_addr: "33600000001"
          dest_addr: "33700000002"
          content: "MO probe A"
      # when mode: auto, use instead:  rate_per_sec: 5, content_template: "..."

    # Scheduled connection faults, anchored to ticks.
    scheduled_disconnects:
      - at_tick: 300
        scope: all                     # all | oldest | random
        when: before_response          # before_response | after_response

    # In-test scenario transitions, anchored to ticks. Reproducible.
    scheduled_transitions:
      - at_tick: 200
        to_profile: dead-carrier
      - at_tick: 400
        to_profile: healthy
```

Notes de sémantique :

- `scenario.profile` doit appartenir au catalogue figé (§6.1) ; une valeur inconnue est une **erreur de validation au chargement** (fail-fast, le processus refuse de démarrer).
- Tous les mécanismes temporels référencent le **tick logique par bind** (`per_bind_clock`) dès qu'un `seed` est présent ; `clock: wallclock` n'est accepté que sans `seed` (mode chaos).
- `mo_injection`, `scheduled_disconnects` et `scheduled_transitions` sont les trois formes déclaratives des actions temporelles — chacune ancrée à un tick, donc reproductible.
- Le `.yml` est validé intégralement au démarrage (schéma, cohérence `seed`/`clock`, profils connus) avant d'ouvrir le moindre port SMPP.

### 3.2 État d'exécution (en mémoire, borné, par SMSC virtuel)

```
received_pdus   -- ring buffer (size = pdu_buffer_size) of recent submit_sm PDUs, queryable read-only for assertions
active_binds    -- current bind sessions (account/system_id, bind type, connected_at)
metrics         -- Prometheus counters/histograms (binds, submit_sm received/outcome, current scenario, latency)
logical_clock   -- per virtual SMSC monotonic counter of processed submit_sm; a GLOBAL observation/assertion
                   reference (exposed read-only via GET /logical-clock). NOT the deterministic timing reference
                   (its increment order across concurrent binds is non-reproducible) — see per_bind_clock
per_bind_clock  -- per (virtual SMSC, bind session) monotonic counter of submit_sm on THAT bind; the deterministic
                   timing reference for clock=logical mechanisms (DLR delay, spike, MO events, scheduled
                   disconnects, scheduled transitions). Determinism of sequence/content is guaranteed per bind;
                   global cross-bind order is not (pin bind_pool_size=1 for globally-ordered assertions)
pending_logical_schedule  -- per-bind ordered set of DLR/MO/spike/disconnect/transition events due at a future
                   tick; drained when the bind's per_bind_clock reaches the due tick, or by the quiescence flush
                   when the bind goes idle
active_scenario -- the profile currently in effect for the virtual SMSC; starts at scenario.profile and advances
                   only through scheduled_transitions (no runtime mutation path exists)
```

Aucune base persistante ; toute la config vit dans le `.yml` versionné, refourni à chaque exécution.

---

## 4. Conception de haut niveau (High-Level Design)

```
                         +----------------------------+
                         |   smsc-simulator.yml        |  <- SINGLE config input, loaded & validated at startup
                         +-------------+--------------+
                                       | (parsed once; no runtime reconfiguration)
                                       v
+---------------------------------------------------------------------------+
|                     smsc-simulator (single Go binary)                     |
|  +------------------------+   +------------------------+                  |
|  | Virtual SMSC #1         |   | Virtual SMSC #2 ...N    |  <- one SMPP    |
|  | (SMPP Server Engine)    |   | (SMPP Server Engine)    |     listener    |
|  |  - bind handling         |   |  - bind handling         |     per port   |
|  |  - Scenario Engine        |   |  - Scenario Engine        |             |
|  |  - Fault Injector          |   |  - Fault Injector          |           |
|  |  - DLR Scheduler             |   |  - DLR Scheduler             |       |
|  |  - MO Injector (scheduled)     |   |  - MO Injector (scheduled)     |   |
|  |  - Schedule Runner (tick)        |   |  - Schedule Runner (tick)        | |
|  |  - per_bind_clock                  |   |  - per_bind_clock                | |
|  |  - PDU Recorder (ring buffer)        |   |  - PDU Recorder (ring buffer)      | |
|  +------------+-------------+   +------------+-------------+              |
|               |                              |                            |
|  +------------v------------------------------v-------------+             |
|  |        Observability API (HTTP, READ-ONLY, single port)   |             |
|  |  GET binds - GET received-pdus - GET logical-clock         |             |
|  |  GET health - Prometheus /metrics    (NO config, NO mutate)|             |
|  +-------------------------------------------------------------+           |
+-----------------------------------------------------------------------------+
                     ^ SMPP binds (one per virtual SMSC port)
        +------------+--------------+
        |   Gateway under test        |  (connector-pool-svc treats each virtual SMSC as a real smsc_connector)
        +-------------------------------+
```

### 4.1 Composants

1. **Config Loader** — au démarrage, lit et valide le `.yml` (schéma, profils connus, cohérence `seed`/`clock`), puis instancie les SMSC virtuels. Toute erreur est fatale avant l'ouverture des ports (fail-fast). C'est le seul chemin de configuration ; il n'existe aucune API de mutation.
2. **SMPP Server Engine** (un par SMSC virtuel) — accepte les connexions TCP sur son port, gère bind, `enquire_link`, `submit_sm`/`submit_sm_resp`, `deliver_sm`, `unbind`, réutilisant le codec de PDU partagé avec la passerelle.
3. **Scenario Engine** — à chaque `submit_sm`, applique le comportement du profil **actif** (celui du `.yml` ou celui atteint par une transition planifiée), sélectionne un résultat pondéré, le transmet au Fault Injector. Incrémente à chaque PDU le `per_bind_clock` de la session (référence de timing déterministe) et le `logical_clock` global (observable d'assertion uniquement).
4. **Fault Injector** — applique la distribution de latence et, pour `disconnect`, coupe la connexion TCP en cours de transaction (avant/après réponse, selon le profil ou une entrée `scheduled_disconnects`).
5. **DLR Scheduler** — pour les messages « soumis » avec succès, planifie un `deliver_sm` DLR asynchrone. En mode déterministe, le délai est ancré au tick du `submit_sm` d'origine (`per_bind_clock`) ; les DLR en attente sont drainés à l'atteinte du tick dû ou par le flush de quiescence quand le bind cesse de recevoir du trafic.
6. **MO Injector** — envoie des `deliver_sm` non sollicités selon la déclaration `mo_injection` du `.yml` : événements ancrés à un tick (mode `scheduled`) ou minuteur auto-planifié (mode `auto`) — piloté par `per_bind_clock` en mode déterministe, par horloge murale uniquement en mode chaos ; soumis au flush de quiescence.
7. **Schedule Runner** — moteur de planification par bind qui draine `pending_logical_schedule` : DLR dus, MO planifiés, rafales `spike`, déconnexions planifiées et **transitions de scénario planifiées**, tous dans l'ordre de tick déterministe.
8. **PDU Recorder** — ajoute chaque PDU reçue au tampon circulaire borné, exposé en lecture seule.
9. **Observability API** — surface HTTP **strictement en lecture seule** : inspection des PDU et des binds, compteur logique global, santé, métriques Prometheus. Aucun endpoint ne crée, modifie ou supprime quoi que ce soit.

---

## 5. Conception de l'API (API Design)

La « conception d'API » du simulateur comporte deux surfaces bien séparées : le **fichier de configuration `.yml`** (entrée, en écriture par l'auteur du test, lue au démarrage) et la **surface d'observabilité HTTP** (sortie, en lecture seule, pour les assertions).

### 5.1 Surface de configuration — fichier `.yml`

Unique entrée de configuration. Structure détaillée en §3.1. Contrat :

- Chargée et validée **une seule fois au démarrage** ; immuable ensuite.
- Décrit la topologie complète : liste des SMSC virtuels, leurs ports/identifiants/TLS, leur `seed`, leur profil prédéfini paramétré, et leurs planifications (`mo_injection`, `scheduled_disconnects`, `scheduled_transitions`).
- Versionnée comme fixture de test. Reconfigurer = éditer le fichier et relancer le processus (démarrage < 2 s).
- Validation fail-fast : profil inconnu, `clock: wallclock` avec un `seed`, port en doublon, paramètre hors bornes → le processus refuse de démarrer avec un message d'erreur explicite, plutôt que de démarrer dans un état ambigu.

### 5.2 Surface d'observabilité — `http://localhost:<observability-port>/v1` (READ-ONLY)

Aucun verbe mutant (`POST`/`PATCH`/`PUT`/`DELETE`). Uniquement de la lecture, pour les assertions CI et le scraping Prometheus. Le bloc `observability` du `.yml` peut être omis pour désactiver entièrement le HTTP.

```
GET     /health                                          # liveness

# Read-only inspection (assertions)
GET     /virtual-smscs                                   # list configured virtual SMSCs + active profile
GET     /virtual-smscs/{id}                              # one virtual SMSC's current view (read-only)
GET     /virtual-smscs/{id}/received-pdus?sourceAddr=&destAddr=&since=   # paginated PDU log
GET     /virtual-smscs/{id}/binds                        # current bind sessions
GET     /virtual-smscs/{id}/logical-clock                # current global tick count (observation only)

# Observability
GET     /metrics                                         # Prometheus exposition format
```

Note : `received-pdus` est en lecture seule. L'isolation entre exécutions de test est obtenue en **relançant le simulateur** avec le `.yml` de la fixture (démarrage < 2 s), ce qui repart d'un tampon vide et d'un `per_bind_clock` à zéro — plus déterministe qu'un reset à chaud.

### 5.3 Interface SMPP (par port de SMSC virtuel)

Comportement serveur SMPP v3.4 standard (v5.0 optionnel) — `bind_*`, `submit_sm`, `deliver_sm` (MO + DLR), `enquire_link`, `unbind` — surface identique à ce que `connector-pool-svc` attend d'un vrai SMSC (§5.1 compagnon), plus les modes opt-in de PDU malformées activés par `protocol_edge_cases_enabled` dans le profil.

---

## 6. Conception détaillée (Detailed Design)

### 6.1 Moteur de scénario & profils prédéfinis

Le catalogue de profils est **figé dans le code**. Le `.yml` sélectionne un profil par son nom et en règle les paramètres exposés — il ne peut pas définir de règles de réponse arbitraires. Les règles internes d'un profil sont évaluées comme les règles de routage de la passerelle (correspondance source/dest/contenu, première correspondance gagnante, repli par profil).

| Profil | Comportement | Paramètres exposés (`.yml`) | Ce qu'il exerce dans la passerelle |
|---|---|---|---|
| `healthy` | 100 % succès, latence fixe basse | `latency` | Chemin nominal/référence |
| `flaky-carrier` | ~80 % succès, ~20 % erreurs/timeouts, déconnexions périodiques | `success_rate`, `error_mix`, `disconnect_interval_ticks`, `latency` | Disjoncteur (§6.15), retry/dead-letter (§6.7) |
| `throttling-carrier` | `ESME_RTHROTTLED` au-delà d'un débit | `throughput_cap_per_sec`, `error_code`, `latency` | Throttling adaptatif (§6.4) |
| `dead-carrier` | Refuse les binds, ou fait timeout sur chaque `submit_sm` | `mode` (reject_bind \| timeout_all), `latency` | Disjoncteur `open` (§6.15), repli de routage (§6.1), auto-reconnexion (§6.13) |
| `slow-carrier` | Latence haute bornée (2–4 s), aucune erreur | `latency` (borne basse/haute) | `response_timeout_ms`/`window_size`, durée de span (§6.11 compagnon) |
| `throughput-capped` | Applique son propre plafond, throttle au-delà | `throughput_cap_per_sec`, `latency` | Boucle de throttling adaptatif bout-en-bout |

**Transitions en cours de test.** Le pattern « healthy → dead-carrier → healthy » (ouvrir puis refermer le disjoncteur, vérifier la reprise) se déclare via `scheduled_transitions` dans le `.yml`, ancré à des ticks logiques. Le profil actif d'un SMSC virtuel n'avance **que** par ces transitions planifiées — il n'existe aucun chemin de mutation runtime. Deux exécutions de la même fixture produisent donc exactement la même séquence de bascules.

### 6.2 Mécanique d'injection de panne

- **Latence** : `fixed`, `uniform`, `normal` (borné à non-négatif), `spike` (référence basse avec rafales périodiques). En mode déterministe, l'intervalle `spike` est exprimé en ticks (`per_bind_clock`) ; en mode chaos, en durée réelle.
- **Timeouts** : le simulateur retient `submit_sm_resp` au-delà du `response_timeout_ms` attendu — le timeout propre de la passerelle se déclenche naturellement.
- **Déconnexions** : deux origines, toutes deux déclaratives — (a) intrinsèques au profil (ex. `flaky-carrier.disconnect_interval_ticks`), (b) explicites via `scheduled_disconnects` (à un tick précis, `scope` et `when` configurables). Aucune ne dépend d'un appel externe.

### 6.3 Déterminisme & modes chaos

Le déterminisme du mode à graine s'appuie sur un compteur logique de PDU, jamais sur l'horloge murale (une panne périodique pilotée par l'horloge murale ne peut pas être reproductible d'une exécution à l'autre : gigue CI, pauses GC, ordonnancement réseau). Aucune entrée temporelle non-déterministe n'existe (pas de hot-swap ni d'inject-MO déclenché à un instant arbitraire) ; tout événement temporel provient du `.yml` et est ancré à un tick.

- **Horloge par bind (`per_bind_clock`)** : la référence de timing déterministe est un compteur **par session de bind**. Un DLR est planifié « M ticks après le `submit_sm` d'origine, sur le bind d'origine » ; `spike`, MO planifiés, déconnexions et transitions sur le tick du bind concerné. Au sein d'un bind (flux TCP ordonné), séquence et contenu sont reproductibles.
- **Portée de la garantie** : la reproductibilité est **par bind**, pas globalement — la passerelle scale un connecteur avec `bind_pool_size` binds parallèles (§6.8 compagnon), et l'ordre d'entrelacement entre binds concurrents dépend de l'ordonnancement, non reproductible. Une assertion à ordre global doit épingler `bind_pool_size = 1`. La plupart des tests fonctionnels/CI (comportement précis à faible volume) sont dans ce cas ; les tests de charge multi-bind conservent le déterminisme par bind et l'agrégation statistique.
- **Compteur global (`logical_clock`)** : exposé comme observable d'assertion en lecture seule (`GET /logical-clock`), jamais comme référence de planification.
- **Flush de quiescence** : une planification en ticks n'avance qu'avec le trafic entrant. Dans le cas CI majoritaire (soumettre un lot puis attendre les DLR / une transition), le trafic cesse et le compteur se figerait. Chaque bind tient un `pending_logical_schedule` drainé (a) à l'atteinte du tick en fonctionnement normal, ou (b) par un flush après `quiescence_flush_ms` (défaut 250 ms) sans nouveau `submit_sm`, dans l'ordre de tick déterministe. Le déterminisme de séquence/contenu est préservé ; seule la latence murale absolue d'un événement au repos n'est pas garantie — sans conséquence pour une assertion de résultat.
- **Mode chaos (sans graine)** : `clock: wallclock` autorisé et par défaut pour les mécanismes périodiques ; aucune prétention de reproductibilité.

### 6.4 Intégration CI/CD

- **Image Docker** : binaire unique, base minimale, **config via un seul `.yml` monté** (ou passé par variable d'environnement pointant vers le fichier). Aucun port de configuration à exposer, seulement l'éventuel port d'observabilité.
- **docker-compose** : le simulateur câblé comme une ou plusieurs entrées `smsc_connectors` de la passerelle pointant vers ses ports — indistinguable d'une vraie connexion opérateur. Le `.yml` du simulateur est monté comme fixture.
- **Isolation entre tests** : relancer le conteneur avec le `.yml` de la fixture (démarrage < 2 s) plutôt que de réinitialiser à chaud — repart d'un état propre et d'un `per_bind_clock` à zéro.
- **Test de charge** : associer un profil `throughput-capped`/`healthy` à fort débit (paramétré dans le `.yml`) avec l'outillage de génération de charge de la passerelle pour valider débit/latence bout-en-bout. Augmenter `pdu_buffer_size` si l'inspection de PDU sur toute la durée importe.
- **Test de résilience** : décrire dans le `.yml` un profil `dead-carrier`/`flaky-carrier` (avec, au besoin, des `scheduled_transitions` pour scénariser panne puis reprise) et asserter contre l'API Admin de la passerelle (statut connecteur, disjoncteur) et les endpoints read-only `/received-pdus`/`/binds` du simulateur pour vérifier que les comportements de résilience documentés (§6.13/§6.15/§6.1 compagnon) se produisent réellement.

---

## 7. Évaluation (Evaluation)

| Décision | Compromis |
|---|---|
| Simulateur sur mesure vs simulateur SMPP open-source | Les fonctionnalités de résilience spécifiques de cette passerelle (disjoncteur, auto-reconnexion, throttling adaptatif) valent un outil sur mesure avec profils nommés, config scriptable et Go natif (outillage partagé), face à l'adaptation d'un outil générique. |
| Config `.yml` déclarative vs API de config runtime | Le fichier unique donne une **source de vérité unique** (pas de divergence « fichier vs état patché » à diagnostiquer) et **supprime toute entrée temporelle non-déterministe** (un hot-swap / inject-MO HTTP dépendrait de l'instant d'appel, en horloge murale). Le coût — pas de mutation à chaud — est neutralisé par la NFR de démarrage < 2 s : un nouveau scénario est une nouvelle instance. Le seul pattern concerné, la transition en cours de test, est couvert de façon *reproductible* via `scheduled_transitions`. |
| Scénarios prédéfinis (catalogue figé) vs règles arbitraires dans le `.yml` | Un catalogue figé de profils paramétrables garde les fixtures lisibles et le comportement borné/testé ; on renonce à des scénarios totalement sur mesure, jugés inutiles pour exercer les mécanismes de résilience ciblés et sources de fixtures illisibles. |
| Surface HTTP en lecture seule vs aucune surface HTTP | Le read-only ne viole pas la contrainte « aucune API de config » et sert les assertions d'**état vivant** (disjoncteur ouvert en < N s ? binds actifs ?) et le scraping Prometheus, bien plus ergonomiques en HTTP qu'en fichier ; le coût est un petit serveur HTTP, désactivable en omettant le bloc `observability`. |
| État uniquement en mémoire vs stockage persistant | Correspond à la nature éphémère des exécutions CI et garde l'outil sans dépendance ; les définitions de scénario sont refournies à chaque exécution (le `.yml` versionné). |
| Un processus hébergeant plusieurs SMSC virtuels | Empreinte plus légère, orchestration plus simple ; un crash fait tomber tous les SMSC virtuels du processus — non-problème pour un outil de test. |
| Mode déterministe à graine comme principal, chaos en secondaire | La reproductibilité a plus de valeur pour le cas majoritaire (vérifier un comportement précis en CI) ; le chaos reste pour le test exploratoire sans rendre la plupart des tests instables. |
| Horloge de timing **par bind** (`per_bind_clock`) plutôt que le compteur global | Le pool de binds multiple de la passerelle rend l'ordre global entre binds non reproductible ; le compteur par bind restaure un déterminisme réel, au prix d'une garantie scopée « par bind » (`bind_pool_size = 1` requis pour un ordre global). |
| Flush de quiescence pour les planifications logiques | Sans lui, un test à faible volume se fige sur un compteur gelé ; le flush draine les planifications en attente dans l'ordre de tick, au prix d'abandonner la garantie de latence murale absolue d'un événement au repos. |

**Ce qu'on revisiterait :** un mode de rechargement du `.yml` sur signal (`SIGHUP`) si des topologies très lourdes rendent le redémarrage complet coûteux — en acceptant qu'il n'introduise aucune mutation dépendante de l'horloge murale ; sharder la gestion de connexion d'un SMSC virtuel sur plusieurs pools de goroutines, ou des instances dédiées par shard de test de charge, si le débit par instance devient un goulot ; un mode de fuzzing programmatique si la couverture de cas limites protocolaires devient prioritaire ; un mode d'échantillonnage du PDU recorder si `pdu_buffer_size` étendu pousse la mémoire au-delà de la cible.

---

## 8. Composant distinctif (Distinctive Component)

**Moteur d'injection de panne orienté résilience, avec un déterminisme entièrement déclaratif et des garanties honnêtes.**

Un simulateur SMPP générique répond aux binds et fait écho aux `submit_sm_resp`. Celui-ci est construit autour de l'affirmation que la passerelle fait sur elle-même : résilience face aux pannes opérateur via disjoncteur, auto-reconnexion, throttling adaptatif et repli de routage. Les profils prédéfinis (`dead-carrier`, `flaky-carrier`, `throttling-carrier`, `slow-carrier`) déclenchent chacun de ces mécanismes à la demande et en combinaison, entièrement décrits dans un `.yml` versionné, avec une surface d'observabilité en lecture seule qui permet à un test de vérifier non seulement « la passerelle a continué » mais « le disjoncteur s'est ouvert dans les N secondes et le trafic s'est réorienté ».

Le second trait distinctif est une **séparation honnête entre déterminisme de séquence/contenu et déterminisme de timing**, portée par une **configuration purement déclarative**. Plutôt que de promettre une reproductibilité que les mécanismes pilotés par l'horloge murale ne peuvent tenir, le simulateur : (1) n'accepte de configuration que depuis un `.yml` chargé au démarrage, sans entrée temporelle externe ; (2) ancre le déterminisme **par session de bind** (`per_bind_clock`) — parce que le pool de binds multiple de la passerelle rend l'ordre global non reproductible ; (3) exprime *tout* événement temporel (DLR, MO, déconnexions, transitions de scénario) comme une planification sur tick logique déclarée dans le fichier ; et (4) draine ces planifications par un **flush de quiescence** quand le trafic cesse. Une assertion CI construite sur ce simulateur repose ainsi sur une garantie réellement vraie, y compris sous la topologie multi-bind, en période de silence, et à travers des transitions de scénario en cours de test — le tout reproductible bit pour bit à partir d'un simple fichier de fixture.

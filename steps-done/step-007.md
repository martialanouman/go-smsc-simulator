# Step 007 — S7 · Cas limites protocolaires + fuzz + packaging CI/CD + charge

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §11.
> **Statut : ⏳ À FAIRE.** Jalon de **durcissement et de sortie**.

## Objectif

Durcir le parsing (cas limites + fuzz), packager pour la CI (Docker/K8s), et valider les **NFR** de débit et de démarrage.

## Dépend de

S6.

## Nouvelles dépendances

Aucune dans `go.mod` (générateur de charge SMPP externe ou harnais de la passerelle, hors module).

## Découpage en tâches

### T1 — Injection de cas limites protocolaires (opt-in)
- `protocol_edge_cases_enabled` (déjà dans `ScenarioConfig`) active des PDU malformées : longueur invalide, `command_id` invalide, numéros de séquence hors ordre.
- **Désactivé par défaut** → parsing strict sinon.
- Chaque type de malformation est déterministe (ancré ticks/PRNG graîné) et documenté.

### T2 — Fuzz du décodeur PDU (`go test -fuzz`)
- Cible : le **décodeur** de `internal/smpp` (surface d'entrée non fiable).
- Objectifs : **aucune panique**, **aucune allocation non bornée** sur entrée hostile (ex. `command_length` géant).
- Corpus de seed + durée bornée en CI (`-fuzztime`).

### T3 — Packaging
- `Dockerfile` : binaire **statique**, base minimale (`scratch`/`distroless`), `.yml` monté en volume. (La cible `make docker` échoue déjà explicitement tant que ce fichier n'existe pas.)
- `docker-compose.yml` d'exemple : câble le simulateur comme `smsc_connectors` de la passerelle.
- `deploy/` : modèle de **Job Kubernetes**.
- Vérifier le **démarrage à froid < 2 s** de l'image.

### T4 — Charge & NFR
- Profils `throughput-capped` / `healthy` à fort débit.
- Valider : **≥ 15 000 msg/s par SMSC virtuel**, **démarrage à froid < 2 s**, **< 50 Mo** de base par SMSC virtuel au repos.
- Vérifier que le **déterminisme par bind tient sous charge multi-bind** (agrégation statistique conservée).

## Hors périmètre

Rien de nouveau fonctionnellement — c'est le jalon de durcissement/sortie.

## Critères d'acceptation (tests)

- [ ] Le profil de cas limites n'injecte des PDU malformées **que** si `protocol_edge_cases_enabled` ; sinon parsing strict.
- [ ] `go test -fuzz` sur le décodeur : aucune panique sur le corpus généré (durée bornée en CI).
- [ ] L'image Docker démarre à froid **< 2 s** et sert des binds ; `docker-compose up` branche le simulateur comme connecteur **indistinguable** d'un vrai SMSC.
- [ ] **Charge** : ≥ 15 000 msg/s soutenu par SMSC virtuel, latence servie conforme ; déterminisme **par bind** vérifié pendant un run multi-bind.

## Risques & points d'attention

- **Fuzz + allocations non bornées** : borner explicitement `command_length` lu avant toute allocation (`make([]byte, n)` sur un `n` attaquant est le bug classique). Ajouter cette borne dans le décodeur dès qu'un cas fuzz la révèle.
- **Base image statique** : CGO désactivé (`CGO_ENABLED=0`) ; le binaire ne dépend d'aucune lib système. Vérifier que TLS (S6) fonctionne toujours sur `scratch` (certs racine non requis côté serveur).
- **NFR de charge** : le harnais de charge est **hors `go.mod`** ; documenter comment le lancer. Le test de débit ne tourne pas dans la CI unitaire (trop lourd) — cible séparée / manuelle.
- **Déterminisme sous charge** : la reproductibilité est garantie **par bind**, pas globalement — l'assertion de charge doit agréger statistiquement, pas exiger un ordre inter-bind.
- Intégration `docker-compose` avec la passerelle : dépend du schéma de connecteur de la passerelle (`specification-technique-passerelle-sms.md`).

## Definition of Done

§0.4 du plan. Mettre à jour `CLAUDE.md` (commandes docker/charge) et vérifier que `make check` reste vert avec les nouveaux tests fuzz (temps borné).

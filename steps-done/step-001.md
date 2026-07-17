# Step 001 — S1 · Config `.yml` déclarative complète + validation fail-fast

> Plan de référence : `docs/plan-execution-simulateur-smsc.md` §5.
> **Statut : ✅ LIVRÉ** (branche `s1-config-declarative-validation` — commits `b76fb9c`, `63d7722`, `905fd15`, `55a08fd`).
> Documente le plan et sert de checklist de non-régression.

## Objectif

Parser et **valider intégralement** le schéma `.yml` de la spec §3.1, matérialiser les SMSC virtuels en mémoire immuable — **sans** rien servir.

## Dépend de

S0.

## Livrables (état réel du dépôt)

| Livrable | Fichier(s) | Fait |
|---|---|---|
| Schéma complet | `internal/config/schema.go` (`VirtualSMSCConfig`, `ScenarioConfig`, `ScenarioParams`, `LatencyConfig`, `DLRConfig`, `MOInjectionConfig`, `ScheduledDisconnect/Transition`) | ✅ |
| Énumérations | `internal/config/enums.go` (`Profile`, `LatencyDistribution`, `Clock`, `DisconnectScope/When`, `MOMode`, `DeadCarrierMode`, `SMPPErrorCode`) | ✅ |
| Catalogue de validation des profils | `internal/config/profiles.go` (`profileCatalogue` : knobs exposés + bornes de latence par profil) | ✅ |
| Validation fail-fast | `internal/config/validate.go` (+ `validate_test.go`) | ✅ |
| Décodage strict | `config.go` : `yaml.Decoder` avec `KnownFields(true)` (clé inconnue = erreur) | ✅ |
| Fixtures valides | `examples/*.yml` (une par profil) | ✅ |
| Fixtures invalides | `internal/config/testdata/*.yml` (une par règle) | ✅ |

## Règles de validation implémentées (spec §3.1)

- `profile` inconnu → erreur.
- knob `params` non exposé par le profil actif → `ErrParamNotExposed`.
- `clock: wallclock` avec `seed` défini → erreur (cohérence seed/clock).
- port en doublon → erreur.
- paramètre hors bornes (latence hors `[min,max]` du profil, `success_rate` ∉ [0,1], poids DLR de somme nulle…) → erreur.
- `to_profile` d'une transition inconnu → erreur.
- clé YAML inconnue (typo) → erreur (`KnownFields`).
- Chaque erreur **nomme le champ fautif**.

## Modèle immuable

`Config` et son arbre n'exposent **aucun setter** ; `Load` renvoie `(nil, err)` sur échec (rien de mi-validé ne fuit). Champs optionnels en pointeur pour distinguer *absent* de *zéro*.

## Hors périmètre

Aucun comportement runtime : pas de listener, pas de PDU. Uniquement charger, valider, matérialiser.

## Critères d'acceptation (checklist)

- [x] Table-driven : chaque fixture valide charge ; chaque fixture invalide échoue avec l'erreur attendue.
- [x] Invariant (b) : validation complète **avant** toute ouverture de port.
- [x] Test qui itère `examples/*.yml` : tout y charge sans erreur.
- [x] Modèle immuable (aucune API de mutation).

## Notes pour les jalons suivants

- `profiles.go` porte le catalogue de **validation** (knobs/bornes). S3 ajoutera un catalogue **comportemental** distinct dans `internal/scenario` (poids, application de latence) — ne pas fusionner les deux (cf. commentaire d'en-tête de `profiles.go`).
- `DLRDelay` supporte `fixed` (ticks) ; `uniform` (min/max ticks) est déjà modélisé mais réservé — à activer à S4.

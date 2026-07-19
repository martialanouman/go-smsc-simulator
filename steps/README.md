# Plans d'implémentation par jalon

Un fichier `step-00N.md` par jalon du plan d'exécution (`docs/plan-execution-simulateur-smsc.md`, jalons `S0`–`S7`). Chaque fichier porte : objectif, dépendances, découpage en tâches (PR fines), hors périmètre, critères d'acceptation (tests) et risques.

Correspondance : `step-000.md` ↔ `S0`, `step-001.md` ↔ `S1`, … `step-007.md` ↔ `S7`.

## Convention de cycle de vie

- **`steps/`** — les jalons **restant à exécuter**. Le contenu du dossier reflète à tout moment le reste à faire.
- **`steps-done/`** — l'**archive** des jalons livrés.

Dès qu'un jalon est **exécuté** — critères d'acceptation verts, `make check` vert, PR mergée — déplacer son `step-00N.md` de `steps/` vers `steps-done/` :

```bash
git mv steps/step-00N.md steps-done/
```

À ne pas supprimer : le plan archivé sert de checklist de non-régression pour les jalons suivants (qui s'appuient dessus).

## État actuel

| Fichier | Jalon | Statut |
|---|---|---|
| `steps-done/step-000.md` | S0 · Fondations & outillage | ✅ livré |
| `steps-done/step-001.md` | S1 · Config déclarative + validation | ✅ livré |
| `steps-done/step-002.md` | S2 · Squelette SMPP vertical | ✅ livré |
| `steps-done/step-003.md` | S3 · Scénarios + pannes + déterminisme | ✅ livré |
| `steps-done/step-004.md` | S4 · DLR + flush de quiescence | ✅ livré |
| `steps-done/step-005.md` | S5 · MO + déconnexions + transitions | ✅ livré |
| `steps-done/step-006.md` | S6 · Multi-SMSC + TLS + métriques | ✅ livré |
| `steps-done/step-007.md` | S7 · Cas limites + fuzz + packaging + charge | ✅ livré |

# Architecture — egress-guardian-operator

## Problème résolu

Les workloads Kubernetes établissent silencieusement des connexions sortantes vers des destinations externes (SaaS, APIs tierces, stockage cloud). Sans gouvernance, il est impossible de savoir ce qui sort réellement, ni de produire une politique egress correcte sans couper du trafic légitime.

`egress-guardian-operator` résout ce problème en trois phases : **observer → suggérer → enforcer**.

---

## CRDs

### EgressProfile

Intention de gouvernance pour un workload donné. Contient :
- `spec.targetRef` : le Deployment/StatefulSet/DaemonSet cible
- `spec.mode` : `Observe` | `Suggest` | `Enforce` — **seul driver de la machine à états**
- `spec.baselineWindow` : fenêtre glissante de rétention (ex. `24h`)
- `spec.policy` : options DNS, kube-system, nom de la CNP
- `spec.gitOps` : export YAML vers un path local (GitOps)

Le `status` ne contient que des compteurs, refs et conditions. Les données volumineuses vivent dans un ConfigMap owned.

### EgressPolicyProposal

Artefact reviewable créé automatiquement par l'opérateur en mode `Suggest`/`Enforce`. Contient :
- `spec.profileRef` : référence vers l'EgressProfile parent
- `spec.approval` : porte d'approbation humaine (`approved`, `approvedBy`, `approvedAt`)
- `status.phase` : `Draft` | `PendingApproval` | `Approved` | `Applied` | `Failed`
- `status.confidenceScore` : 0-100
- `status.excludedDestinations` : destinations exclues de la policy avec la raison

---

## Stockage (ConfigMap per-profile)

Chaque EgressProfile possède un ConfigMap `egress-guardian-<name>` (owned, GC automatique) :

```
data:
  baseline.json   # inventaire complet des destinations + scores (max 500)
  policy.yaml     # dernier CiliumNetworkPolicy généré (non appliqué si pas approuvé)
```

Le status ne stocke jamais de YAML ni de listes longues — il reste sous la limite etcd de ~1,5 Mio.

---

## Machine à états (spec.mode)

```
mode=Observe  →  accumule les flux. Aucune Proposal créée. Rien appliqué.
mode=Suggest  →  accumule + génère une Proposal (PendingApproval). Rien appliqué.
mode=Enforce  →  accumule + génère une Proposal.
                 Si approval.approved=true → applique la CNP (default-deny egress).
                 Sinon → AwaitingApproval.
```

L'approbation est une **porte**, pas un mode parallèle. Elle ne change pas le mode.

---

## Intégration Hubble (streaming)

Hubble Relay expose un stream gRPC continu (`/observer.Observer/GetFlows`). Ce n'est **pas** un store historique — il est impossible de demander « les 24 dernières heures ».

L'opérateur consomme ce stream via un `Accumulator` (goroutine background, `mgr.Add`). La baseline est construite par **purge glissante** côté accumulateur : les entrées dont `lastSeen` dépasse `baselineWindow` sont retirées.

Les réconciliateurs lisent l'état persisté dans le ConfigMap. Ils ne touchent jamais au stream.

```
FlowSource (interface)
  ├── HubbleGRPCFlowSource   — connexion Hubble Relay, proto minimal sans cilium/cilium
  └── MockFlowSource         — flux scriptés pour tests/démo

Accumulator (mgr.Runnable)
  ├── ingest(Flow)            — agrège par workload+dest+port+proto
  ├── purge()                 — retire les entrées trop anciennes
  └── flushSnapshots()        — écrit baseline.json dans le ConfigMap
```

---

## Génération CiliumNetworkPolicy

La CNP est construite via `unstructured.Unstructured` (GVK `cilium.io/v2`), sans dépendance `github.com/cilium/cilium`.

Règles de sécurité appliquées automatiquement :
- Jamais `matchPattern: "*"` pour l'egress applicatif (autorisé uniquement dans la règle DNS)
- `*.com`, `*.net`, `*.org` → **High**, toujours exclu
- `*.amazonaws.com`, `*.cloudfront.net` → **Medium**, exclu de l'auto-enforce
- IP publique directe sans FQDN → **High**, toujours exclu
- Les exclusions sont listées dans `status.excludedDestinations` pour transparence

---

## Scoring (0-100, advisory)

Le score oriente le risque ; seules les règles dures bloquent l'enforcement.

| Condition                          | Delta |
|------------------------------------|-------|
| FQDN stable (≥3 snapshots)         | +25   |
| FlowCount élevé (≥10)              | +15   |
| Port standard (443, 80, 5432…)     | +10   |
| Pas de wildcard                    | +10   |
| Vu une seule fois                  | -15   |
| IP publique directe                | -30   |
| Wildcard large                     | -30   |

`riskLevel` : Low ≥ 80 ; Medium 50–79 ; High < 50 **ou** IP directe **ou** wildcard large.

---

## Default-deny egress — effet de l'apply

Dès qu'une `CiliumNetworkPolicy` sélectionne un endpoint, Cilium bascule ce workload en **default-deny egress** : tout flux non listé dans l'allowlist est droppé, y compris DNS si `allowDNS=false`.

L'opérateur émet un `Event Warning` de type `DefaultDenyEgress` lors de chaque apply pour rendre cet effet explicitement visible dans `kubectl describe`.

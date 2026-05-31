# egress-guardian-operator

> **Kubernetes operator for egress governance** — observes workload network flows via Cilium/Hubble, builds a FQDN baseline, generates `CiliumNetworkPolicy` allowlists and enforces them through a safe *observe → suggest → enforce* workflow.

---

## Table of Contents

- [Why this operator](#why-this-operator)
- [How it works](#how-it-works)
- [Architecture](#architecture)
- [CRDs](#crds)
- [Quick start](#quick-start)
- [Installation](#installation)
- [Usage — step by step](#usage--step-by-step)
- [Verification](#verification)
- [Configuration reference](#configuration-reference)
- [Helm chart](#helm-chart)
- [Risk scoring](#risk-scoring)
- [Security rules](#security-rules)
- [Limitations](#limitations)
- [Development](#development)

---

## Why this operator

In Kubernetes, workloads silently establish outbound connections to external services (SaaS APIs, cloud storage, third-party auth providers…). Without governance:

- You don't know what leaves your cluster
- Writing a correct egress policy without breaking production traffic is risky
- Applying a `CiliumNetworkPolicy` enforces **default-deny egress** — every unlisted destination is dropped, including DNS

`egress-guardian-operator` solves this by **watching first, suggesting a safe policy, and only enforcing after human approval**.

---

## How it works

```
                   ┌─────────────────────────────────┐
                   │        Hubble Relay (gRPC)       │
                   │   or MockFlowSource (local dev)  │
                   └────────────────┬────────────────┘
                                    │ streaming flows
                                    ▼
                         ┌──────────────────┐
                         │   Accumulator    │  ◄── background goroutine
                         │  (sliding window │       registered as
                         │   purge + state) │       mgr.Runnable
                         └────────┬─────────┘
                                  │ snapshot (JSON)
                                  ▼
                         ┌──────────────────┐
                         │   ConfigMap      │  baseline.json + policy.yaml
                         │  egress-guardian │  owned by EgressProfile (GC)
                         │  -<profile-name> │
                         └────────┬─────────┘
                                  │
              ┌───────────────────┼───────────────────┐
              ▼                                       ▼
   ┌────────────────────┐               ┌──────────────────────────┐
   │  EgressProfile     │               │  EgressPolicyProposal    │
   │  Reconciler        │  creates ───► │  Reconciler              │
   │                    │               │                          │
   │  • phase/status    │               │  • scores destinations   │
   │  • registers wkld  │               │  • generates CNP YAML    │
   │  • ensures ConfigMap              │  • applies if approved   │
   └────────────────────┘               └──────────────────────────┘
                                                    │
                                                    ▼
                                        ┌──────────────────────┐
                                        │  CiliumNetworkPolicy │
                                        │  (unstructured,      │
                                        │   no cilium/cilium   │
                                        │   dep)               │
                                        └──────────────────────┘
```

### The three modes

| Mode | What happens |
|------|-------------|
| `Observe` | Accumulates flows. No proposal created. Nothing applied. |
| `Suggest` | Accumulates + generates an `EgressPolicyProposal` (phase: `PendingApproval`). Nothing applied. |
| `Enforce` | Accumulates + generates proposal. Applies the `CiliumNetworkPolicy` **only** if `spec.approval.approved: true`. Otherwise stays `AwaitingApproval`. |

**Approval is a gate, not a mode.** Setting `approved: true` without switching to `Enforce` does nothing.

### What the accumulator does

The Hubble stream is a **continuous ring buffer** — it is *not* a queryable historical store. The accumulator:

1. Consumes the flow stream in a background goroutine (never inside `Reconcile`)
2. Aggregates destinations per workload: `(FQDN|IP, port, proto)` → `(firstSeen, lastSeen, flowCount, bytes, snapshots)`
3. Applies a **sliding window purge** — entries not seen within `baselineWindow` are removed
4. Writes a snapshot to the ConfigMap every `snapshotInterval` (default 30s)

Reconcilers read the snapshot — they never touch the stream.

---

## Architecture

```
egress-guardian-operator/
├── api/v1alpha1/
│   ├── egressprofile_types.go          # EgressProfile CRD types
│   └── egresspolicyproposal_types.go   # EgressPolicyProposal CRD types
├── internal/
│   ├── controller/
│   │   ├── egressprofile_controller.go          # manages ConfigMap, Proposal, status
│   │   └── egresspolicyproposal_controller.go   # scores, generates CNP, enforces
│   ├── observer/
│   │   ├── flowsource.go      # FlowSource interface
│   │   ├── flow.go            # internal Flow model
│   │   ├── mock.go            # MockFlowSource (no Cilium required)
│   │   ├── hubble_grpc.go     # HubbleGRPCFlowSource (raw proto, no cilium/cilium dep)
│   │   ├── accumulator.go     # stream consumer, aggregator, sliding purge, snapshots
│   │   └── workload.go        # Pod → ReplicaSet → Deployment owner-ref resolution
│   ├── store/
│   │   └── configmap_store.go # baseline.json + policy.yaml persistence
│   ├── baseline/
│   │   ├── builder.go         # scores all destinations, sorts by risk
│   │   ├── scorer.go          # 0-100 confidence score per destination
│   │   └── drift.go           # detects new/removed destinations between snapshots
│   ├── cilium/
│   │   ├── policy_generator.go  # builds CiliumNetworkPolicy (unstructured)
│   │   ├── wildcard.go          # wildcard risk evaluator
│   │   └── types.go             # unstructured CNP helpers
│   └── gitops/
│       └── exporter.go          # writes policy YAML to local path (GitOps export)
├── charts/egress-guardian-operator/    # Helm chart
├── config/{crd,rbac,manager,samples}   # Kustomize manifests
└── docs/{architecture,demo,limitations}.md
```

---

## CRDs

### EgressProfile — governance intent

```yaml
apiVersion: egress.platform.io/v1alpha1
kind: EgressProfile
metadata:
  name: payment-api
  namespace: payment
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: payment-api
  mode: Observe             # Observe | Suggest | Enforce  ← single state driver
  baselineWindow: 24h       # sliding retention window
  policy:
    allowDNS: true          # add kube-dns egress rule automatically
    allowKubeSystem: true
    generatedPolicyName: payment-api-egress-allowlist
  gitOps:
    enabled: true
    outputPath: ./generated/policies
status:
  phase: Observing          # Observing | Proposed | AwaitingApproval | Enforced | Failed
  observedDestinationsCount: 4
  truncated: false
  lastObservationTime: "2026-05-31T18:45:58Z"
  baselineConfigMapRef: egress-guardian-payment-api
  proposalRef: payment-api-proposal
  riskSummary: { low: 3, medium: 0, high: 1 }
  message: "1 destination(s) High exclue(s) de la proposition."
```

### EgressPolicyProposal — reviewable artifact

```yaml
apiVersion: egress.platform.io/v1alpha1
kind: EgressPolicyProposal
metadata:
  name: payment-api-proposal
  namespace: payment
spec:
  profileRef:
    name: payment-api
  generatedPolicy:
    type: CiliumNetworkPolicy
    name: payment-api-egress-allowlist
  approval:
    approved: false         # set to true to unlock enforcement
    approvedBy: ""          # audit trail
    approvedAt: null
status:
  phase: PendingApproval    # Draft | PendingApproval | Approved | Applied | Failed
  confidenceScore: 92       # 0-100
  riskLevel: Medium         # Low | Medium | High
  policyConfigMapRef: egress-guardian-payment-api
  excludedDestinations:
    - dest: "8.8.8.8:443"
      risk: High
      reason: "IP publique directe sans FQDN observé"
```

### ConfigMap — bulk data storage

Each profile owns a ConfigMap `egress-guardian-<name>` with:

```
data:
  baseline.json   # full destination inventory with scores (capped at 500)
  policy.yaml     # last generated CiliumNetworkPolicy YAML (not yet applied)
```

Status never stores YAML or long lists — it stays well under the etcd ~1.5 MiB limit.

---

## Quick start

### With mock flow source (no Cilium required)

```bash
# 1. Install CRDs
kubectl apply -f https://raw.githubusercontent.com/ihsenalaya/egress-guardian-operator/main/config/crd/bases/egress.platform.io_egressprofiles.yaml
kubectl apply -f https://raw.githubusercontent.com/ihsenalaya/egress-guardian-operator/main/config/crd/bases/egress.platform.io_egresspolicyproposals.yaml

# 2. Run locally
EGRESS_GUARDIAN_FLOW_SOURCE=mock \
EGRESS_GUARDIAN_SNAPSHOT_INTERVAL=10s \
go run ./cmd/main.go --leader-elect=false

# 3. Create a profile
kubectl apply -f config/samples/egressprofile_observe.yaml
```

### With kind + Cilium (full test)

```bash
# Create a kind cluster without default CNI
cat <<EOF | kind create cluster --name egress-demo --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
networking:
  disableDefaultCNI: true
  kubeProxyMode: none
EOF

# Install Cilium with Hubble
helm repo add cilium https://helm.cilium.io/
helm install cilium cilium/cilium \
  --namespace kube-system \
  --set kubeProxyReplacement=true \
  --set hubble.enabled=true \
  --set hubble.relay.enabled=true \
  --set ipam.mode=kubernetes \
  --set k8sServiceHost=egress-demo-control-plane \
  --set k8sServicePort=6443

kubectl -n kube-system rollout status ds/cilium
kubectl -n kube-system rollout status deploy/hubble-relay
```

---

## Installation

### Via Helm (recommended)

```bash
helm install egress-guardian \
  oci://ghcr.io/ihsenalaya/charts/egress-guardian-operator \
  --namespace egress-guardian-system \
  --create-namespace \
  --set flowSource=mock          # or "hubble" if Cilium is running
```

Or from the cloned repo:

```bash
helm install egress-guardian charts/egress-guardian-operator \
  --namespace egress-guardian-system \
  --create-namespace \
  --set image.repository=ghcr.io/ihsenalaya/egress-guardian-operator \
  --set image.tag=0.1.0
```

### Via Kustomize

```bash
make install   # installs CRDs
make deploy IMG=ghcr.io/ihsenalaya/egress-guardian-operator:0.1.0
```

### Via Docker

```bash
docker pull ghcr.io/ihsenalaya/egress-guardian-operator:0.1.0
```

---

## Usage — step by step

### Step 1 — Observe

Create a profile pointing to your workload:

```yaml
apiVersion: egress.platform.io/v1alpha1
kind: EgressProfile
metadata:
  name: payment-api
  namespace: payment
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: payment-api
  mode: Observe
  baselineWindow: 24h
  policy:
    allowDNS: true
    generatedPolicyName: payment-api-egress-allowlist
```

Watch the baseline grow:

```bash
kubectl -n payment get egressprofile payment-api -w
# NAME          MODE      PHASE       DESTINATIONS
# payment-api   Observe   Observing   4

kubectl -n payment get cm egress-guardian-payment-api \
  -o jsonpath='{.data.baseline\.json}' | jq .
```

### Step 2 — Suggest

Switch to Suggest mode to generate a policy proposal:

```bash
kubectl -n payment patch egressprofile payment-api \
  --type=merge -p '{"spec":{"mode":"Suggest"}}'
```

A `EgressPolicyProposal` appears with score, risk level, and excluded destinations:

```bash
kubectl -n payment get egresspolicyproposal
# NAME                   PHASE             SCORE   RISK     APPROVED
# payment-api-proposal   PendingApproval   92      Medium   false

# Review the generated CiliumNetworkPolicy YAML
kubectl -n payment get cm egress-guardian-payment-api \
  -o jsonpath='{.data.policy\.yaml}'
```

Example generated policy:

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
spec:
  endpointSelector:
    matchLabels:
      app: payment-api
  egress:
    - toEndpoints:           # DNS rule (allowDNS: true)
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: kube-system
            k8s:k8s-app: kube-dns
      toPorts:
        - ports: [{port: "53", protocol: UDP}]
          rules:
            dns: [{matchPattern: "*"}]
    - toFQDNs:               # application allowlist
        - matchName: api.stripe.com
        - matchName: login.microsoftonline.com
        - matchName: storage.googleapis.com
      toPorts:
        - ports: [{port: "443", protocol: TCP}]
```

Destinations excluded from the policy (listed in `status.excludedDestinations`):

```
8.8.8.8:443 — High — "IP publique directe sans FQDN observé"
```

### Step 3 — Enforce

Switch to Enforce mode and approve:

```bash
# Switch mode
kubectl -n payment patch egressprofile payment-api \
  --type=merge -p '{"spec":{"mode":"Enforce"}}'
# → status.phase = AwaitingApproval (nothing applied yet)

# Approve after review
kubectl -n payment patch egresspolicyproposal payment-api-proposal \
  --type=merge -p '{
    "spec": {
      "approval": {
        "approved": true,
        "approvedBy": "ops-team"
      }
    }
  }'
# → CiliumNetworkPolicy applied, status.phase = Applied
```

> ⚠️ **WARNING** — Applying the policy puts the workload under **DEFAULT-DENY egress**.
> Every destination not in the allowlist (including DNS if `allowDNS: false`) will be dropped.
> The operator emits a `Warning` event `DefaultDenyEgress` to make this explicit.

Verify the CNP is active:

```bash
kubectl -n payment get ciliumnetworkpolicies
# NAME                           AGE   VALID
# payment-api-egress-allowlist   12s   True

kubectl -n payment get events --field-selector reason=DefaultDenyEgress
# Warning  DefaultDenyEgress  CiliumNetworkPolicy applied — DEFAULT-DENY egress active
```

---

## Verification

### Does the policy actually block traffic?

```bash
# Run a curl pod WITH the workload label (so CNP applies to it)
kubectl -n payment run test \
  --image=curlimages/curl \
  --labels="app=payment-api" \
  --restart=Never \
  --command -- sleep 3600

# Allowlisted → passes
kubectl -n payment exec test -- curl -sk --max-time 8 \
  https://api.stripe.com -o /dev/null -w "%{http_code}\n"
# → 404  (Stripe responds = traffic authorized by Cilium)

# Not in whitelist → blocked by Cilium
kubectl -n payment exec test -- curl -sk --max-time 8 \
  https://www.google.com -o /dev/null -w "%{http_code}\n"
# → 000  (connection timeout = Cilium drops the packet)

# Direct public IP (excluded as High risk) → blocked
kubectl -n payment exec test -- curl -sk --max-time 8 \
  https://8.8.8.8 -o /dev/null -w "%{http_code}\n"
# → 000  (dropped)
```

### Watch flows with Hubble

```bash
# See FORWARDED and DROPPED verdicts in real time
kubectl -n kube-system exec -it ds/cilium -- \
  hubble observe --namespace payment --type drop -f
```

---

## Configuration reference

### EgressProfile spec

| Field | Default | Description |
|-------|---------|-------------|
| `spec.targetRef` | required | Workload to govern (Deployment, StatefulSet…) |
| `spec.mode` | `Observe` | State driver: `Observe` \| `Suggest` \| `Enforce` |
| `spec.baselineWindow` | `24h` | Sliding retention window for accumulated flows |
| `spec.policy.allowDNS` | `true` | Auto-add kube-dns egress rule |
| `spec.policy.allowKubeSystem` | `true` | Allow traffic to kube-system |
| `spec.policy.generatedPolicyName` | `<name>-egress-allowlist` | CiliumNetworkPolicy name |
| `spec.gitOps.enabled` | `false` | Export YAML to `outputPath` |
| `spec.gitOps.outputPath` | `""` | Filesystem path for YAML export |

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `EGRESS_GUARDIAN_FLOW_SOURCE` | `mock` | `mock` or `hubble` |
| `HUBBLE_RELAY_ADDRESS` | `hubble-relay.kube-system.svc.cluster.local:80` | Hubble Relay gRPC address |
| `EGRESS_GUARDIAN_SNAPSHOT_INTERVAL` | `30s` | Accumulator snapshot interval |

---

## Helm chart

```bash
helm install egress-guardian charts/egress-guardian-operator \
  --namespace egress-guardian-system \
  --create-namespace \
  --set flowSource=hubble \
  --set hubble.relayAddress=hubble-relay.kube-system.svc.cluster.local:80 \
  --set operator.snapshotInterval=30s \
  --set resources.limits.memory=128Mi
```

Key values:

| Value | Default | Description |
|-------|---------|-------------|
| `image.repository` | `ghcr.io/ihsenalaya/egress-guardian-operator` | Image repository |
| `image.tag` | chart appVersion | Image tag |
| `flowSource` | `mock` | `mock` or `hubble` |
| `hubble.relayAddress` | `hubble-relay.kube-system.svc.cluster.local:80` | Hubble Relay address |
| `operator.snapshotInterval` | `30s` | Snapshot interval |
| `operator.leaderElect` | `true` | Enable leader election |

---

## Risk scoring

Each destination receives a confidence score (0–100). Higher = safer to include in an enforce policy.

| Condition | Delta |
|-----------|-------|
| Seen across ≥ 3 snapshot cycles (stable) | +25 |
| High flow count (≥ 10 flows) | +15 |
| Standard port (443, 80, 5432, 6379…) | +10 |
| Specific FQDN (no wildcard) | +10 |
| Seen only once | −15 |
| Direct public IP without FQDN | −30 |
| Large wildcard (`*.com`, `*.net`…) | −30 |

**Risk levels:**

- `Low` — score ≥ 80 → safe to include
- `Medium` — score 50–79 → review recommended  
- `High` — score < 50 **or** direct public IP **or** large wildcard → **never auto-included in Enforce**

The score is **advisory** — risk levels enforce the hard safety rules.

---

## Security rules

These are enforced automatically and cannot be bypassed:

| Rule | Reason |
|------|--------|
| `matchPattern: "*"` is never used for application egress | Would allow all FQDN traffic |
| `*.com`, `*.net`, `*.org`, `*.io` → always excluded | TLD-level wildcards are effectively allow-all |
| `*.amazonaws.com`, `*.cloudfront.net` → excluded from auto-enforce | CDN/cloud ranges span many customers |
| Direct public IP (no FQDN) → always excluded | Cilium cannot enforce FQDN rules on direct IP connections |
| High-risk destinations are listed in `status.excludedDestinations` | Full transparency on what is not in the policy |

---

## Limitations

1. **DNS visibility requires Cilium L7 DNS proxy** — if DNS L7 is not active, only raw IPs are visible and they will all be marked High.

2. **FQDN policy does not cover direct IP connections** — a workload connecting directly to an IP without DNS resolution will not be covered by `toFQDNs` rules.

3. **Default-deny egress is binary** — once the CNP is applied, all unlisted traffic is dropped. Use `Observe` mode long enough before enforcing.

4. **CDN IP correlation is approximate** — cloud providers share IP ranges across many customers; IP→FQDN mapping may be inaccurate.

5. **Hubble is a ring buffer, not a store** — the operator must run continuously to build a baseline; restarting clears in-memory state (persisted state in ConfigMap is preserved).

See [docs/limitations.md](docs/limitations.md) for the full list.

---

## Development

### Prerequisites

- Go 1.21+
- kubebuilder 3.14
- Docker
- kind + kubectl

### Run locally (mock mode)

```bash
# Install CRDs
make install

# Run the operator
EGRESS_GUARDIAN_FLOW_SOURCE=mock \
EGRESS_GUARDIAN_SNAPSHOT_INTERVAL=10s \
go run ./cmd/main.go --leader-elect=false
```

### Run tests

```bash
make test
# or just the unit tests (no envtest needed)
go test ./internal/baseline/... ./internal/cilium/... ./internal/observer/...
```

### Build and push the image

```bash
make docker-build docker-push IMG=ghcr.io/ihsenalaya/egress-guardian-operator:0.1.0
```

### Make targets

```bash
make help          # list all targets
make manifests     # regenerate CRDs + RBAC
make generate      # regenerate DeepCopy methods
make test          # run all tests
make build         # build the binary
make docker-build  # build the Docker image
make docker-push   # push to registry
make deploy        # deploy to current cluster
make undeploy      # remove from cluster
```

---

## License

Copyright 2026 — Apache License 2.0. See [LICENSE](LICENSE).

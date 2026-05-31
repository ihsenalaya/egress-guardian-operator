# Demo — egress-guardian-operator avec kind + Cilium + Hubble

## Prérequis

```bash
# Outils requis
kind version        # >= 0.20
kubectl version
helm version        # >= 3.x
cilium version      # CLI Cilium (optionnel, pour vérifications)
```

## 1. Créer le cluster kind avec Cilium

```bash
# Cluster kind sans CNI par défaut (Cilium prend la main)
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

# Installer Cilium via Helm
helm repo add cilium https://helm.cilium.io/
helm install cilium cilium/cilium \
  --namespace kube-system \
  --set kubeProxyReplacement=true \
  --set hubble.enabled=true \
  --set hubble.relay.enabled=true \
  --set hubble.ui.enabled=true \
  --set image.pullPolicy=IfNotPresent \
  --set ipam.mode=kubernetes

# Vérifier que Cilium est ready
kubectl -n kube-system rollout status ds/cilium
kubectl -n kube-system rollout status deploy/hubble-relay
```

## 2. Installer les CRDs de l'opérateur

```bash
cd egress-guardian-operator
make manifests
kubectl apply -f config/crd/bases/
```

## 3. Déployer la démo (mode mock, sans Hubble réel)

```bash
# Créer le namespace et le workload de démo
kubectl apply -f config/samples/demo_payment_api.yaml

# Construire et charger l'image dans kind
make docker-build IMG=egress-guardian-operator:dev
kind load docker-image egress-guardian-operator:dev --name egress-demo

# Déployer l'opérateur (flow-source=mock par défaut)
make deploy IMG=egress-guardian-operator:dev
kubectl -n egress-guardian-system rollout status deploy/egress-guardian-operator-controller-manager
```

## 4. Observer la baseline se construire

```bash
# Créer l'EgressProfile en mode Observe
kubectl apply -f config/samples/egressprofile_observe.yaml

# Surveiller le status
kubectl -n payment get egressprofile payment-api -w

# Inspecter la baseline dans le ConfigMap
kubectl -n payment get cm egress-guardian-payment-api -o jsonpath='{.data.baseline\.json}' | jq .
```

## 5. Passer en mode Suggest

```bash
kubectl -n payment patch egressprofile payment-api \
  --type=merge -p '{"spec":{"mode":"Suggest"}}'

# Une EgressPolicyProposal apparaît
kubectl -n payment get egresspolicyproposal

# Voir le YAML généré dans le ConfigMap
kubectl -n payment get cm egress-guardian-payment-api \
  -o jsonpath='{.data.policy\.yaml}'
```

## 6. Approuver et enforcer

```bash
# Passer en mode Enforce
kubectl -n payment patch egressprofile payment-api \
  --type=merge -p '{"spec":{"mode":"Enforce"}}'

# Approuver la proposal
kubectl -n payment patch egresspolicyproposal payment-api-proposal \
  --type=merge -p '{"spec":{"approval":{"approved":true,"approvedBy":"ops-team"}}}'

# Vérifier que la CiliumNetworkPolicy a été appliquée
kubectl -n payment get ciliumnetworkpolicies

# Voir l'événement Warning default-deny
kubectl -n payment describe egresspolicyproposal payment-api-proposal | grep -A3 DefaultDenyEgress
```

## 7. Utiliser Hubble Relay réel

```bash
# Redéployer avec flow-source=hubble
helm upgrade egress-guardian ... \
  --set env.EGRESS_GUARDIAN_FLOW_SOURCE=hubble \
  --set env.HUBBLE_RELAY_ADDRESS=hubble-relay.kube-system.svc.cluster.local:80

# Ou via kustomize, éditer config/manager/manager.yaml :
# - name: EGRESS_GUARDIAN_FLOW_SOURCE
#   value: hubble
```

## 8. Nettoyer

```bash
make undeploy
kubectl delete -f config/samples/demo_payment_api.yaml
kind delete cluster --name egress-demo
```

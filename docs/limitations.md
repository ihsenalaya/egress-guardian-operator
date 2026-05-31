# Limitations — egress-guardian-operator

## 1. Visibilité DNS dépend de Cilium

La résolution IP→FQDN repose sur le proxy DNS L7 de Cilium. Si la politique DNS L7 n'est pas active sur le namespace, Hubble ne rapporte pas les noms de domaine — seules les IPs brutes sont visibles. Ces IPs sont marquées **High** et exclues de l'auto-enforce.

**Mitigation** : activer `--enable-l7-proxy` (défaut dans Cilium) et la politique DNS dans Cilium avant de déployer l'opérateur.

## 2. FQDN policy ne couvre pas les connexions IP directes

Un workload qui contacte une IP publique directement (sans résolution DNS préalable) ne sera jamais dans la `toFQDNs` allowlist. Cilium ne peut pas matcher un FQDN sur une connexion qui n'a pas de flow DNS associé.

**Comportement de l'opérateur** : ces destinations sont détectées, marquées High, et listées dans `excludedDestinations` pour revue manuelle.

## 3. Mécanisme default-deny egress

Dès qu'une `CiliumNetworkPolicy` sélectionne un endpoint Cilium, **tout flux egress non listé est droppé** — y compris les requêtes DNS si `allowDNS=false`. C'est le comportement normal de Cilium en mode default-deny.

L'opérateur émet un Event Warning `DefaultDenyEgress` à chaque apply. Ne jamais passer en mode `Enforce` sans avoir validé la baseline sur plusieurs cycles.

## 4. Wildcards dangereux

`toFQDNs` avec `matchPattern: "*.amazonaws.com"` autorise **tout** sous-domaine AWS, ce qui inclut des buckets S3 publics ou des endpoints d'autres clients. L'opérateur exclut automatiquement ces wildcards de l'enforce ; ils nécessitent une revue manuelle.

## 5. Hubble Relay est un ring buffer, pas un store historique

Le ring buffer Hubble conserve quelques minutes à ~1h de flows en mémoire selon la charge. Il est impossible de demander des flows plus anciens. La baseline est construite sur l'accumulation en temps réel ; une instance de l'opérateur qui redémarre repart d'une baseline vide (état repris depuis le ConfigMap si le snapshot précédent existe).

## 6. Corrélation IP→FQDN imprécise sur les CDN

Les CDN (Cloudflare, CloudFront, Fastly) partagent des plages IP entre de nombreux clients. Une IP peut correspondre à plusieurs FQDNs selon l'heure. La corrélation via les flows DNS récents est bornée par les TTL DNS et peut être inexacte.

## 7. Nécessité d'un mode progressif (rollout)

L'apply d'une CNP est binaire : soit elle est là, soit elle ne l'est pas. Il n'y a pas de mécanisme de rollout progressif intégré. Pour un déploiement progressif, utiliser des GitOps tools (ArgoCD, Flux) avec review manuelle du diff avant apply.

## 8. Pas de webhook validating (optionnel)

Le prompt recommande un validating webhook qui rejette un `mode=Enforce` dont la proposal contient un wildcard large. Ce webhook n'est pas implémenté dans cette version ; c'est une prochaine étape.

## Prochaines étapes pour publication

- [ ] Helm chart complet (values, NOTES.txt, tests Helm)
- [ ] Validating webhook (rejeter Enforce avec wildcard High)
- [ ] Métriques Prometheus (destinations par workload, snapshots/s, apply count)
- [ ] Support multi-namespace dans l'accumulateur
- [ ] Corrélation IP→FQDN via cache DNS dédié
- [ ] Tests envtest complets (EgressProfile + EgressPolicyProposal reconciliation)
- [ ] CI GitHub Actions (lint, test, build, push)
- [ ] CRD vendorisé pour tests envtest sans Cilium installé

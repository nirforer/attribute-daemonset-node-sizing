# Live Karpenter test on EKS

How the DaemonSet-overhead behaviour was observed on a real Karpenter, first on
1.13.1 (affected by kubernetes-sigs/karpenter#715) and then on 1.14.1 (fixed by
PR #2975). Results are in `results/` and summarised by `summarize_rounds.py`.

## Setup (once, about 10 minutes)

1. IAM, SQS and EventBridge from Karpenter's own CloudFormation template:
   `aws cloudformation deploy --stack-name Karpenter-<CLUSTER_NAME> --template-file cloudformation.yaml --capabilities CAPABILITY_NAMED_IAM --parameter-overrides ClusterName=<CLUSTER_NAME>`
   (template: `https://raw.githubusercontent.com/aws/karpenter-provider-aws/v1.14.1/website/content/en/preview/getting-started/getting-started-with-karpenter/cloudformation.yaml`).
2. An IRSA role for the controller service account `karpenter/karpenter`, trusting
   the cluster's OIDC provider, with the six `KarpenterController*Policy-<CLUSTER_NAME>`
   managed policies attached.
3. An EKS access entry of type `EC2_LINUX` for `KarpenterNodeRole-<CLUSTER_NAME>`.
4. Karpenter itself: `helm upgrade --install karpenter-crd oci://public.ecr.aws/karpenter/karpenter-crd --version 1.13.1 -n karpenter --create-namespace`
   then `helm upgrade --install karpenter oci://public.ecr.aws/karpenter/karpenter --version 1.13.1 -n karpenter -f karpenter-values.yaml`.
   Change both versions to 1.14.1 for the second half.
5. `kubectl apply -f manifests/nodeclass.yaml -f manifests/nodepool.yaml -f manifests/probe.yaml`.
   The NodePool mirrors the customer's on-demand c6i pool: one pool from c6i.large to c6i.8xlarge.

## Rounds

The sensor DaemonSets under test are rendered from the tiered chart with busybox
images (the containers fail to start, which does not matter: scheduling and
Karpenter's planning only look at the pod spec). Three configurations:

* Attribute's existing Karpenter mode, 14 DaemonSets: `--set daemonset.enabled=true --set daemonset.karpenter=true`
* Three tiers on `karpenter.k8s.aws/instance-cpu`: `-f examples/attribute-tiers-instance-cpu.yaml`
* One tier keyed on `karpenter.sh/nodepool`: `-f manifests/tiers-nodepool.yaml`

Render with `helm template t charts/operator-chart-tiered -n attrb-karp-test -f charts/operator-chart-tiered/ci/ci-values.yaml <values> --set image.sensor.repository=public.ecr.aws/docker/library/busybox --set image.sensor.tag=1.36 --set image.operator.repository=public.ecr.aws/docker/library/busybox --set image.operator.tag=1.36`
and keep only the DaemonSets, ServiceAccounts, RBAC and PriorityClass (kinds
filter). Note: `helm template --show-only templates/06_daemonset_karpenter.yaml`
returns a single DaemonSet because that template starts every document with
`---` right after Helm's `# Source:` header; render the whole chart instead.

`round.sh <config> <label>` applies the DaemonSets, scales the `probe`
Deployment to 1 (its nodeSelector forces a new Karpenter node), records the
NodeClaim's `spec.resources.requests` (Karpenter's planned requests, DaemonSet
overhead included), the candidate instance types, the launched type, and which
sensor pod landed on the node. `teardown.sh <config>` scales the probe back,
deletes the NodeClaim and the DaemonSets.

## Teardown

Delete the NodePool and EC2NodeClass, uninstall both Helm releases, delete the
`karpenter` and `attrb-karp-test` namespaces, delete the access entry, the IRSA
role and the CloudFormation stack.

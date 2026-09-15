# Attribute sensor: DaemonSet resources per node size

Attribute's sensor ships as a Helm chart (`oci://quay.io/attribute/operator-chart`)
and runs one privileged eBPF pod per node. A DaemonSet has a single pod
template, so every sensor gets the same requests and limits whatever the node
size. This repo holds the analysis, a tiered-DaemonSet change to Attribute's
chart, the Karpenter evidence behind the design, and the test material.

## 1. What the chart does today (operator-chart 0.0.98)

| Mode | Values | Sizing |
|---|---|---|
| Operator (default) | `daemonset.enabled=false` | Operator creates one sensor pod per node at runtime, fixed `sensorresources` |
| Single DaemonSet | `daemonset.enabled=true` | One DaemonSet, fixed `sensorresources` |
| Karpenter DaemonSets | `daemonset.karpenter=true` | 13 DaemonSets keyed on `karpenter.k8s.aws/instance-cpu` plus a `default` DaemonSet with `NotIn` |

Attribute's customers run Karpenter. The requirement is a middle ground between
the last two modes: split the single DaemonSet into a few groups, each with its
own node selection and resources.

## 2. Karpenter and split DaemonSets

**The bug is real and documented.** When Karpenter plans a new node it adds up
the requests of every DaemonSet that could land on it. Before v1.14 it checked
DaemonSet affinity only against the NodePool's own requirements and skipped any
well-known label the NodePool does not constrain, such as
`karpenter.k8s.aws/instance-cpu` or `node.kubernetes.io/instance-type`. So a
DaemonSet pinned to `instance-cpu In [64]` was counted against every candidate
node, including 2-vCPU ones. This is
[kubernetes-sigs/karpenter#715](https://github.com/kubernetes-sigs/karpenter/issues/715),
open since March 2023, fixed by
[PR #2486](https://github.com/kubernetes-sigs/karpenter/pull/2486) and released in
Karpenter **v1.14.0 on 2026-07-11**. From v1.14 the overhead is grouped per
instance type, so instance-size labels become safe.

**Effect on the current Karpenter mode, on Karpenter < 1.14.** For a NodePool
that does not constrain instance-cpu, all 14 sensor DaemonSets are charged to
every new node:

| Planned sensor overhead per new node | CPU | Memory |
|---|---|---|
| Karpenter <= 1.13 | 7240m | 15520Mi |
| Karpenter >= 1.14, or what really lands | 100m to 1920m depending on size | 300Mi to 4Gi |

**Custom NodePool labels bypass the bug on every Karpenter version.** Labels set
in a NodePool's `spec.template.metadata.labels` are copied into the NodePool's
requirements when Karpenter builds its planning template
(`NewNodeClaimTemplate`). A DaemonSet requiring a custom label the NodePool does
not define is rejected outright ("custom labels must intersect, but if not
defined are denied"), and one requiring a value the NodePool does define must
match it. The same holds for `karpenter.sh/nodepool`, which Karpenter always
adds, so tiers can be keyed on NodePool names with no NodePool changes at all.
Granularity is per NodePool: one sensor size per NodePool, which is the
"between Karpenter mode and one DaemonSet" shape that was asked for.

**Evidence.** `karpenter-check/` is a small Go program that imports Karpenter's
own `pkg/scheduling` library (v1.14.1) and replays both the pre-1.14 and the
1.14 decision for four ways of splitting the sensor, against three NodePools
and four instance types. `output.txt` has the full run. Summary of planned
sensor DaemonSets per new node:

| Split strategy | Karpenter <= 1.13 | Karpenter >= 1.14 |
|---|---|---|
| A. Current karpconfig, 14 DaemonSets on instance-cpu | 14 counted on an unconstrained NodePool | exact, 1 |
| B. Tiers on custom NodePool label `attrb.io/sensor-size` | exact, 1 | exact, 1 |
| C. Tiers on `karpenter.sh/nodepool` | exact, 1 | exact, 1 |
| D. Two tiers on instance-cpu value ranges | 2 counted on an unconstrained NodePool | exact, 1 |

All 32 post-1.14 decisions matched what the node really receives once it exists.
The bug only affects planning of new nodes; for existing nodes Karpenter always
used the node's real labels.

Sources: Karpenter `pkg/scheduling/requirements.go` (`Compatible`, unchanged
between v1.13.0 and main), `pkg/controllers/provisioning/scheduling/scheduler.go`
(`getDaemonOverhead` in v1.13.0 vs `buildDaemonOverheadGroups` in v1.14),
`nodeclaimtemplate.go` (`NewNodeClaimTemplate`).

## 3. The change: tiered DaemonSet mode

`charts/operator-chart-tiered/` is operator-chart 0.0.98 plus tiered mode;
`patches/operator-chart-0.0.98-tiered-daemonsets.patch` is the diff for
Attribute. Files touched: `values.yaml`, `templates/05_daemonset.yaml`
(now a driver), new `templates/_sensor_daemonset.tpl` (the DaemonSet body,
moved verbatim). The Karpenter template and everything else are untouched.

```yaml
daemonset:
  enabled: true
  tierLabelKey: attrb.io/sensor-size        # default label key for tiers
  tiers:
    large:
      values: ["large", "xlarge"]           # label values that select this tier
      resources:                            # sensorresources shape; a cpu/memory block replaces
        cpu:    { request: "300m", limit: "1500m" }   # the global block as a whole, an absent
        memory: { request: "640Mi", limit: "3Gi" }    # block is inherited
    gpu:
      key: karpenter.sh/nodepool            # per-tier key override
      values: ["gpu-pool"]
      resources:
        cpu:    { request: "500m" }
        memory: { request: "1Gi", limit: "4Gi" }
      # tolerations: optional, replaces daemonset.selectors.tolerations for this tier
```

Rendering:

* One DaemonSet per tier, `<release>-<tier>`, node affinity
  `key In [values]` ANDed with the existing os and arch checks in one term.
* The catch-all keeps the historical name `<release>` and selector, with
  `key NotIn [all values of that key]` for every key used by tiers, so a node
  matches exactly one DaemonSet and unlabeled nodes get the catch-all.
* `tiers: {}` renders byte for byte what 0.0.98 renders today. Karpenter and
  operator modes render identically too (checked with `diff`).
* Render fails on: a label value claimed by two tiers, a tier named `default`,
  a non DNS-1123 tier name, missing values, missing key, a cpu or memory block
  without a request.

Migration behaviour, verified live (`scripts/gke-tiered-migration-test.sh`):

1. Install with no tiers: one DaemonSet `t`.
2. Upgrade with tiers: `t` is updated in place (same UID, rolling update at
   `rollout_max_unavailable`), `t-large` and `t-gpu` are created. No delete
   and recreate of the fleet, and never two sensors on an unlabeled node.
3. Label the node `attrb.io/sensor-size=large`: the catch-all pod is removed
   and a `t-large` pod with 300m / 640Mi appears.
4. Remove the label: the node returns to the catch-all.

Selector note for reviewers: tier DaemonSets add `attrb.io/sensor-tier` to
their selector; the catch-all's selector is unchanged because selectors are
immutable and renaming it would delete every sensor at once. Its selector
therefore also matches tier pods. That is safe: the DaemonSet controller only
manages pods whose ownerReference points at it, and the NotIn affinity keeps it
off tier nodes. If Attribute prefers disjoint selectors, name the catch-all
`<release>-default` as the Karpenter template does and accept the one-time
recreate.

Recommendation for the tier label on Karpenter clusters: a label the customer
sets on each NodePool, or `karpenter.sh/nodepool`. Instance-size labels only on
Karpenter >= 1.14.

## 4. What is still not covered

Operator mode. The operator receives four flat `SENSOR_RESOURCES_*` values and
creates pods itself; no chart change can size those per node. The operator
already reads Node objects and knows the target node when it creates a pod, so
sizing from node capacity there is a small operator change and would be
cloud-agnostic. Worth pitching alongside this chart change.

## 5. Earlier verification with demo charts (GKE, 2026-09-15)

Before the real chart was available, two demo charts implemented the same
In / NotIn partition keyed on `node.kubernetes.io/instance-type`:
`charts/agent` (profiles map, deep-merged resources) and
`charts/node-sized-agent` (busybox agent that logs its own requests through the
downward API). On cluster `aws-discrepancy-explainer` with e2-standard-2, -4
and -8 nodes, each node ran exactly one pod with its tier's resources, a
removed tier fell back to the catch-all, and a duplicate instance type failed
the render. `scripts/verify.sh` reproduces the check. Side finding: the single
DaemonSet template in 0.0.98 puts its os and arch checks in two separate
`nodeSelectorTerms`, which Kubernetes ORs; the tiered path uses one term.

## 6. Layout

```
charts/operator-chart-tiered/      Attribute chart 0.0.98 + tiered DaemonSet mode (the deliverable)
patches/                           diff of that change against 0.0.98
upstream/operator-chart-0.0.98/    pristine copy of Attribute's chart
karpenter-check/                   Go program using Karpenter's scheduling library, plus its output
examples/attribute-tiers-karpenter.yaml   tiered values used in the tests
examples/values-no-medium-tier.yaml       demo-chart override
scripts/gke-tiered-migration-test.sh      live single -> tiered -> label flip test
scripts/verify.sh                         one-pod-per-node check for the demo charts
charts/agent, charts/node-sized-agent     demo charts from the first two sessions
```

Test clusters: GKE `aws-discrepancy-explainer` (e2-standard-2/4/8 node pools)
and EKS `attribute-playground` (eu-central-1), which runs Attribute's own
release in single DaemonSet mode with a managed node group and no Karpenter.

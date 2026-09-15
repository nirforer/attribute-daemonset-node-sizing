# Can the customer's NodePools carry the sensor size today?

Short answer: no. Nothing in their current NodePool configuration lets a DaemonSet
tell a 2-vCPU node from a 32-vCPU node in a way that Karpenter 1.13 plans
correctly. The one label that is shaped like a size band sits in the wrong place
and covers the whole range. This note explains why, element by element, and what
a change on their side would have to look like. All numbers come from replaying
Karpenter's own scheduling library (`karpenter-check/`, sections E and F) and
from the live rounds on the EKS playground with Karpenter 1.13.1.

## 1. What Karpenter reads from a NodePool when it plans a node

Before it launches a node, Karpenter builds a planning template from the
NodePool and decides which DaemonSets will land on the node, so it can reserve
room for them. The template contains exactly three things:

1. `spec.template.metadata.labels`, turned into `key In [value]` requirements.
2. `karpenter.sh/nodepool In [<pool name>]`, added automatically.
3. `spec.template.spec.requirements`, as written.

A DaemonSet is counted when its tolerations cover the pool's taints and its
`nodeSelector` plus first required node-affinity term intersect that template.
Up to Karpenter 1.13 this is the whole test. A well-known label the pool does
not mention is simply skipped, and a range requirement such as
`instance-cpu Gt 1, Lt 34` intersects every tier value between 2 and 32.
From Karpenter 1.14 the test is repeated per candidate instance type, which is
what makes instance-size labels safe there
([kubernetes-sigs/karpenter#715](https://github.com/kubernetes-sigs/karpenter/issues/715),
[PR #2975](https://github.com/kubernetes-sigs/karpenter/pull/2975), v1.14.0).

Two consequences matter here. A label only helps if it reaches the planning
template, so it must be under `spec.template.metadata.labels`, not on the
NodePool object itself. And a label only sizes the sensor if its value differs
between small and large nodes, so one pool can carry exactly one size.

## 2. Their pool, element by element

The five rendered pools (`on-demand-c6i`, `-avro`, `-dynamic-env`, `-efs`,
`-largedisk`) share one requirement set and differ only in labels and taints.

| Element in their NodePool | Reaches nodes and planning? | Differs between a 2 and a 32 vCPU node? |
|---|---|---|
| `spec.template.metadata.labels`: `node.riskified.com/instanceCategory=c`, `instanceFamilies=c6i`, `capacityType`, `nodePoolType`, `karpenter.sh/capacity-type`, `Application=<app>`, `efs-capable`, `NodeAttribute=large-disk` | yes | no. They describe what runs on the node and how it is bought, and are identical for every size the pool launches |
| `metadata.labels`: the same set plus `node.riskified.com/nodePoolCoreSize: "2_33"` | **no**. These sit on the NodePool object only; Karpenter never copies them to nodes or into planning | the value is the whole band, 2 to 33 vCPU, and it is the same on all five pools |
| `karpenter.k8s.aws/instance-cpu Gt 1` and `Lt 34` | yes, as a range | this *is* the size dimension, but as a range every tier value from 2 to 32 intersects it, so every tier is counted |
| `instance-family In [c6i]`, `instance-generation Gt 4, Lt 8`, `capacity-type In [on-demand]`, `arch`, `os` | yes | no |
| `node.riskified.com/capacity-spread In [1-ondemand, 2-ondemand]` | yes | no, it is a spread key |
| `run-with-daemonset Exists`, `node.riskified.com/group Exists` | yes, but the value is chosen by whichever pending pod triggers the node | no. Karpenter plans the node before it knows its size, and a DaemonSet keyed on an `Exists` label is counted on every node of the pool |
| `karpenter.sh/nodepool` | yes | no, each pool spans 2 to 32 vCPU |
| taints (`capacityType`, `Application`, `NodeAttribute`, the efs startup taint) | used to drop DaemonSets that do not tolerate them | irrelevant here, the chart's default `operator: Exists` toleration covers all of them |

So a pool-keyed tier today can only say "this is a c6i on-demand node of the
default application", which is the same statement for c6i.large and
c6i.8xlarge. Section E4 of the replay and live round 2 show the result: exact
counting, and a 320m / 640Mi sensor on a 2-vCPU node.

## 3. The near miss: `nodePoolCoreSize`

`node.riskified.com/nodePoolCoreSize: "2_33"` is the only label in their
configuration that encodes a CPU band, and it encodes exactly the `Gt 1, Lt 34`
pair next to it. Two things stop it from helping:

* It is under `metadata.labels`, so it never reaches a node. Moving it into
  `spec.template.metadata.labels` is a one-line change in their Helm chart.
* Its value is the entire range the pool launches. Section F1 replays that
  move with a tier keyed on it: planning becomes exact on every Karpenter
  version, but c6i.large and c6i.8xlarge still receive the same sensor.

Its presence does suggest their chart already thinks in core-size bands, which
would make a split a values change rather than a template rewrite. Whether
other bands exist in their cluster is not visible from these five pools.

## 4. What a split would have to look like

For pool-keyed tiers to size the sensor, each pool must launch one size band.
Three ways to write that, replayed in section F against Karpenter 1.13 and 1.14
(sizes taken from Attribute's own `karpconfig` for the same CPU counts):

| Layout | Planned sensor DaemonSets per node, Karpenter <= 1.13 | Karpenter >= 1.14 |
|---|---|---|
| F2. Three pools with discrete sets `instance-cpu In [2,4]` / `In [8,16]` / `In [32]`, tiers keyed on `instance-cpu` | exact, 1 | exact, 1 |
| F3. Three pools with ranges `Gt 1, Lt 5` / `Gt 5, Lt 17` / `Gt 17, Lt 34`, tiers keyed on `instance-cpu` | **2**: the band's tier plus the `NotIn` catch-all, because Karpenter cannot prove a `NotIn` is empty over a range | exact, 1 |
| F4. The same ranges, each pool labelled `attrb.io/sensor-size: <band>`, tiers keyed on that label | exact, 1 | exact, 1 |

The F3 row is the trap: splitting the pools but keeping `Gt`/`Lt` ranges leaves
the catch-all over-counted on 1.13. Use discrete `In` sets, or give each band a
template label and key the tiers on the label.

## 5. What a split costs them

* **More pools.** Each of their five application variants times three bands is
  fifteen NodePools, each with its own limits and disruption budget.
* **Different instance choices.** Karpenter takes the highest-weight NodePool
  whose instance types can hold the pending pods. Today that is one pool and
  the cheapest c6i between 2 and 32 vCPU that fits the batch. With bands it is
  whichever band they weight highest, as long as its largest instance can hold
  the batch, so bin-packing behaviour changes and needs weights chosen on
  purpose.
* **Consolidation** is judged per pool and per pool limits, so the bands also
  change when nodes are replaced or removed.

None of this is the sensor chart's business. The chart supports both keys with
a single value, `daemonset.tierLabelKey`; which one the customer can use is a
property of their NodePools and their Karpenter version.

## 6. Bottom line

* On Karpenter 1.14 or newer: keep the pools as they are and key the tiers on
  `karpenter.k8s.aws/instance-cpu` (`examples/attribute-tiers-instance-cpu.yaml`).
* On Karpenter 1.13 or older: the bypass only sizes the sensor after the pools
  are split into bands, written as discrete `In` sets or with a per-band
  template label (F2 or F4). Otherwise the choices are the 680m / 1560Mi
  planning error of three instance-cpu tiers, or staying with one DaemonSet.

## 7. Observed on EKS with Karpenter 1.13.1

Same pool shape as theirs (c6i, `instance-cpu Gt 1, Lt 34`), a 100m / 128Mi
pod as the trigger. Baseline overhead on every round was 350m / 428Mi: the pod,
Attribute's own single-mode sensor already on the cluster, aws-node and
kube-proxy. Everything above that is planned sensor overhead.

| Round | DaemonSets | Karpenter planned | Sensor that really landed | Instance launched |
|---|---|---|---|---|
| 1. Three instance-cpu tiers + catch-all | 4 | 1030m / 1988Mi, so 680m / 1560Mi of sensor | `t-small`, 100m / 300Mi | c6i.large |
| 2. Tier keyed on `karpenter.sh/nodepool` + catch-all | 2 | 670m / 1068Mi, so 320m / 640Mi of sensor, exact | `t-c6i`, 320m / 640Mi | c6i.large |
| 3. Attribute's karpconfig mode | 14 | 1590m / 3528Mi, so 1240m / 3100Mi of sensor | `t-cpu4`, 100m / 300Mi | **c6i.xlarge**; c6i.large was dropped from the candidates because 3528Mi does not fit its 3114Mi allocatable |

Round 3 is the cost of the bug in one line: a 4-vCPU, 8 GiB instance for a
100m pod, because Karpenter reserved room for eight sensors, two of which
(`cpu12`, `cpu24`) match no c6i size at all.

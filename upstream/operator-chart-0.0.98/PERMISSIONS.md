# Permissions Reference — zouz-operator chart

Every permission the chart grants, which component holds it, and why it is
needed. Written to be shared with security reviewers. Applies to chart
`0.0.97`.

Object names below are the historical base names; since 0.0.94 every RBAC
object is release-scoped to allow multiple installs per cluster —
cluster-scoped objects (ClusterRoles, ClusterRoleBindings) are prefixed with
`<namespace>-<release>-`, namespaced objects (ServiceAccounts, Roles,
RoleBindings) with `<release>-`. The grants are unchanged by the prefixing.

Components and their identities (ServiceAccounts, all in the release
namespace):

| ServiceAccount | Component | Lifetime |
|---|---|---|
| `zouz-operator` | Operator Deployment | permanent |
| `zouz-sensor` | Sensor pods / DaemonSet | permanent |
| `attrb-cluster-inventory` | Cluster inventory Deployment | only when `clusterinventory.enabled=true` |
| `attrb-pre-install-hook` | Registration init Job (`zprobe-k8s-init`) | pre-install hook only — the SA, its RBAC, and the Job are deleted by Helm as soon as the hook succeeds |

---

## 1. Cluster-wide read-only access — `zouz-object-lister` ClusterRole

Bound to `zouz-operator` and `zouz-sensor` (the inventory gets an identical
copy, `attrb-object-lister`). Verbs are always **`get`, `watch`, `list`** —
this role grants no write access of any kind.

**Default: `apiGroups: ["*"], resources: ["*"]`.** The wildcard exists
because workload identification walks a pod's `ownerReferences` chain to its
topmost owner (Pod → ReplicaSet → Deployment, Pod → Job → CronJob, or any
controller CRD such as Argo Workflows), and the CRDs present on a customer
cluster cannot be predicted. Only object **metadata** (names, labels,
ownerReferences) is consumed on that path — never payload fields such as
`Secret.data` — but RBAC cannot express a metadata-only grant, hence the
allowlist alternative below.

**Restricted alternative:** setting `objectlistpermissions.rules` replaces
the wildcard with an enumerated allowlist (see
`examples/values-minimal-rbac.yaml`). Kubernetes RBAC has no deny rules, so
excluding Secrets is only possible this way. The recommended minimal set and
the reason for each entry:

| apiGroup | Resources | Why it is needed |
|---|---|---|
| `""` (core) | `nodes` | Node discovery for sensor placement/ranking, node capacity and lifecycle tracking, cloud-provider detection via `spec.providerID` |
| `""` | `nodes/proxy` | Kubelet `stats/summary` endpoint — per-node and per-pod CPU/memory utilization for telemetry |
| `""` | `pods` | Cluster-wide pod watch: mapping pods to workloads, extracting resource reservations (CPU/memory/GPU), computing per-workload coverage |
| `""` | `namespaces` | Namespace labels attached to workload telemetry |
| `""` | `services` | Load-balancer / service-endpoint identification for workloads |
| `""` | `persistentvolumes`, `persistentvolumeclaims` | Mapping pod volumes to storage for workload identity |
| `""` | `replicationcontrollers` | Legacy workload owner type (owner-chain resolution) |
| `apps` | `deployments`, `replicasets`, `statefulsets`, `daemonsets` | Standard workload owners for root-object resolution. `daemonsets` additionally serves cloud-environment detection (reads well-known DaemonSets in `kube-system`); `deployments` additionally lets the operator read its own Deployment (restart coordination) |
| `batch` | `jobs`, `cronjobs` | Workload owners (Pod → Job → CronJob resolution) |
| `networking.k8s.io` | `ingresses` | Load-balancer identification |
| `discovery.k8s.io` | `endpointslices` | Service-backend mapping for load-balancer tracking |
| `vpcresources.k8s.aws` | `cninodes` | EKS environment detection (AWS VPC CNI custom resource) |
| `argoproj.io` | `rollouts`, `workflows`, `cronworkflows` | Common CRD workload owners (Argo Rollouts / Argo Workflows) |

Add one rule per additional controller CRD that owns workload pods in your
cluster. If an owner CRD is missing from the allowlist, its pods are still
monitored but are reported as standalone Pods instead of being attributed to
their root workload, and the operator logs `Forbidden` list/watch errors —
nothing crashes.

**Secrets:** not in the allowlist, and nothing in the operator or sensor
reads Secret data. Under the wildcard default they are technically readable
(RBAC cannot exclude them from `*`); use the allowlist to remove that.

---

## 2. Namespace-scoped access (release namespace only)

| Role | Bound to | Resources / verbs | Why it is needed |
|---|---|---|---|
| `zouz-pod-manager` — rendered **only when `daemonset.enabled=false`** | `zouz-operator` | `pods` — all verbs | In evaluations mode the operator itself creates and deletes sensor pods in its own namespace to place them on nodes needing coverage. With `daemonset.enabled=true` sensors come from the Helm DaemonSet instead: this Role is not rendered at all and the operator runs with `DISABLE_EVALUATIONS=true`, so its pod-write code path never executes |
| `zouz-cm-reader` | `zouz-operator` | `configmaps` — `get`, `watch`, `list` | Reads the filter ConfigMaps (label `type=filter`) that define which workloads to track and their target coverage |

The sensor has **no** namespaced roles — the read-only ClusterRole above is
its entire API surface.

---

## 3. Pre-install hook (ephemeral — deleted by Helm when the hook succeeds)

| Grant | Scope | Why it is needed |
|---|---|---|
| `attrb-pre-install-hook-role` (ClusterRole): `daemonsets` — `get` | cluster-wide | Environment detection by the registration job (looks up well-known cloud-agent DaemonSets, e.g. in `kube-system`) |
| `attrb-pre-install-hook-secret-setter` (Role): `secrets` — `create`, `get`, `update`, `delete` | release namespace only | Registers the cluster with the Attribute backend and writes the resulting credentials Secret (`initconfig.secretName`) that the operator and sensors consume; also cleans up its temporary org-token Secret |

Both bindings, the ServiceAccount, and the Job itself carry
`helm.sh/hook-delete-policy: hook-succeeded` — they exist only for the
seconds the registration runs.

---

## 4. Linux privileges — sensor DaemonSet

The sensor is an eBPF + cross-container instrumentation agent, which is why
it needs host-level access at all. Both modes run with `hostPID: true`
(required to see host processes and map them to containers) and mount the
host rootfs at `/hostfs`. `sensorHostNetwork` is a separate, optional value.

### `reducePermissions: false` (default, legacy)

`privileged: true` — all capabilities, no seccomp, all host devices, host
`/sys` mounted read-write, `/hostfs` read-write.

### `reducePermissions: true` (recommended, kernel ≥ 5.10)

`privileged: false`, `capabilities.drop: [ALL]`, seccomp `RuntimeDefault`
enforced, no host device access, `/hostfs` read-only, host `/sys` narrowed
to `/sys/kernel/tracing` + `/sys/kernel/debug`, and only these capabilities:

| Capability | Why it is needed |
|---|---|
| `SYS_ADMIN` | uprobe registration: the kernel's `trace_uprobe.c` path was never migrated to honor `CAP_PERFMON` in the 5.8 capability split, so SSL/Go-TLS/gRPC/Node/.NET user-space probes require it. Also implies `CAP_BPF` and `CAP_PERFMON` (both kernel checks are `cap_x \|\| SYS_ADMIN`), which is why those are not listed separately |
| `SYS_PTRACE` | Traversing `/proc/<host-pid>/{root,ns/*}` magic links into target containers' mount/PID namespaces — workload inspection and kubelet kubeconfig detection. Checked by `__ptrace_may_access()`; `SYS_ADMIN` does not substitute |
| `SYS_RESOURCE` | Raising `RLIMIT_MEMLOCK` for BPF map memory on kernel 5.10 (no-op on 5.11+, where BPF accounting moved to memcg; 5.10 is the minimum supported kernel) |
| `DAC_OVERRIDE` | The Java instrumentor writes jattach + the agent jar into target containers' `/tmp` via `/proc/<pid>/root` when the target runs as a non-root UID. Strict superset of `DAC_READ_SEARCH`, which is therefore not listed |

AppArmor/SELinux are set to `Unconfined`/`spc_t` in reduced mode because the
default container LSM profiles block cross-container ptrace — `privileged:
true` used to mask this by disabling LSM confinement implicitly.

Deliberately **not** granted: `BPF`/`PERFMON` (redundant under `SYS_ADMIN`),
`NET_ADMIN`/`NET_RAW` (no raw sockets, no networking BPF program types),
`SYS_MODULE` (no kernel modules — also blocked by seccomp).

**Honest scope statement:** reduced mode removes the generic escape vectors
of privileged mode (raw host devices, disabled seccomp, writable host rootfs
and `/sys`, module loading, network manipulation). It does not sandbox the
sensor: cross-container instrumentation inherently requires the
ptrace/procfs pathway, so the sensor remains a highly privileged component
and does not meet Pod Security Standards `baseline`. Treat the sensor
DaemonSet as node-level infrastructure, like a CNI or monitoring agent.

---

## 5. Linux privileges — pre-install hook Job

Runs once at install with `hostPID: true`, `hostNetwork: true`, and the host
rootfs mounted **read-only** at `/mnt/rootfs`; deleted on success. Since
0.0.94 it is no longer `privileged: true` — it runs with
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`,
`capabilities.drop: [ALL]` and only:

| Setting | Why it is needed |
|---|---|
| `runAsUser: 0` + `DAC_READ_SEARCH` | Read permission-protected node-local files (e.g. the kubelet kubeconfig) during cluster registration |
| `SYS_PTRACE` + AppArmor `Unconfined` / SELinux `spc_t` | Traverse `/proc/<host-pid>/root/...` into the kubelet's mount namespace; the default container LSM profiles block this even with the capability (previously masked by `privileged: true`) |

Configurable via `initconfig.securityContext` (set `privileged: true` there
to revert to the legacy behaviour).

## 6. Operator and cluster-inventory pods

No elevated Linux privileges — no host mounts, no `hostPID`/`hostNetwork`,
no added capabilities. Since 0.0.94 both run under a restricted
securityContext: non-root (uid/gid 65532), `seccompProfile: RuntimeDefault`,
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`,
`capabilities.drop: [ALL]`. Their only permission surface is the Kubernetes
RBAC in sections 1–2. Opt-in NetworkPolicies (`networkPolicy.enabled`) can
additionally restrict pod-level network reachability.

## 7. Optional in-cluster OTEL collector proxy

Disabled by default (`otelproxy.enabled`); it exists to concentrate cluster
egress for telemetry traffic into a single pod.

(The chart also shipped a LaunchDarkly relay proxy, `launchdarklyproxy`, for
the same reason on the feature-flag side. It was removed in chart 0.0.98:
operator 0.0.32 and sensor 0.0.291 evaluate flags against Unleash, not
LaunchDarkly, so the relay was never contacted. The equivalent today is to run
your own Unleash Edge and point `featureFlags.url` at it — the chart then
renders it as `UNLEASH_URL` on the operator and sensors. The read-only Unleash
client token stays compiled into both binaries and is deliberately never
written into a pod spec.)

The collector does not talk to the Kubernetes API — **no Roles, ClusterRoles,
or bindings exist for it**. It runs as a plain unprivileged container (no host
mounts, no added capabilities, no `hostPID`/`hostNetwork`) under the namespace
`default` ServiceAccount. It receives the org token (`API_KEY`) as env, since
it authenticates the telemetry it forwards to `export_url`, and serves its
in-cluster gRPC receiver over TLS with a chart-provided certificate (clients
connect with server-certificate verification disabled). When NetworkPolicies
are enabled, note the collector pod does not carry the chart's instance label
and is therefore not governed by the default-deny policy.

## 8. Java agent auto-injection (`javaAutoInject.enabled=true`)

Disabled by default. When enabled, one of two admission backends adds an
init container, an `emptyDir` volume and a `JAVA_TOOL_OPTIONS` entry to pods
that opt in via the `instrumentation.attrb.io/inject-java: "true"` annotation
on the pod or its namespace. Nothing else is modified, and non-opted-in pods
are left byte-identical.

**Kubernetes RBAC.** In `policy` mode (Kubernetes ≥ 1.36) there is no
component and no identity at all — the mutation is CEL evaluated inside the
apiserver. In `webhook` mode the webhook Deployment runs under its own
`<release>-inject-java-webhook` ServiceAccount whose only grant is
**`get`/`watch`/`list` on `namespaces`**, needed because admission requests
do not carry the namespace object and namespace-level opt-in must be
resolved. It reads no pods, no Secrets, and writes nothing.

**Linux privileges of the injected init container.** None: since 0.0.97 it
is restricted-Pod-Security-Standard compliant, and both backends set it
identically — `runAsNonRoot: true`, `runAsUser`/`runAsGroup: 65532` (the
agent image's own user), `allowPrivilegeEscalation: false`,
`privileged: false`, `readOnlyRootFilesystem: true`,
`capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`, plus CPU/memory
requests and limits. It runs one `cp` of the agent jar into the shared
`emptyDir` and exits; the application containers mount that volume
**read-only**. No host mounts, no `hostPID`/`hostNetwork`, no capabilities
added.

This means injection needs **no Pod Security exception** (no Kyverno
`PolicyException`, no Gatekeeper exemption) on a cluster enforcing
`restricted`. Configurable via `javaAutoInject.securityContext` and
`javaAutoInject.resources`; on OpenShift, whose restricted-v2 SCC assigns
per-namespace UID ranges, set `runAsUser`/`runAsGroup` to `null` there.

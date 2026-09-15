---
title: Zouz Operator Chart - Release Notes
subtitle: Comprehensive Release History and Version Updates
author: Attribute
date: May 26, 2026
lang: en
---
# Release Notes

---

## Version 0.0.99 (proposed)
**Release Date:** unreleased

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.98 → 0.0.99 (pending)

### 📐 Tiered sensor DaemonSets (DaemonSet mode)

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.99` | • New `daemonset.tiers` / `daemonset.tierLabelKey`: in DaemonSet mode (`daemonset.enabled=true`, `daemonset.karpenter=false`) the sensor DaemonSet can be split into tiers, each pinned to a set of node-label values (`<key> In [...]`) with its own `resources` and optional `tolerations`, plus the catch-all DaemonSet (`<key> NotIn [...]`) that keeps the release name and selector so enabling tiers is a rolling update, not a recreate. A label value listed in two tiers, a tier named `default`, or a cpu/memory block without a request fails the render. <br/> • With `tiers` empty the render is byte-identical to 0.0.98; Karpenter and operator modes are unchanged. <br/> • Guidance: key tiers on a label set on the NodePool (`spec.template.metadata.labels`) or on `karpenter.sh/nodepool`. Instance-size labels (`karpenter.k8s.aws/instance-cpu`, `node.kubernetes.io/instance-type`) are only safe on Karpenter >= 1.14.0: older Karpenter counts every tier's DaemonSet against every new node (kubernetes-sigs/karpenter#715, fixed by #2975). The existing `karpconfig` mode is affected by the same bug on Karpenter < 1.14. |

---

## Version 0.0.98
**Release Date:** September 8, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.97 → 0.0.98

### 🚩 Feature Flags: LaunchDarkly → Unleash

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.98` | • The `launchdarklyproxy` (ld-relay) proxy is removed — nothing uses LaunchDarkly any more. Enabling it now fails the render with a pointer to the replacement. <br/> • New `featureFlags.url`: point it at your own Unleash Edge to keep feature-flag traffic in cluster. Leave it empty (default) and nothing changes. |
| **Operator** | `0.0.32` | • Feature flags moved from LaunchDarkly to Unleash. Nothing to configure — the defaults are built in. |
| **Sensor** | `0.0.291` | • Feature flags moved from LaunchDarkly to Unleash. <br/> • AgentCore EC2 detection. <br/> • Static AgentCore Python. <br/> • rustls: handle missing symbols. <br/> • eBPF Go dependency updated for 7.1. |

---

## Version 0.0.97
**Release Date:** July 29, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.96 → 0.0.97

### 🔒 Security / Hardened Clusters

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.97` | • **Java auto-inject now works on hardened clusters.** The injected init container previously carried no `securityContext` and no resource requests/limits, so on a cluster enforcing the restricted Pod Security Standard (PSA `enforce: restricted`, or the equivalent Kyverno/Gatekeeper policies) the *pod it was injected into* was rejected — `allowPrivilegeEscalation != false`, `unrestricted capabilities` — turning instrumentation into an outage that only a Kyverno `PolicyException` worked around. Both backends (`policy` and `webhook`) now set a restricted-compliant `securityContext` (`allowPrivilegeEscalation: false`, `privileged: false`, `readOnlyRootFilesystem: true`, `runAsNonRoot: true`, `runAsUser`/`runAsGroup: 65532`, `capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`) plus `resources` requests **and** limits (`100m` CPU / `64Mi` memory, requests equal to limits so a Guaranteed pod is not demoted to Burstable — pod QoS counts init containers). No policy exception is needed any more. Both are configurable via the new `javaAutoInject.securityContext` and `javaAutoInject.resources`; set either to `null` to inject nothing. `runAsUser`/`runAsGroup` match the agent image's own non-root user and are pinned rather than inherited so injection also survives a pod whose pod-level `securityContext` sets `runAsUser: 0`. **OpenShift:** its restricted-v2 SCC assigns each namespace a UID range and rejects a container asking for a UID outside it — set `javaAutoInject.securityContext.runAsUser` and `.runAsGroup` to `null` there; the agent image is already non-root, so `runAsNonRoot: true` still holds. Requires operator `0.0.31`. |
| **Operator** | `0.0.31` | • The webhook backend takes the injected container's hardening from two new flags the chart renders as JSON, `--inject-security-context` and `--inject-resources`, keeping it identical to what the `MutatingAdmissionPolicy` produces. Unknown fields in either are rejected at startup, so a typo in the Helm values fails while deploying rather than silently shipping an unhardened init container that then fails admission. |

### ✨ Features

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.284` | • Java dynamic instrumentation improvements. <br/> • VertexAI improvements. <br/> • MongoDB attribution improvements. |

---

## Version 0.0.96
**Release Date:** July 23, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.95 → 0.0.96

### ✨ Features

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.96` | • Java auto-inject now works on **Kubernetes < 1.36** via a new webhook backend. `javaAutoInject.mode` selects the backend: `policy` (the existing MutatingAdmissionPolicy, K8s ≥ 1.36), `webhook` (a MutatingWebhookConfiguration + admission server, K8s ≥ 1.16), or `auto` (default — picks `policy` on ≥ 1.36, else `webhook`). Both backends perform the identical pod mutation and still require `sensorDisableAutoJavaInstrumentation=true`. Webhook mode ships a self-contained TLS setup (self-signed cert generated by the chart and persisted across upgrades — no cert-manager dependency), a 2-replica webhook Deployment (`javaAutoInject.webhook.*`) spread across nodes by a soft podAntiAffinity and protected by a `maxUnavailable: 1` PodDisruptionBudget, and least-privilege RBAC (namespaces read-only). The webhook always excludes its own namespace and kube-system from interception (no `failurePolicy=Fail` bootstrap deadlock). Admission stays fail-open by default (`failurePolicy: Ignore`) with a 2s `timeoutSeconds`, so a crashed or unreachable webhook leaves pods starting unmutated rather than blocking them; `javaAutoInject.webhook.namespaceSelector` can narrow the scope further and is recommended on large clusters (every pod CREATE in matching namespaces round-trips through the webhook). Compatible with `networkPolicy.enabled=true`: the chart's default-deny-ingress would otherwise drop the apiserver's admission call, so webhook mode additionally opens the webhook port (all other chart pods stay default-deny). Requires operator `0.0.30`. |
| **Operator** | `0.0.30` | • New `--webhook` mode: the operator image also runs the Java-inject admission webhook server (`injectwebhook` package). Honors pod- and namespace-level opt-in annotations, fails open (no patch) on error. Hardened for the admission path: request bodies are size-capped (the port is reachable cluster-wide, so an unbounded read could OOM the container), full `http.Server` read/write/idle timeouts, `/readyz` reflects the namespace cache so an unsynced or terminating replica leaves the Service, and the per-injection log line sits at Debug (`--debug`) instead of flooding the pipeline during a rollout — use the apiserver's `apiserver_admission_webhook_admission_duration_seconds` for aggregate visibility. The webhook Deployment now also sets `GOMEMLIMIT`/`GOMAXPROCS` from its cgroup limits, and drains via a `preStop` sleep (`javaAutoInject.webhook.preStopDrainSeconds`) so a rollout or node drain does not silently skip injection while Endpoints removal propagates. • Cluster-scoped workloads (e.g. a global CRD owner) now carry the pod's namespace as an `attrb_pod_ns` label, since the workload itself has none. |

---

## Version 0.0.95
**Release Date:** July 9, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.93 → 0.0.95

### 🔒 Security / RBAC Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.95` | • Security hardening release: reduced pod privileges across the chart (pre-install hook, operator, cluster inventory), optional read-only RBAC allowlist for the object-lister ClusterRoles with a recommended minimal example that excludes Secrets (`examples/values-minimal-rbac.yaml`), opt-in NetworkPolicies, release-scoped object names (multiple installs per cluster), a new `PERMISSIONS.md` permissions reference, and minor template/doc fixes. Requires operator `0.0.29`. |
| **Operator** | `0.0.29` | • Support for the chart's release-scoped names: sensor pods created at runtime take their ServiceAccount and PriorityClass from the new `SENSOR_SERVICE_ACCOUNT`/`SENSOR_PRIORITY_CLASS` env. |
| **Sensor** | `0.0.283` | • Sensor image bump. |
| **Java agent** | `0.0.11` | • Auto-inject Java agent image bump. |

---

## Version 0.0.92
**Release Date:** June 23, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.91 → 0.0.92

### 🔒 Security / RBAC Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.92` | • Restricted operator RBAC in DaemonSet mode: the namespaced `zouz-pod-manager` Role (`pods: ["*"]`) and its RoleBinding are no longer created when `daemonset.enabled=true` (covers both standard and Karpenter DaemonSet modes). In DaemonSet mode the operator runs with `DISABLE_EVALUATIONS=true` and never performs pod CRUD, so the write/exec permissions on pods are dropped. Pod reads for telemetry remain covered by the cluster-wide read-only `zouz-object-lister` ClusterRole. |

---

## Version 0.0.91
**Release Date:** June 23, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.90 → 0.0.91

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.280` | • Java disable flag. |
| **Sensor** | `0.0.279` | • Kernel 7.x eBPF support. |
| **Sensor** | `0.0.278` | • SQL normalizer RHS comparation bugfix. • redshift support. |
| **Sensor** | `0.0.277` | • SelfManaged/Standalone k8s namespace labels support |
| **Sensor** | `0.0.276` | • SQLPostExtraction convert hex to uuid. |
| **Chart** | `0.0.91` | • Added `sensorDisableAutoJavaInstrumentation` value (default `false`) that sets `JAVA_INSTRUMENTATION_DISABLED=TRUE` on sensor DaemonSet pods. • Added `javaAutoInject` (default `false`): a Kubernetes 1.36+ `MutatingAdmissionPolicy` that injects the Attribute Java agent into opted-in pods (annotation on the pod or its namespace) at admission time (init container + shared volume + `JAVA_TOOL_OPTIONS`). Renders only on k8s ≥ 1.36 and requires `sensorDisableAutoJavaInstrumentation=true` so it never runs alongside the sensor's runtime Java instrumentation; `JAVA_TOOL_OPTIONS` merge logic is shared with cel-go unit tests under `tests/`. |


---

## Version 0.0.90
**Release Date:** June 14, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.89 → 0.0.90

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.275` | • SQL support INSERT attribution. • Fixing config comparation crash. |
| **Sensor** | `0.0.274` | • RDS v2 events. • HTTP Path replacement. • HTTP remove v1 events. • New metric, self hosted AI: hosted-ai-interface. • tls cert verify domain IO domain.  |


---

## Version 0.0.89
**Release Date:** June 14, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.88 → 0.0.89

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Operator** | `0.0.28` | • Added namespace labels. • Labels export improvements. |
| **Sensor** | `0.0.273` | • Bedrock converse camelcase cache tokens fix & added context size. |
| **Sensor** | `0.0.272` | • Bedrock attribution. |


---

## Version 0.0.88
**Release Date:** May 26, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.87 → 0.0.88

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.271` | • Fixing potential issues with execve heavy workload instrumentation. |


---

## Version 0.0.87
**Release Date:** May 25, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.86 → 0.0.87

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.270` | • Anonymous SSL connections resolving. |
| **Sensor** | `0.0.269` | • Dependency upgrades. |
| **Sensor** | `0.0.268` | • Configurable socket filter. |
| **Sensor** | `0.0.267` | • VertexAI enhanced support. • Memory leak fix. |
| **Sensor** | `0.0.266` | • rust_tls_native support. • accept4 bugfix. |


---

## Version 0.0.86
**Release Date:** May 12, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.85 → 0.0.86

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.265` | • bugfix ssl anonymous crash. • bugfix claude code instrumentation & extended version support. • mysql allowed values & post extraction regex on endpoint. |
| **Sensor** | `0.0.264` | • kafka major improvements. |
| **Sensor** | `0.0.263` | • fixing doubled log issue. |
| **Sensor** | `0.0.262` | • gcp env detector idms retry mechanism. • prevent collision between init failed exit code to runtime unrecovered panics. |
| **Sensor** | `0.0.261` | • kafka fix msg count. • k8s endpoint better handling - sending Service Ip with WL params, instead of drop event. wl "Service" kind in case when we cannot locate specific workload. |
| **Sensor** | `0.0.260` | • mysql tiny bugfix. • mongo bugfixes. • kafka bugfixes. • Fix sensor race condition crash due to anon ssl process exit concurrent map access. • remove LD redundant allocations. |
| **Sensor** | `0.0.259` | • mysql/pgsql/mssql switching lexical analyzer. • Rust instrumentation. • gcs grpc. |
| **Sensor** | `0.0.258` | • GC & memory optimizations. • go tls bugfix. • bugfix swarm cpu calculation. • mongo bugfix loosing long connection. • support gcs via s3 rusty library. |
| **Sensor** | `0.0.257` | • s3 url path style bugfix. • claude instrumentation. |
| **Sensor** | `0.0.256` | • vertexai add region. • sqs v2 metrics, attribution rules. • seccomp dep upgrade. • grpcio versions. |
| **Sensor** | `0.0.255` | • bedrock support non token api. • zeebe ttl 2 hours. • dep & golang 1.26.2. • grpcio versions. |
| **Sensor** | `0.0.254` | • mysql attribution rule bugfix. |
| **Sensor** | `0.0.253` | • bigtable add projectid & support more apis. |
| **Sensor** | `0.0.252` | • pgsql attribution bugfix. • gprcio versions. |
| **Sensor** | `0.0.251` | • support openssl 4.0. • bigtable support. • grpcio instumentation support. • bigquery major improvements. |
| **Sensor** | `0.0.250` | • gcs not sending empty path. • sql attribution rule expansion. |
| **Sensor** | `0.0.249` | • mongo regex extraction fix. |
| **Sensor** | `0.0.248` | • kafka bugfix. • pgsql ignore place holder. • mongo attribution rules regex extractions on dbName & collection & regular value. • bedrock support embedding model. |
| **Sensor** | `0.0.247` | • pgsql support ANY(). |


---

## Version 0.0.85
**Release Date:** April 9, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.84 → 0.0.85

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.85` | • Added `priorityclass.existingName` value to support using a pre-existing PriorityClass on DaemonSet pods. • Added priorityClassName support to the Karpenter DaemonSet template. |
| **Operator** | `0.0.27` | • Dependencies upgrade. |
| **Sensor** | `0.0.246` | • Dependencies upgrade. • Protocol extraction improvements. |

---

## Version 0.0.84
**Release Date:** March 27, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.83 → 0.0.84

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.84` | • Added `extraEnv` value for injecting additional environment variables globally to operator deployment, daemonsets, and k8s-init job. |

---

## Version 0.0.83
**Release Date:** March 17, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.80 → 0.0.83

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Init Config** | `1.0.14` | • distroless |
| **Init Config** | `1.0.13` | • Azure redhat openshift support. |
| **Cluster Inventory** | `0.1.4` | • Quay artifact registry. |
| **Operator** | `0.0.26` | • distroless & libraries upgrade. |
| **Operator** | `0.0.25` | • Quay artifact registry. |
| **Sensor** | `0.0.239` | • kafka fixes & improvements |
| **Sensor** | `0.0.238` | • Azure redhat openshift support |
| **Sensor** | `0.0.237` | • binanalyzer read with zero allocation. <br/> kafka bugfix |
| **Sensor** | `0.0.236` | • GRPC support request & json extraction. <br/> golang 1.26.1 & libraries upgrade |
| **Sensor** | `0.0.235` | • Quay artifact registry. <br/> http v2 rules support commaSplit & responseheader |
| **Sensor** | `0.0.234` | • Bedrock support agent. <br/>  azure support scaleset. <br/> azure support self managed. <br/> pod2pod resolve. <br/> grpc support request and json extraction. |
| **Sensor** | `0.0.233` | • loggin rate limit. <br/>  bq fix. <br/> kafka batch fix. |
| **Sensor** | `0.0.232` | • mssql support redirect. |

---

## Version 0.0.80

**Release Date:** February 22, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.79 → 0.0.80

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.231` | • GCS support more operations. <br/> Azure openai fix |
| **Init Config** | `1.0.11` | • Azure support auto cluster detection |

---

## Version 0.0.79

**Release Date:** February 10, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.78 → 0.0.79

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.230` | • bigquery revert identification of job. <br/> |
| **Sensor** | `0.0.229` | • mysql fix missing db name when query is REDACTED. <br/> HTTP2 better handling corruption frames. <br/> FF http send empty path. <br/> module monitor fix path detection. <br/> bq support attribution from query param. <br/> bedrock support streaming and http2. <br/>. <br/> openai support metricsv2 & streaming & fix missing cache tokens & add reasoning. <br/>  |
| **Sensor** | `0.0.228` | • protometricsv2 flushing optimization.|
| **Sensor** | `0.0.227` | • ARM build without kprobes/uprobes, only trampolines. <br/>  better filtering of istio-sidecar.|
| **Sensor** | `0.0.226` | • disable user stdout & stderr. <br/>  http2 better handling H2C.|
| **Sensor** | `0.0.225` | • http2 better headers frame handling. |
| **Sensor** | `0.0.224` | • filter out istio sidecar (envoy) workers. <br/> http validations of path and host. <br/> http2 improvements. <br/> http2 mini refactor. <br/> improve stale process cleaning. |
| **Sensor** | `0.0.223` | • FF disable ssl probes. <br/> inactivity streams optimization. |
| **Sensor** | `0.0.222` | • FF disable client-socketstat metric. |
| **Sensor** | `0.0.221` | • http-server value override fix.|
| **Sensor** | `0.0.220` | • http-server attribution rules improvements.|
| **Sensor** | `0.0.219` | • http-server support workload whitelist.|
| **Sensor** | `0.0.218` | • reduce launch darkly outputs.|
| **Sensor** | `0.0.217` | • sqs bug fix.|
| **Sensor** | `0.0.216` | • couchdb improvements.<br/> supports gcs attribution rules |
| **Sensor** | `0.0.215` | • openai support project-id.<br/>anthropic support org id. |
| **Sensor** | `0.0.214` | • supports schema name as dbname.<br/> |
| **Sensor** | `0.0.213` | • supports k8s multi kubeconfig locations.<br/> supports anthropic.<br/>ptrace bug fix. |

---


## Version 0.0.77

**Release Date:** January 6, 2026

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.76 → 0.0.77

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.212` | • gcp vm support .<br/> • remove dedicated cgroups, using oom priority and host cgroups .<br/> |
| **Sensor** | `0.0.211` | • amqp 1.0 Empty Queue Name Fix and Link Handshake logic refactor .<br/> • AMQP091 Implementing resync for.<br/> • bedrock support more tokens and sampler.<br/> |
| **Init Config** | `1.0.10` | • Support ec2 standalone.<br/> |

---

## Version 0.0.76

**Release Date:** December 24, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.75 → 0.0.76

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.210` | • Golang instrumentation memory optimization.<br/> |
| **Sensor** | `0.0.210` | • Lock contention performance improvements.<br/> |

***

## Version 0.0.75

**Release Date:** December 23, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.74 → 0.0.75

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.209` | • Adjusting GC memory threshold.<br/> |

***
## Version 0.0.74

**Release Date:** December 23, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.73 → 0.0.74

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.74` | • Added init config secret name verification.<br/> |
| **Sensor** | `0.0.203` | • Bedrock extraction improvements, Docker installation file, Metricbuilder race condition fix, HTTP parsing improvements.<br/> |
| **Sensor** | `0.0.204` | • RDS data API support.<br/> |
| **Sensor** | `0.0.205` | • RDS data API ARN identification.<br/> |
| **Sensor** | `0.0.206` | • Dependency upgrade, protocol signature detection support, performance optimizations.<br/> |
| **Sensor** | `0.0.207` | • Fargate launcher permission fix.<br/> |
| **Sensor** | `0.0.208` | • Invalid reference crash fix.<br/> |

***
## Version 0.0.73

**Release Date:** December 17, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.72 → 0.0.73

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Chart** | `0.0.73` | • Support priority class for sensor.<br/> |
| **Sensor** | `0.0.194` | • HTTP Brotli compression support <br/> |
| **Sensor** | `0.0.195` | • HTTP JSON performance improvements <br/> |
| **Sensor** | `0.0.196` | • Allowing to disable uprobes <br/> |
| **Sensor** | `0.0.197` | • OpenAI & OpenSSL instrumentation bugfix <br/> |
| **Sensor** | `0.0.198` | • Elasticsearch index_name attribution support <br/> |
| **Sensor** | `0.0.199` | • HTTP processing optimization <br/> |
| **Sensor** | `0.0.200` | • Memory pool adjustments <br/> |
| **Sensor** | `0.0.201` | • BUGFIX - memory pool edge case handling <br/> |
| **Sensor** | `0.0.202` | • Docker Swarm support <br/> |
***
## Version 0.0.72

**Release Date:** November 26, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.71 → 0.0.72

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Init Config** | `1.0.8` | • Trim white spaces from token.<br/> |
| **Operator** | `0.0.24` | • Trim white spaces from token.<br/> |
| **Sensor** | `0.0.194` | • Support brotli compression.<br/> |
| **Sensor** | `0.0.193` | • Trim white spaces from token.<br/> |
| **Sensor** | `0.0.192` | • CouchDB customer attribution fix.<br/> |
| **Sensor** | `0.0.191` | • OpenAI/Azure OpenAI support header/jwt customer attribution.<br/> |
| **Sensor** | `0.0.190` | • Http support custom client attribution.<br/> |
| **Sensor** | `0.0.189` | • CouchDB support.<br/>• Supporting Additional HTTP Extraction Rules<br/>• Dep updates (go-json instead of encoding/json).<br/> |
***
## Version 0.0.71

**Release Date:** November 05, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.70 → 0.0.71

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Charts** | `0.0.71` | • Removing labels from coverage config since it's not used by the operator |
| **Sensor** | `0.0.186` | • New ESC AMI support alongside Bottlerocket based images.<br/>• DynamoDB OOB Read Bugfix.<br/>• MySQL parsing improvements<br/>• Supporting Additional HTTP Extraction Rules<br/>• Fargate NodeJS Support |

***

## Version 0.0.70

**Release Date:** October 29, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.69 → 0.0.70

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.184` | • HTTP Race Condition Fix<br/>• Dependency Updates |

***

## Version 0.0.69

**Release Date:** October 20, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.68 → 0.0.69

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.183` | • SQS Protocol Edge Case Fix<br/>• Deduplicating MongoDB query values<br/>• HTTP Client Metrics<br/>• PGSQL Subquery Parsing Support<br/>• MongoDB specific customer identifier support |

***

## Version 0.0.68

**Release Date:** October 20, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.67 → 0.0.68

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `0.0.181` | Solved issue with HTTP packet read |

***

## Version 0.0.67

**Release Date:** October 19, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.66 → 0.0.67

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Charts** | `0.0.67` | • Set init hook dnsPolicy to ClusterFirstWithHostNet |
| **Init Config** | `v1.0.8` | • AWS Go SDK v2 upgrade |

***

## Version 0.0.66

**Release Date:** October 17, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.65 → 0.0.66

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Init Config** | `v1.0.7` | • Logging registration requests |

***

## Version 0.0.65

**Release Date:** October 16, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.64 → 0.0.65

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Init Config** | `v1.0.6` | • Support GKE detection for old auth mode |
| **Sensor** | `v0.0.180` | • Support GKE detection for old auth mode<br/>• HTTP parsing performance improvements<br/>• CockroachDB support (pgwire) |

***

## Version 0.0.64

**Release Date:** October 16, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.63 → 0.0.64

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Init Config** | `v1.0.5` | • Added GKE Detector Logs |

***

## Version 0.0.63

**Release Date:** October 10, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.62 → 0.0.63

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Charts** | `0.0.63` | • Added global tolerations and nodeSelector (does not apply to daemonset) |

***

## Version 0.0.62

**Release Date:** October 9, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.61 → 0.0.62

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Charts** | `0.0.62` | • Changed image tags to support multiarch manifest |

***

## Version 0.0.61

**Release Date:** October 9, 2025

### 🔗 Related Versions

- **Chart Version Bump:** 0.0.60 → 0.0.61

### 🔧 Service-Specific Changes

| Service | New Version | Key Updates |
|---------|-------------|-------------|
| **Sensor** | `v0.0.179` | • Added Trivy automated vulnerability scanning to CI/CD pipeline |
| **Operator** | `v0.0.23` | • Added Trivy automated vulnerability scanning to CI/CD pipeline |
| **Init Config** | `v1.0.4` | • Added Trivy automated vulnerability scanning to CI/CD pipeline |
| **Cluster Inventory** | `v0.1.3` | • Added Trivy automated vulnerability scanning to CI/CD pipeline |
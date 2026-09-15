{{/*
Expand the name of the chart.
*/}}
{{- define "zouz-operator-chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "zouz-operator-chart.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "zouz-operator-chart.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "zouz-operator-chart.labels" -}}
helm.sh/chart: {{ include "zouz-operator-chart.chart" . }}
{{ include "zouz-operator-chart.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.extraLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "zouz-operator-chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "zouz-operator-chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Release-scoped RBAC name prefixes (finding H3: identity/RBAC objects must be
unique per install so two releases never collide on a cluster-scoped name or
silently rebind a cluster-wide grant to another namespace's identity).

  nsPrefix      - for namespaced objects (ServiceAccount, Role, RoleBinding):
                  unique within the release namespace.
  clusterPrefix - for cluster-scoped objects (ClusterRole, ClusterRoleBinding,
                  PriorityClass): includes the namespace too, so even two
                  installs sharing a release name in different namespaces stay
                  distinct.

The sensor ServiceAccount and PriorityClass names are also passed to the
operator (SENSOR_SERVICE_ACCOUNT / SENSOR_PRIORITY_CLASS env) so the pods it
creates at runtime reference the same release-scoped names.
*/}}
{{- define "zouz-operator-chart.nsPrefix" -}}
{{- .Release.Name -}}
{{- end }}
{{- define "zouz-operator-chart.clusterPrefix" -}}
{{- printf "%s-%s" .Release.Namespace .Release.Name -}}
{{- end }}
{{- define "zouz-operator-chart.sensorServiceAccountName" -}}
{{- printf "%s-zouz-sensor" (include "zouz-operator-chart.nsPrefix" .) -}}
{{- end }}
{{- define "zouz-operator-chart.sensorPriorityClassName" -}}
{{- printf "%s-zouz-sensor-priority" (include "zouz-operator-chart.clusterPrefix" .) -}}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "zouz-operator-chart.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "zouz-operator-chart.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the sensor image
*/}}
{{- define "zouz-operator-chart.sensorImage" -}}
{{- if hasPrefix "@" .Values.image.sensor.tag }}
{{- printf "%s%s" .Values.image.sensor.repository .Values.image.sensor.tag }}
{{- else }}
{{- printf "%s:%s" .Values.image.sensor.repository .Values.image.sensor.tag }}
{{- end }}
{{- end }}

{{/*
Create the sensor repo
*/}}
{{- define "zouz-operator-chart.sensorImageRepo" -}}
{{- index (split ":" (include "zouz-operator-chart.sensorImage" .)) "_0" }}
{{- end }}

{{/*
Create the sensor tag
*/}}
{{- define "zouz-operator-chart.sensorImageTag" -}}
{{- index (split ":" (include "zouz-operator-chart.sensorImage" .)) "_1" }}
{{- end }}

{{/*
Create the operator image
*/}}
{{- define "zouz-operator-chart.operatorImage" -}}
{{- if hasPrefix "@" .Values.image.operator.tag }}
{{- printf "%s%s" .Values.image.operator.repository .Values.image.operator.tag }}
{{- else }}
{{- printf "%s:%s" .Values.image.operator.repository .Values.image.operator.tag }}
{{- end }}
{{- end }}

{{/*
RBAC rules for the cluster-wide read-only "object lister" ClusterRoles
(zouz-object-lister and attrb-object-lister). Verbs are always get/watch/list.

Two value shapes are supported:
  - objectlistpermissions.rules: an optional explicit allowlist of
    {apiGroups, resources} rules. When non-empty it takes precedence —
    setting it is the single opt-in needed to restrict the grant (see
    examples/values-minimal-rbac.yaml).
  - objectlistpermissions.apiGroups + .resources: the single-rule form used
    by the default (["*"]/["*"], the historical unrestricted grant).
*/}}
{{- define "zouz-operator-chart.objectListRules" -}}
{{- if .Values.objectlistpermissions.rules }}
{{- range .Values.objectlistpermissions.rules }}
- apiGroups:
    {{- range .apiGroups }}
    - {{ . | quote }}
    {{- end }}
  resources:
    {{- range .resources }}
    - {{ . | quote }}
    {{- end }}
  verbs: ["get", "watch", "list"]
{{- end }}
{{- else }}
- apiGroups:
    {{- range .Values.objectlistpermissions.apiGroups }}
    - {{ . | quote }}
    {{- end }}
  resources:
    {{- range .Values.objectlistpermissions.resources }}
    - {{ . | quote }}
    {{- end }}
  verbs: ["get", "watch", "list"]
{{- end }}
{{- end }}

{{/*
Effective Java auto-inject backend: resolves javaAutoInject.mode, turning "auto"
into "policy" on Kubernetes >= 1.36 (where MutatingAdmissionPolicy is GA) and
"webhook" below that. Returns "policy" | "webhook" | (any invalid value verbatim,
so validateJavaAutoInject can reject it).
*/}}
{{- define "zouz-operator-chart.javaInjectMode" -}}
{{- $mode := .Values.javaAutoInject.mode | default "auto" -}}
{{- if eq $mode "auto" -}}
{{- if semverCompare ">=1.36.0-0" .Capabilities.KubeVersion.Version -}}policy{{- else -}}webhook{{- end -}}
{{- else -}}
{{- $mode -}}
{{- end -}}
{{- end }}

{{/*
"true" when the webhook backend is the one actually being deployed, else empty
(so callers can use it directly in an `if`). Both 12_java_inject_webhook.yaml
and the webhook's NetworkPolicy in 11_networkpolicy.yaml gate on this, so they
can never disagree about whether the webhook pods exist.
*/}}
{{- define "zouz-operator-chart.javaInjectWebhookEnabled" -}}
{{- if and .Values.javaAutoInject.enabled (eq (include "zouz-operator-chart.javaInjectMode" .) "webhook") -}}true{{- end -}}
{{- end }}

{{/*
Container port of the webhook server. Lives here rather than inline so the
Deployment and the NetworkPolicy that opens the port cannot drift. Defaulted
explicitly (not via values.yaml) because `helm upgrade --reuse-values` does not
merge a newer chart's defaults, so javaAutoInject.webhook may be absent/partial.
*/}}
{{- define "zouz-operator-chart.javaInjectWebhookPort" -}}
{{- $wh := .Values.javaAutoInject.webhook | default dict -}}
{{- $wh.port | default 8443 -}}
{{- end }}

{{/*
Render a values map as a *typed* CEL object literal for MutatingAdmissionPolicy:
  Object.<path>{key: value, ...}
Call with: dict "path" "spec.initContainers.securityContext" "value" <map>.

The path is the field's schema path on the target resource, which is how CEL in
a MutatingAdmissionPolicy names object types (same convention as the
Object.spec.initContainers.volumeMounts literals in 10_java_inject_policy.yaml).
Nested maps recurse with the path extended, string/number lists become CEL lists,
strings are quoted and bools/numbers are emitted bare.

Only for schema *objects*. A map-typed field (resources.requests, nodeSelector,
labels…) is a CEL map, not an Object literal, and must be rendered as `toJson`-ish
`{"k": "v"}` instead — see javaInjectInitResources. Keys iterate in Go template
map order, i.e. sorted, so the rendered policy is stable across renders (an
unstable expression string would churn the policy object on every upgrade).
*/}}
{{- define "zouz-operator-chart.celObject" -}}
{{- $path := .path -}}
{{- $parts := list -}}
{{- range $k, $v := .value -}}
{{- if kindIs "map" $v -}}
{{- $parts = append $parts (printf "%s: %s" $k (include "zouz-operator-chart.celObject" (dict "path" (printf "%s.%s" $path $k) "value" $v))) -}}
{{- else if kindIs "slice" $v -}}
{{- $items := list -}}
{{- range $item := $v -}}
{{- $items = append $items (include "zouz-operator-chart.celScalar" $item) -}}
{{- end -}}
{{- $parts = append $parts (printf "%s: [%s]" $k (join ", " $items)) -}}
{{- else -}}
{{- $parts = append $parts (printf "%s: %s" $k (include "zouz-operator-chart.celScalar" $v)) -}}
{{- end -}}
{{- end -}}
{{- printf "Object.%s{%s}" $path (join ", " $parts) -}}
{{- end }}

{{/*
One scalar as a CEL literal: strings quoted, everything else (bool, int) bare.
*/}}
{{- define "zouz-operator-chart.celScalar" -}}
{{- if kindIs "string" . -}}
{{- . | quote -}}
{{- else -}}
{{- . -}}
{{- end -}}
{{- end }}

{{/*
javaAutoInject.resources as a CEL literal:
  Object.spec.initContainers.resources{requests: {"cpu": "100m"}, limits: {...}}
Empty when no resources are configured, so the caller can concatenate it
unconditionally.

requests/limits are map<string, Quantity>, i.e. CEL *maps* — celObject cannot
render them (an Object literal for a map-typed field fails the policy's CEL type
check at install time). Quantities are stringified explicitly: an unquoted
`cpu: 1` in values.yaml is an int in YAML, and an int where the schema wants a
Quantity would likewise fail the type check.
*/}}
{{- define "zouz-operator-chart.javaInjectInitResources" -}}
{{- $res := .Values.javaAutoInject.resources -}}
{{- if $res -}}
{{- $fields := list -}}
{{- range $f := list "limits" "requests" -}}
{{- with index $res $f -}}
{{- $kv := list -}}
{{- range $k, $v := . -}}
{{- $kv = append $kv (printf "%q: %q" $k (toString $v)) -}}
{{- end -}}
{{- $fields = append $fields (printf "%s: {%s}" $f (join ", " $kv)) -}}
{{- end -}}
{{- end -}}
{{- if $fields -}}
{{- printf "Object.spec.initContainers.resources{%s}" (join ", " $fields) -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Guard rails for Java auto-inject.

Fails the render (rather than silently mis-deploying) when javaAutoInject is
enabled but its preconditions are not met:
  1. The load-time -javaagent injection and the sensor's runtime Java
     instrumentation must never both be active, otherwise workloads get
     instrumented twice. Enabling injection therefore requires
     sensorDisableAutoJavaInstrumentation=true.
  2. The effective backend must be supported by the cluster version:
       - policy  : MutatingAdmissionPolicy is GA only in Kubernetes 1.36+.
       - webhook : admissionregistration.k8s.io/v1 webhooks are GA since 1.16.
     "auto" picks a supported backend automatically; an explicit mode that the
     cluster cannot support fails here with a clear message.
*/}}
{{- define "zouz-operator-chart.validateJavaAutoInject" -}}
{{- if .Values.javaAutoInject.enabled }}
{{- if not .Values.sensorDisableAutoJavaInstrumentation }}
{{- fail "javaAutoInject.enabled=true requires sensorDisableAutoJavaInstrumentation=true: the load-time -javaagent injection and the sensor's runtime Java instrumentation must not run together (double instrumentation). Set sensorDisableAutoJavaInstrumentation: true." }}
{{- end }}
{{- $mode := include "zouz-operator-chart.javaInjectMode" . }}
{{- if eq $mode "policy" }}
{{- if not (semverCompare ">=1.36.0-0" .Capabilities.KubeVersion.Version) }}
{{- fail (printf "javaAutoInject mode=policy requires Kubernetes >= 1.36 (MutatingAdmissionPolicy is GA there). Cluster reports %s. Set javaAutoInject.mode=webhook (or auto) or upgrade the cluster." .Capabilities.KubeVersion.Version) }}
{{- end }}
{{- else if eq $mode "webhook" }}
{{- if not (semverCompare ">=1.16.0-0" .Capabilities.KubeVersion.Version) }}
{{- fail (printf "javaAutoInject mode=webhook requires Kubernetes >= 1.16 (admissionregistration.k8s.io/v1). Cluster reports %s." .Capabilities.KubeVersion.Version) }}
{{- end }}
{{/* Reject an unknown antiAffinity rather than silently falling back to one of
     the valid modes — a typo here would quietly drop the spreading guarantee. */}}
{{- $aa := (.Values.javaAutoInject.webhook | default dict).antiAffinity | default "preferred" }}
{{- if not (has $aa (list "preferred" "required" "none")) }}
{{- fail (printf "javaAutoInject.webhook.antiAffinity must be one of preferred|required|none, got %q." $aa) }}
{{- end }}
{{- else }}
{{- fail (printf "javaAutoInject.mode must be one of auto|policy|webhook, got %q." .Values.javaAutoInject.mode) }}
{{- end }}
{{- end }}
{{- end }}
{{/*
Feature-flag env for the operator and the sensor DaemonSets.

Operator 0.0.32+ / sensor 0.0.291+ evaluate flags against Unleash. Both compile
in the client token and the default Edge URL, so this renders nothing unless
featureFlags.url is set — an unset value keeps every pod spec byte-identical to
the pre-Unleash chart and, on the operator side, keeps IsUnleashURLOverridden()
false so the sensor pods it creates at runtime are not churned either.

Only the URL is ever rendered. The token is deliberately never put in a pod
spec, where anyone with `get pod` could read it; Edge validates the compiled-in
token upstream, so a self-hosted Edge needs no token change.
*/}}
{{- define "zouz-operator-chart.featureFlagsEnv" -}}
{{- with (.Values.featureFlags | default dict).url -}}
- name: UNLEASH_URL
  value: {{ . | quote }}
{{- end }}
{{- end }}

{{/*
Removed values, rejected loudly rather than ignored.

launchdarklyproxy deployed an in-cluster LaunchDarkly relay and pointed the
operator and sensors at it with LD_PROXY_ADDRESS. Neither binary reads
LaunchDarkly any more (operator 0.0.32, sensor 0.0.291), so the relay is dead
weight — but silently dropping it would move a cluster that ran it precisely to
keep flag traffic in-cluster back onto egress to ff.app.attrb.io without
anyone noticing. Fail instead, and name the replacement.
*/}}
{{- define "zouz-operator-chart.validateRemovedValues" -}}
{{- if (.Values.launchdarklyproxy | default dict).enabled }}
{{- fail "launchdarklyproxy has been removed in chart 0.0.98: operator 0.0.32 and sensor 0.0.291 use Unleash, not LaunchDarkly, so the ld-relay proxy is never contacted. Drop the launchdarklyproxy values; to keep feature-flag traffic in cluster, run an Unleash Edge and point featureFlags.url at its client API base instead." }}
{{- end }}
{{- end }}

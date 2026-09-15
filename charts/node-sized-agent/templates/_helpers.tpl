{{- define "node-sized-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "node-sized-agent.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "node-sized-agent.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "node-sized-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "node-sized-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "node-sized-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "node-sized-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "node-sized-agent.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Validate tiers and return the sorted union of all tier instance types.
Fails rendering on: a tier without a name, a tier without instanceTypes,
duplicate tier names, or an instance type listed in more than one tier
(which would schedule two agent pods on the same node).
*/}}
{{- define "node-sized-agent.allInstanceTypes" -}}
{{- $seen := dict -}}
{{- $names := dict -}}
{{- range .Values.nodeSizing.tiers -}}
  {{- if not .name -}}{{- fail "nodeSizing.tiers[]: every tier needs a name" -}}{{- end -}}
  {{- if hasKey $names .name -}}{{- fail (printf "nodeSizing.tiers: duplicate tier name %q" .name) -}}{{- end -}}
  {{- $_ := set $names .name true -}}
  {{- if not .instanceTypes -}}{{- fail (printf "nodeSizing.tiers[%s]: instanceTypes must not be empty" .name) -}}{{- end -}}
  {{- $tier := .name -}}
  {{- range .instanceTypes -}}
    {{- if hasKey $seen . -}}
      {{- fail (printf "instance type %q is listed in tiers %q and %q; a node may match only one tier" . (get $seen .) $tier) -}}
    {{- end -}}
    {{- $_ := set $seen . $tier -}}
  {{- end -}}
{{- end -}}
{{- keys $seen | sortAlpha | toJson -}}
{{- end -}}

{{/*
Render one DaemonSet. Expects a dict with:
  root      - the chart root context
  suffix    - "" for the single/unsized DaemonSet, otherwise the tier name
  tier      - label value written to the pods (e.g. small / default / fixed)
  resources - container resources for this DaemonSet
  affinityExpressions - list of node-affinity matchExpressions (may be empty)
*/}}
{{- define "node-sized-agent.daemonset" -}}
{{- $root := .root -}}
{{- $fullname := include "node-sized-agent.fullname" $root -}}
{{- $name := ternary $fullname (printf "%s-%s" $fullname .suffix | trunc 63 | trimSuffix "-") (eq .suffix "") -}}
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: {{ $name }}
  labels:
    {{- include "node-sized-agent.labels" $root | nindent 4 }}
    node-sized-agent/tier: {{ .tier }}
spec:
  selector:
    matchLabels:
      {{- include "node-sized-agent.selectorLabels" $root | nindent 6 }}
      node-sized-agent/tier: {{ .tier }}
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
  template:
    metadata:
      labels:
        {{- include "node-sized-agent.selectorLabels" $root | nindent 8 }}
        node-sized-agent/tier: {{ .tier }}
        {{- with $root.Values.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- with $root.Values.podAnnotations }}
      annotations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
    spec:
      serviceAccountName: {{ include "node-sized-agent.serviceAccountName" $root }}
      terminationGracePeriodSeconds: {{ $root.Values.terminationGracePeriodSeconds }}
      {{- with $root.Values.priorityClassName }}
      priorityClassName: {{ . }}
      {{- end }}
      {{- with $root.Values.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $root.Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- if .affinityExpressions }}
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  {{- toYaml .affinityExpressions | nindent 18 }}
      {{- end }}
      containers:
        - name: agent
          image: "{{ $root.Values.image.repository }}:{{ $root.Values.image.tag }}"
          imagePullPolicy: {{ $root.Values.image.pullPolicy }}
          command: ["/bin/sh", "-c"]
          args:
            - |
              trap 'echo "tier=$TIER node=$NODE_NAME received SIGTERM, exiting"; exit 0' TERM INT
              while true; do
                echo "tier=$TIER node=$NODE_NAME pod=$POD_NAME cpu_request=${CPU_REQUEST}m mem_request=${MEM_REQUEST}Mi cpu_limit=${CPU_LIMIT}m mem_limit=${MEM_LIMIT}Mi"
                sleep {{ $root.Values.logIntervalSeconds }} &
                wait $!
              done
          env:
            - name: TIER
              value: {{ .tier | quote }}
            - name: NODE_NAME
              valueFrom:
                fieldRef:
                  fieldPath: spec.nodeName
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: CPU_REQUEST
              valueFrom:
                resourceFieldRef:
                  containerName: agent
                  resource: requests.cpu
                  divisor: 1m
            - name: MEM_REQUEST
              valueFrom:
                resourceFieldRef:
                  containerName: agent
                  resource: requests.memory
                  divisor: 1Mi
            - name: CPU_LIMIT
              valueFrom:
                resourceFieldRef:
                  containerName: agent
                  resource: limits.cpu
                  divisor: 1m
            - name: MEM_LIMIT
              valueFrom:
                resourceFieldRef:
                  containerName: agent
                  resource: limits.memory
                  divisor: 1Mi
          resources:
            {{- toYaml .resources | nindent 12 }}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            runAsUser: 65534
            capabilities:
              drop: ["ALL"]
{{- end -}}

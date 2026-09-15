{{/*
Renders one sensor DaemonSet for the non-Karpenter DaemonSet mode. Called with:
  root        chart root context
  name        DaemonSet name (the release name for the catch-all, <release>-<tier> for a tier)
  tier        tier name, or "" for the plain single DaemonSet (no tiers configured)
  exprs       extra nodeAffinity matchExpressions (tier In [...] / catch-all NotIn [...]); empty = legacy affinity
  res         resources in the sensorresources shape: cpu.request/limit, memory.request/limit
  tolerations tolerations list (may be empty)
  exporturl   OTEL export address

Selector note: tier DaemonSets add attrb.io/sensor-tier to their selector. The
catch-all keeps the historical name and selector so enabling tiers on a live
install is a rolling update of the existing DaemonSet rather than a delete and
recreate of every sensor pod. Its selector therefore also matches tier pods;
that is safe because the DaemonSet controller only manages pods whose
ownerReference points at it, and the NotIn affinity keeps it off tier nodes.
*/}}
{{- define "zouz-operator-chart.sensorDaemonSet" -}}
{{- $r := .root -}}
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: {{ .name }}
  namespace: {{ $r.Release.Namespace }}
  labels:
    app: {{ $r.Release.Name }}
    {{- include "zouz-operator-chart.labels" $r | nindent 4 }}
    {{- if .tier }}
    attrb.io/sensor-tier: {{ .tier | quote }}
    {{- end }}
spec:
  selector: 
    matchLabels:
      {{- include "zouz-operator-chart.selectorLabels" $r | nindent 6 }}
      {{- if and .tier (ne .tier "default") }}
      attrb.io/sensor-tier: {{ .tier | quote }}
      {{- end }}
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: {{ $r.Values.rollout_max_unavailable }}
  template:
    metadata:
      labels:
        app: {{ $r.Release.Name }}
        attrb.io/inventory-client: "true"
        {{- include "zouz-operator-chart.labels" $r | nindent 8 }}
        {{- if .tier }}
        attrb.io/sensor-tier: {{ .tier | quote }}
        {{- end }}
    spec:
      tolerations: 
      {{- if .tolerations }}
      {{- .tolerations | toYaml | nindent 8 }}
      {{- end }}
      nodeSelector:
      {{- if $r.Values.daemonset.selectors.nodeSelector }}
      {{- $r.Values.daemonset.selectors.nodeSelector | toYaml | nindent 8 }}
      {{- end }}
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            {{- if .exprs }}
            - matchExpressions:
              - key: kubernetes.io/os
                operator: In
                values:
                - linux
              - key: kubernetes.io/arch
                operator: In
                values:
                - amd64
                - arm64
              {{- range .exprs }}
              - key: {{ .key }}
                operator: {{ .operator }}
                values:
                {{- range .values }}
                - {{ . | quote }}
                {{- end }}
              {{- end }}
            {{- else }}
            - matchExpressions:
              - key: kubernetes.io/os
                operator: In
                values:
                - linux
            - matchExpressions:
              - key: kubernetes.io/arch
                operator: In
                values:
                - amd64
                - arm64
            {{- end }}

      serviceAccountName: "{{ include "zouz-operator-chart.sensorServiceAccountName" $r }}"

      {{- if $r.Values.priorityclass.existingName }}
      priorityClassName: {{ $r.Values.priorityclass.existingName }}
      {{- else if $r.Values.priorityclass.enabled }}
      priorityClassName: {{ include "zouz-operator-chart.sensorPriorityClassName" $r }}
      {{- end }}

      hostPID: true
      hostNetwork: {{ $r.Values.sensorHostNetwork }}
      dnsPolicy: {{ $r.Values.sensorDNSPolicy }}
      volumes:
      {{- include "zouz-operator-chart.sensorVolumes" $r | nindent 6 }}

      containers:
      - name: zprobe
        image: "{{ include "zouz-operator-chart.sensorImage" $r }}"
        imagePullPolicy: IfNotPresent

        command: [ "/app/zprobe" ]
        args:
        - "-rootfs=/hostfs"
        - "-procfs={{ include "zouz-operator-chart.sensorProcfsPath" $r }}"
        - "-no-otel-collector"
        - "-incluster"
        {{- if and (eq $r.Values.token_is_secret false) (eq $r.Values.initconfig.enabled false) }}
        - "-bearer-token={{ $r.Values.token }}"
        {{- end }}
        - "-oteladdr={{ .exporturl }}"

        env:
        - name: DISABLE_UTILIZATION_METRICS
          value: "TRUE"
        - name: K8S_CURRENT_NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: MEMORY_LIMIT_BYTES
          valueFrom:
            resourceFieldRef:
              resource: limits.memory
              divisor: "1"
        - name: USE_APP_TAGS
          value: "{{ $r.Values.use_app_name_label }}"
        - name: CONFIG_ENDPOINT
          value: "{{ $r.Values.config_endpoint }}"
        {{- if $r.Values.sensorDisableAutoJavaInstrumentation }}
        - name: JAVA_INSTRUMENTATION_DISABLED
          value: "TRUE"
        {{- end }}
        {{- if ($r.Values.featureFlags | default dict).url }}
        {{- include "zouz-operator-chart.featureFlagsEnv" $r | nindent 8 }}
        {{- end }}
        {{- if eq $r.Values.initconfig.enabled true }}
        - name: API_KEY
          valueFrom:
            secretKeyRef:
              name: {{ $r.Values.initconfig.secretName | quote }}
              key: token
        {{- else if eq $r.Values.token_is_secret true }}
        - name: API_KEY
          valueFrom:
            secretKeyRef:
              name: {{ $r.Values.token }}
              key: {{ $r.Values.token }}
        {{- end }}
        {{- if eq $r.Values.initconfig.enabled false }}
        - name: CLUSTER_NAME
          value: {{ $r.Values.cluster_name | quote }}
        {{- else }}
        - name: CLUSTER_NAME
          valueFrom:
            secretKeyRef:
              name: {{ $r.Values.initconfig.secretName | quote }}
              key: clustername
        {{- end }}
        {{- if eq $r.Values.clusterinventory.enabled true }}
        - name: IVP_SERVER_ADDRESS
          value: "attrb-cluster-inventory.{{ $r.Release.Namespace }}.svc.cluster.local:9191"
        {{- end }}
        {{- with $r.Values.extraEnv }}
        {{- toYaml . | nindent 8 }}
        {{- end }}

        resources:
          requests:
            memory: {{ .res.memory.request }}
            cpu: {{ .res.cpu.request }}
          limits:
            {{if .res.memory.limit }}
            memory: {{ .res.memory.limit }}
            {{end}}
            {{if .res.cpu.limit }}
            cpu: {{ .res.cpu.limit }}
            {{end}}

        securityContext:
          {{- include "zouz-operator-chart.sensorSecurityContext" $r | nindent 10 }}
        volumeMounts:
          {{- include "zouz-operator-chart.sensorVolumeMounts" $r | nindent 10 }}
{{- end -}}

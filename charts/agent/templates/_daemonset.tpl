{{- /*
Renders one DaemonSet. Called with a dict:
  root        the chart root context
  name        suffix for the resource name and component label
  label       node label key to match on
  operator    In (profile) or NotIn (catch-all)
  values      list of machine types for the expression
  resources   final resources block for the container
  tolerations list of tolerations (may be empty)
*/ -}}
{{- define "agent.daemonset" -}}
{{- $ctx := .root -}}
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: {{ $ctx.Release.Name }}-agent-{{ .name }}
  labels:
    app.kubernetes.io/name: agent
    app.kubernetes.io/instance: {{ $ctx.Release.Name }}
    app.kubernetes.io/component: {{ .name }}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: agent
      app.kubernetes.io/instance: {{ $ctx.Release.Name }}
      app.kubernetes.io/component: {{ .name }}
  updateStrategy:
    type: RollingUpdate
  template:
    metadata:
      labels:
        app.kubernetes.io/name: agent
        app.kubernetes.io/instance: {{ $ctx.Release.Name }}
        app.kubernetes.io/component: {{ .name }}
    spec:
      {{- /* NotIn with an empty values list is rejected by the API server.
             No profiles defined means the catch-all should run everywhere,
             so omit the affinity in that case. */ -}}
      {{- if .values }}
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: {{ .label }}
                    operator: {{ .operator }}
                    values:
                      {{- toYaml .values | nindent 22 }}
      {{- end }}
      {{- with .tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      containers:
        - name: agent
          image: "{{ $ctx.Values.image.repository }}:{{ $ctx.Values.image.tag | default $ctx.Chart.AppVersion }}"
          resources:
            {{- toYaml .resources | nindent 12 }}
{{- end -}}

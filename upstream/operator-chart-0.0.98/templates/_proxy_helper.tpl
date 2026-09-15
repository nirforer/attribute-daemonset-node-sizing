{{- define "zouz-operator-chart.proxyConfig" -}}
{{- $limit_mem_config := sub .Values.otelproxy.resources.memory.limit_mib 100 -}}
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: ${env:MY_POD_IP}:4317
        tls:
          cert_file: /etc/otelcol/cert.pem
          key_file: /etc/otelcol/cert.key
processors:
  batch:
  memory_limiter:
    check_interval: 1s
    limit_mib: {{ $limit_mem_config }}
  {{- with .Values.otelproxy.extraProcessorsDefinitions }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
exporters:
  otlp:
    endpoint: {{ .Values.export_url }}
    headers:
      Authorization: "Bearer ${env:API_KEY}"
  {{- with .Values.otelproxy.extraExportersDefinitions }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors:
      - memory_limiter
      {{- range .Values.otelproxy.extraProcessors.traces }}
      - {{ . }}
      {{- end }}
      exporters:
      - otlp
      {{- range .Values.otelproxy.extraExporters.traces }}
      - {{ . }}
      {{- end }}
    metrics:
      receivers: [otlp]
      processors:
      - memory_limiter
      {{- range .Values.otelproxy.extraProcessors.metrics }}
      - {{ . }}
      {{- end }}
      exporters:
      - otlp
      {{- range .Values.otelproxy.extraExporters.metrics }}
      - {{ . }}
      {{- end }}
    logs:
      receivers: [otlp]
      processors:
      - memory_limiter
      {{- range .Values.otelproxy.extraProcessors.logs }}
      - {{ . }}
      {{- end }}
      exporters:
      - otlp
      {{- range .Values.otelproxy.extraExporters.logs }}
      - {{ . }}
      {{- end }}
{{- end }}
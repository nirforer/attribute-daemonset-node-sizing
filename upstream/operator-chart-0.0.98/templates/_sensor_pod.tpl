{{/*
Sensor procfs path passed to -procfs.

When reducePermissions is true the sensor uses the in-container /proc — this
works because hostPID is true, so /proc inside the pod already exposes every
host PID. Java instrumentation writes via /proc/<pid>/root/tmp/... (a procfs
magic link), which crosses into the target container's mount namespace and
is gated by CAP_SYS_PTRACE rather than the read-only flag on /hostfs.

When reducePermissions is false we keep the legacy behaviour of pointing at
the host's procfs via /hostfs/proc.
*/}}
{{- define "zouz-operator-chart.sensorProcfsPath" -}}
{{- if .Values.reducePermissions -}}
/proc
{{- else -}}
/hostfs/proc
{{- end -}}
{{- end }}

{{/*
Pod-level volumes for the sensor.

reducePermissions=true narrows the host /sys mount to /sys/kernel/tracing
(kprobe_events / uprobe_events writes from cilium/ebpf's tracefs fallback).
/sys/kernel/debug is mounted as a secondary fallback for kernels that only
expose tracefs under debugfs.

The host /sys/fs/cgroup mount is intentionally NOT mounted: the only runtime
consumers (GetLimit, NewSelfCG, OOMStatString in monitor.go) are read-only
self-observation paths that tolerate failure (errors logged, sensor still
starts). The write-heavy NewCG path in zbase/cg.go is unreferenced in
production. The container runtime's auto-mounted /sys/fs/cgroup is used if
present; otherwise self memory-limit detection and OOM diagnostics degrade
gracefully.

reducePermissions=false retains the original wide /sys host mount.
*/}}
{{- define "zouz-operator-chart.sensorVolumes" -}}
- name: hostfs
  hostPath:
    path: /
{{- if .Values.reducePermissions }}
- name: tracefs
  hostPath:
    path: /sys/kernel/tracing
- name: debugfs
  hostPath:
    path: /sys/kernel/debug
{{- else }}
- name: sys
  hostPath:
    path: /sys/
{{- end }}
{{- end }}

{{/*
Container volumeMounts for the sensor.

When reducePermissions is true /hostfs is mounted read-only — every consumer
of -rootfs only reads (binary scanning, kubeconfig, cloud metadata, docker
socket). Java instrumentation writes via the procfs magic link, which is not
gated by the /hostfs mount flag.
*/}}
{{- define "zouz-operator-chart.sensorVolumeMounts" -}}
- name: hostfs
  mountPath: /hostfs
{{- if .Values.reducePermissions }}
  readOnly: true
- name: tracefs
  mountPath: /sys/kernel/tracing
- name: debugfs
  mountPath: /sys/kernel/debug
{{- else }}
- name: sys
  mountPath: /sys/
{{- end }}
{{- end }}

{{/*
Container securityContext for the sensor.

reducePermissions=true drops privileged mode in favour of the minimum cap
set the eBPF sensor actually needs on a 5.10+ kernel:

  - SYS_ADMIN     The keystone cap. Required because the kernel's uprobe
                  registration path (trace_uprobe.c) was never migrated to
                  honour CAP_PERFMON during the 5.8 cap-split, so SSL /
                  Go-TLS / gRPC TSI / Node / Rustls / .NET uprobes still
                  need it. SYS_ADMIN is also a superset of CAP_BPF and
                  CAP_PERFMON (both kernel checks are `cap_x || SYS_ADMIN`),
                  so we don't list those redundantly.

  - SYS_PTRACE    Required for traversing /proc/<host-pid>/{root,ns/*}
                  magic links into other containers' mount and PID
                  namespaces — used by kubelet kubeconfig detection in
                  systemdetector/eks_gke_detector.go and by workload
                  inspection generally. __ptrace_may_access() checks this
                  cap specifically; SYS_ADMIN does NOT substitute.

  - SYS_RESOURCE  Required on kernel 5.10 to raise RLIMIT_MEMLOCK for BPF
                  map memory (rlimit.RemoveMemlock in loader.go / main.go).
                  On 5.11+ BPF accounting moved to memcg and this becomes
                  a no-op, but min supported kernel is 5.10 so we keep it.
                  do_prlimit() checks this cap; SYS_ADMIN does NOT
                  substitute.

  - DAC_OVERRIDE  Required so the Java instrumentor can write jattach +
                  jagent.jar into the target container's /tmp via
                  /proc/<pid>/root/tmp when that container runs as a
                  non-root UID (e.g. runAsUser: 1000). Without it the
                  write fails with EACCES because uid 0 in the sensor
                  does not match the target's tmpfs ownership and Linux
                  has no implicit uid-0 DAC bypass without this cap.

Intentionally NOT added:
  - BPF, PERFMON         Redundant under SYS_ADMIN.
  - NET_ADMIN, NET_RAW   Sensor has no AF_PACKET / SOCK_RAW sockets and
                         no networking BPF program types (socket_filter,
                         XDP, TC, sk_skb, cgroup_*). The "Configurable
                         Socket Filter" feature is a userland port
                         allow-list, not a BPF SOCKET_FILTER program.
  - SYS_MODULE           Sensor doesn't load kernel modules.
  - DAC_READ_SEARCH      Redundant under DAC_OVERRIDE (override is a
                         strict superset).

AppArmor and SELinux are set to Unconfined / spc_t: the default container
LSM profiles (containerd's runtime/default and SELinux container_t) block
ptrace of host processes, which prevents reads through /proc/<host-pid>/
root magic links. privileged: true used to mask this because K8s auto-
swaps to unconfined profiles for privileged pods; with privileged: false
we have to opt back in explicitly. CAP_SYS_PTRACE is checked separately
from the LSM gate — both have to allow the access.
*/}}
{{- define "zouz-operator-chart.sensorSecurityContext" -}}
{{- if .Values.reducePermissions -}}
privileged: false
allowPrivilegeEscalation: true
appArmorProfile:
  type: Unconfined
seLinuxOptions:
  type: spc_t
capabilities:
  drop:
    - ALL
  add:
    - SYS_ADMIN
    - SYS_PTRACE
    - SYS_RESOURCE
    - DAC_OVERRIDE
{{- else -}}
privileged: true
capabilities:
  add: ["SYS_ADMIN"]
{{- end }}
seccompProfile:
  type: RuntimeDefault
{{- end }}

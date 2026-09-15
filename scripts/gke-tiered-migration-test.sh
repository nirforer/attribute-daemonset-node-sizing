#!/usr/bin/env bash
# Live test of charts/operator-chart-tiered on a real cluster:
#   1. install in single DaemonSet mode (no tiers)
#   2. upgrade to tiered mode -> the existing DaemonSet must be updated in place (same UID), tier DaemonSets appear
#   3. label a node with the tier label -> its sensor pod moves from the catch-all to the tier DaemonSet
#   4. remove the label -> it moves back
# Sensor/operator images are replaced with busybox: the pods crash-loop but scheduling and resources are real.
# Usage: scripts/gke-tiered-migration-test.sh <kube-context> [namespace]
set -euo pipefail
CTX="${1:?kube context}"; NS="${2:-attrb-tiered-test}"
C="$(cd "$(dirname "$0")/.." && pwd)/charts/operator-chart-tiered"
EX="$(cd "$(dirname "$0")/.." && pwd)/examples/attribute-tiers-karpenter.yaml"
K=(kubectl --context "$CTX"); H=(helm --kube-context "$CTX")
COMMON=(-f "$C/ci/ci-values.yaml" --set daemonset.enabled=true
        --set image.sensor.repository=busybox --set image.sensor.tag=1.36
        --set image.operator.repository=busybox --set image.operator.tag=1.36)
NODE=$("${K[@]}" get nodes -o name | head -1 | sed 's|node/||')
show_ds()  { "${K[@]}" -n "$NS" get ds -o custom-columns='DS:.metadata.name,GEN:.metadata.generation,DESIRED:.status.desiredNumberScheduled,CPU_REQ:.spec.template.spec.containers[0].resources.requests.cpu,MEM_REQ:.spec.template.spec.containers[0].resources.requests.memory,CPU_LIM:.spec.template.spec.containers[0].resources.limits.cpu'; }
show_pods(){ "${K[@]}" -n "$NS" get pods -l attrb.io/inventory-client=true -o custom-columns='POD:.metadata.name,TIER:.metadata.labels.attrb\.io/sensor-tier,NODE:.spec.nodeName,CPU_REQ:.spec.containers[0].resources.requests.cpu,MEM_REQ:.spec.containers[0].resources.requests.memory,DELETING:.metadata.deletionTimestamp'; }
cleanup(){ echo; echo "== cleanup"; "${K[@]}" label node "$NODE" attrb.io/sensor-size- >/dev/null 2>&1 || true; "${H[@]}" uninstall t -n "$NS" >/dev/null 2>&1 || true; "${K[@]}" delete ns "$NS" --wait=false >/dev/null 2>&1 || true; echo "release, namespace and node label removed"; }
trap cleanup EXIT

echo "== step 1: install, single DaemonSet mode (node: $NODE)"
"${H[@]}" upgrade --install t "$C" -n "$NS" --create-namespace "${COMMON[@]}" >/dev/null
sleep 5; show_ds
UID1=$("${K[@]}" -n "$NS" get ds t -o jsonpath='{.metadata.uid}')

echo; echo "== step 2: upgrade to tiered mode (tiers: large, gpu)"
"${H[@]}" upgrade t "$C" -n "$NS" "${COMMON[@]}" -f "$EX" >/dev/null
sleep 8; show_ds
UID2=$("${K[@]}" -n "$NS" get ds t -o jsonpath='{.metadata.uid}')
if [[ -n "$UID1" && "$UID1" == "$UID2" ]]; then echo "RESULT: catch-all DaemonSet 't' kept UID $UID1 -> updated in place (rolling update), not recreated"; else echo "RESULT: FAIL - catch-all DaemonSet recreated or missing ($UID1 -> $UID2)"; exit 1; fi
show_pods

echo; echo "== step 3: label node attrb.io/sensor-size=large"
"${K[@]}" label node "$NODE" attrb.io/sensor-size=large --overwrite >/dev/null
for i in $(seq 1 12); do sleep 5; n=$("${K[@]}" -n "$NS" get pods -l attrb.io/sensor-tier=large --field-selector=status.phase!=Succeeded -o name | wc -l | tr -d ' '); [[ "$n" -ge 1 ]] && break; done
show_pods

echo; echo "== step 4: remove the label"
"${K[@]}" label node "$NODE" attrb.io/sensor-size- >/dev/null
for i in $(seq 1 12); do sleep 5; n=$("${K[@]}" -n "$NS" get pods -l attrb.io/sensor-tier=default -o name | wc -l | tr -d ' '); [[ "$n" -ge 1 ]] && break; done
show_pods

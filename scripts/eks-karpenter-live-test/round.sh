#!/bin/bash
# usage: round.sh <daemonset-config.yaml> <label>
set -u
S=/private/tmp/claude-501/-Users-nirforer-Desktop/5b3f2ca4-9733-449e-9389-ae81a036366d/scratchpad
K=$S/kp; CFG=$1; LABEL=$2; OUT=$S/karpenter-eks/results/$LABEL.txt
{
VER=$($K -n karpenter get deploy karpenter -o jsonpath='{.spec.template.spec.containers[0].image}' | sed -E 's/.*:([0-9]+\.[0-9]+\.[0-9]+)@.*/\1/')
echo "##### $LABEL   karpenter=$VER   $(date -u +%FT%TZ)"
$K apply -f "$CFG" | grep -i daemonset
sleep 8
echo "--- sensor DaemonSets in attrb-karp-test (before launch):"
$K -n attrb-karp-test get ds -o custom-columns='NAME:.metadata.name,DESIRED:.status.desiredNumberScheduled,READY:.status.numberReady,CPU_REQ:.spec.template.spec.containers[0].resources.requests.cpu,MEM_REQ:.spec.template.spec.containers[0].resources.requests.memory'
T0=$(date -u +%FT%TZ)
$K -n attrb-karp-test scale deploy probe --replicas=1 >/dev/null
echo "--- probe scaled to 1 at $T0; waiting for a NodeClaim"
n=""; for i in $(seq 1 45); do n=$($K get nodeclaim -o name 2>/dev/null | head -1); [ -n "$n" ] && break; sleep 2; done
echo "nodeclaim: ${n:-NONE}"
$K get nodeclaim -o custom-columns='NAME:.metadata.name,TYPE:.metadata.labels.node\.kubernetes\.io/instance-type,VCPU:.metadata.labels.karpenter\.k8s\.aws/instance-cpu,PLANNED_CPU:.spec.resources.requests.cpu,PLANNED_MEM:.spec.resources.requests.memory,PLANNED_PODS:.spec.resources.requests.pods'
sleep 5
echo "--- karpenter log lines (provisioning) since $T0:"
$K -n karpenter logs deploy/karpenter --since-time="$T0" | grep -E '"message":"[^"]*(nodeclaim|provisionable)' | python3 -c '
import sys, json
for l in sys.stdin:
    try: d = json.loads(l)
    except Exception: continue
    keys = ["message", "NodeClaim", "requests", "instance-types", "instance-type", "capacity-type", "zone", "allocatable", "Pods", "pods"]
    print({k: d[k] for k in keys if k in d})'
echo "--- waiting for the node to be Ready (up to 5 min)"
node=""; for i in $(seq 1 60); do node=$($K get nodes -l karpenter.sh/nodepool=sensor-test -o name 2>/dev/null | head -1); if [ -n "$node" ] && [ "$($K get $node -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = "True" ]; then break; fi; sleep 5; done
echo "node: ${node:-NONE}"
if [ -n "$node" ]; then
  $K get $node -L node.kubernetes.io/instance-type,karpenter.k8s.aws/instance-cpu,karpenter.k8s.aws/instance-memory --no-headers
  sleep 20
  echo "--- pods on the new node:"
  $K get pods -A -o wide --field-selector spec.nodeName=${node#node/} --no-headers | awk '{printf "%-16s %-44s %s\n", $1, $2, $4}'
  echo "--- sensor test pods on the node, requests:"
  $K get pods -n attrb-karp-test -o json --field-selector spec.nodeName=${node#node/} | python3 -c '
import sys, json
for p in json.load(sys.stdin)["items"]:
    print(" ", p["metadata"]["name"], p["spec"]["containers"][0]["resources"].get("requests", {}))'
fi
echo "##### end $LABEL $(date -u +%FT%TZ)"
} 2>&1 | tee "$OUT"

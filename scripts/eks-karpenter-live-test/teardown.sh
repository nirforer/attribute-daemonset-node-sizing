#!/bin/bash
# usage: teardown.sh <daemonset-config.yaml>   (scale probe to 0, remove the Karpenter node, delete the sensor DaemonSets)
S=/private/tmp/claude-501/-Users-nirforer-Desktop/5b3f2ca4-9733-449e-9389-ae81a036366d/scratchpad
K=$S/kp
$K -n attrb-karp-test scale deploy probe --replicas=0 >/dev/null
$K delete nodeclaim --all --wait=false 2>/dev/null
for i in $(seq 1 48); do [ -z "$($K get nodeclaim -o name 2>/dev/null)" ] && break; sleep 5; done
echo "nodeclaims left: $($K get nodeclaim -o name 2>/dev/null | wc -l | tr -d ' ')   karpenter nodes left: $($K get nodes -l karpenter.sh/nodepool -o name | wc -l | tr -d ' ')"
$K delete -f "$1" --ignore-not-found 2>&1 | grep -ci daemonset | sed 's/^/daemonsets deleted: /'

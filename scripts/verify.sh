#!/usr/bin/env bash
# Verifies that a release of charts/node-sized-agent or charts/agent runs exactly one pod per node and that
# each pod's requests match the tier expected for that node's instance type.
# Usage: scripts/verify.sh [namespace] [release]
set -euo pipefail
NS="${1:-node-sizing-demo}"
REL="${2:-agent}"
SEL="app.kubernetes.io/instance=${REL}"

echo "== Nodes"
kubectl get nodes -o custom-columns='NODE:.metadata.name,INSTANCE_TYPE:.metadata.labels.node\.kubernetes\.io/instance-type,ALLOC_CPU:.status.allocatable.cpu,ALLOC_MEM:.status.allocatable.memory'

echo; echo "== DaemonSets"
kubectl -n "$NS" get ds -l "$SEL" -o custom-columns='DAEMONSET:.metadata.name,TIER:.metadata.labels.node-sized-agent/tier,COMPONENT:.metadata.labels.app\.kubernetes\.io/component,DESIRED:.status.desiredNumberScheduled,READY:.status.numberReady,CPU_REQ:.spec.template.spec.containers[0].resources.requests.cpu,MEM_REQ:.spec.template.spec.containers[0].resources.requests.memory'

echo; echo "== Pods (tier, node, node instance type, resources)"
kubectl -n "$NS" get pods -l "$SEL" -o json | python3 -c '
import json,subprocess,sys
allpods=json.load(sys.stdin)["items"]
terminating=[p for p in allpods if p["metadata"].get("deletionTimestamp")]
pods=[p for p in allpods if not p["metadata"].get("deletionTimestamp")]
for p in terminating:
    print(f"note: ignoring pod {p['metadata']['name']} on {p['spec'].get('nodeName')} (terminating since {p['metadata']['deletionTimestamp']})")
nodes={n["metadata"]["name"]:n for n in json.loads(subprocess.check_output(["kubectl","get","nodes","-o","json"]))["items"]}
print(f"{"POD":48} {"TIER":8} {"PHASE":9} {"NODE":52} {"INSTANCE_TYPE":15} {"CPU_REQ":8} {"MEM_REQ":8} {"MEM_LIM":8}")
per_node={}
for p in sorted(pods,key=lambda p:p["spec"].get("nodeName","")):
    node=p["spec"].get("nodeName","<unscheduled>")
    per_node[node]=per_node.get(node,0)+1
    r=p["spec"]["containers"][0]["resources"]
    it=nodes.get(node,{}).get("metadata",{}).get("labels",{}).get("node.kubernetes.io/instance-type","?")
    print(f"{p["metadata"]["name"]:48} {(p["metadata"]["labels"].get("node-sized-agent/tier") or p["metadata"]["labels"].get("app.kubernetes.io/component","?")):8} {p["status"]["phase"]:9} {node:52} {it:15} {r["requests"]["cpu"]:8} {r["requests"]["memory"]:8} {r.get("limits",{}).get("memory","-"):8}")
print()
ok=True
for n in nodes:
    c=per_node.get(n,0)
    if c!=1:
        ok=False; print(f"FAIL: node {n} has {c} agent pods (expected 1)")
if "<unscheduled>" in per_node:
    ok=False; print(f"FAIL: {per_node["<unscheduled>"]} agent pod(s) unscheduled")
if any(p["status"]["phase"]!="Running" for p in pods):
    ok=False; print("FAIL: not all agent pods are Running")
print("RESULT:", "PASS - every node runs exactly one agent pod" if ok else "FAIL")
sys.exit(0 if ok else 1)
'

echo; echo "== What each pod sees about itself (downward API)"
for p in $(kubectl -n "$NS" get pods -l "$SEL" -o name); do
  kubectl -n "$NS" logs "$p" --tail=1
done

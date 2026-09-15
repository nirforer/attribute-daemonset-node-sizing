# Java-inject policy tests

Self-contained Go module (independent of the operator project) that tests the
`javaAutoInject` MutatingAdmissionPolicy shipped by this chart.

Two layers:

- **`jto_merge_test.go`** — cel-go unit tests for the `JAVA_TOOL_OPTIONS`
  value-merge expression (`../files/cel/merge_jto.cel`). Fast, no cluster.
- **`policy_envtest_test.go`** — end-to-end tests that boot a real Kubernetes
  **1.36** apiserver via [envtest] (no Docker), apply the **helm-rendered**
  policy + binding, create pods, and assert what mutating admission actually
  produced (pod/namespace opt-in, env merge, multi-container, idempotency,
  and the injected container's `securityContext`/`resources`). One case runs in
  a namespace enforcing the **restricted** Pod Security Standard and asserts the
  pod is *admitted* — that is the regression test for an unhardened injected
  container getting the whole workload rejected on a hardened cluster. It only
  means something while the apiserver's `PodSecurity` plugin is on, which
  `setupEnv` enables explicitly.

## Run

```bash
# from this directory
cd charts/zouz-operator-chart/tests

# 1. fetch the apiserver+etcd binaries once (no Docker needed)
export KUBEBUILDER_ASSETS="$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24 use 1.36.x -p path)"

# 2. run everything (helm must be on PATH for the envtest cases)
go test ./... -count=1 -v
```

`-count=1` matters: the envtest cases read the chart template at runtime via
`helm template`, which Go's test cache cannot see — without it you may get stale
cached results after editing the chart.

If `KUBEBUILDER_ASSETS` is unset or `helm` is missing, the envtest cases
`t.Skip` and only the cel-go merge tests run.

## The webhook backend

This module covers the **policy** backend only. The `webhook` backend
(`javaAutoInject.mode=webhook`, for Kubernetes < 1.36) has an equivalent envtest
suite in **`injectwebhook/envtest_test.go`** in the operator module — it lives
there because it exercises the operator's admission server, and keeping it out of
here preserves this module's independence from the operator project.

The two suites assert the same scenarios against the same pinned `agentArg` and
annotation literals; that shared expectation is what keeps the backends from
drifting. If you change an injection default in `values.yaml`, expect both to
fail — update them together.

[envtest]: https://book.kubebuilder.io/reference/envtest.html

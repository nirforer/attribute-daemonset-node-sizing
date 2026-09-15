// Real, end-to-end tests for the Java auto-inject MutatingAdmissionPolicy.
//
// These boot a real Kubernetes >=1.36 apiserver via envtest (no Docker), apply
// the *helm-rendered* policy + binding, then create pods and assert what the
// apiserver's mutating admission actually produced. This exercises the real
// CEL (Object{}/JSONPatch{}, namespaceObject, variables, matchConditions
// scoping) — i.e. the parts the cel-go merge test cannot cover, and exactly the
// behaviour that the namespaceObject/matchConditions bug broke.
//
// Requirements:
//   - `helm` on PATH (renders the actual chart templates).
//   - envtest apiserver+etcd binaries. Get them by running
//     `go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use 1.36.x`,
//     then export KUBEBUILDER_ASSETS to the printed path (or pass -p env).
//
// If either is missing the envtest cases t.Skip (the cel-go merge tests still run).
package tests

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

const (
	releaseName  = "t"
	injectAnnot  = "instrumentation.attrb.io/inject-java"
	resourceName = "attrb-jagent"
	// agentArg is declared in jto_merge_test.go (same package).
)

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	clientK8s *kubernetes.Clientset
	envReady  bool
)

func TestMain(m *testing.M) {
	if err := setupEnv(); err != nil {
		// Leave envReady=false; envtest cases will skip with this reason.
		skipReason = err.Error()
	}
	code := m.Run()
	if testEnv != nil {
		_ = testEnv.Stop()
	}
	os.Exit(code)
}

var skipReason string

func setupEnv() error {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		return errf("KUBEBUILDER_ASSETS not set (run: setup-envtest use 1.36.x)")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		return errf("helm not found on PATH")
	}

	testEnv = &envtest.Environment{BinaryAssetsDirectory: assets}
	// MutatingAdmissionPolicy is GA in 1.36 (on by default); enable explicitly
	// so the suite is robust across apiserver default-plugin changes. PodSecurity
	// likewise: TestInject_RestrictedNamespaceAdmitted only means anything while
	// it is actually running.
	testEnv.ControlPlane.GetAPIServer().Configure().
		Append("enable-admission-plugins", "MutatingAdmissionPolicy").
		Append("enable-admission-plugins", "PodSecurity")

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		return err
	}
	clientK8s, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}

	if err := applyRenderedPolicy(); err != nil {
		return err
	}
	if err := waitForPolicyActive(); err != nil {
		return err
	}
	envReady = true
	return nil
}

type strErr string

func (e strErr) Error() string { return string(e) }
func errf(s string) error      { return strErr(s) }

// applyRenderedPolicy renders templates/10_java_inject_policy.yaml with helm and
// creates the resulting MutatingAdmissionPolicy + Binding in the test apiserver.
func applyRenderedPolicy() error {
	out, err := exec.Command("helm", "template", releaseName, "..",
		"--show-only", "templates/10_java_inject_policy.yaml",
		"--kube-version", "1.36.0",
		"--set", "javaAutoInject.enabled=true",
		"--set", "sensorDisableAutoJavaInstrumentation=true",
		"--set", "initconfig.enabled=false",
		"--set", "token=x",
		"--set", "cluster_name=c",
	).CombinedOutput()
	if err != nil {
		return errf("helm template failed: " + string(out))
	}

	ctx := context.Background()
	for _, doc := range strings.Split(string(out), "\n---") {
		doc = strings.TrimSpace(doc)
		if doc == "" || !strings.Contains(doc, "kind:") {
			continue
		}
		var tm metav1.TypeMeta
		if err := yaml.Unmarshal([]byte(doc), &tm); err != nil {
			return err
		}
		switch tm.Kind {
		case "MutatingAdmissionPolicy":
			var p admissionregistrationv1.MutatingAdmissionPolicy
			if err := yaml.Unmarshal([]byte(doc), &p); err != nil {
				return err
			}
			if _, err := clientK8s.AdmissionregistrationV1().MutatingAdmissionPolicies().
				Create(ctx, &p, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
		case "MutatingAdmissionPolicyBinding":
			var b admissionregistrationv1.MutatingAdmissionPolicyBinding
			if err := yaml.Unmarshal([]byte(doc), &b); err != nil {
				return err
			}
			if _, err := clientK8s.AdmissionregistrationV1().MutatingAdmissionPolicyBindings().
				Create(ctx, &b, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
		}
	}
	return nil
}

// waitForPolicyActive polls until the apiserver has compiled and wired the
// policy into the admission chain (there is a short propagation delay).
func waitForPolicyActive() error {
	ctx := context.Background()
	ns := "probe-activation"
	mustNamespace(ctx, ns, nil)
	return wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 60*time.Second, true,
		func(ctx context.Context) (bool, error) {
			p := podSpec("probe", map[string]string{injectAnnot: "true"}, nil)
			created, err := clientK8s.CoreV1().Pods(ns).Create(ctx, p, metav1.CreateOptions{})
			if err != nil {
				return false, nil
			}
			active := hasInitContainer(created, resourceName)
			_ = clientK8s.CoreV1().Pods(ns).Delete(ctx, "probe", metav1.DeleteOptions{})
			return active, nil
		})
}

// ---- helpers ----

func mustNamespace(ctx context.Context, name string, annotations map[string]string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations}}
	_, err := clientK8s.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		panic(err)
	}
}

// podSpec builds a single- or multi-container pod. containers maps container
// name -> existing JAVA_TOOL_OPTIONS value (use "" for none). If containers is
// nil, a single "app" container with no env is used.
func podSpec(name string, annotations map[string]string, containers map[string]string) *corev1.Pod {
	if containers == nil {
		containers = map[string]string{"app": ""}
	}
	var cs []corev1.Container
	for cname, jto := range containers {
		c := corev1.Container{Name: cname, Image: "busybox"}
		if jto != "" {
			c.Env = []corev1.EnvVar{{Name: "JAVA_TOOL_OPTIONS", Value: jto}}
		}
		cs = append(cs, c)
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations},
		Spec:       corev1.PodSpec{Containers: cs},
	}
}

// mustRestrictedNamespace creates a namespace that ENFORCES the restricted Pod
// Security Standard, i.e. one where an injected container without a compliant
// securityContext takes the whole pod down with it.
func mustRestrictedNamespace(ctx context.Context, name string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: name,
		Labels: map[string]string{
			"pod-security.kubernetes.io/enforce":         "restricted",
			"pod-security.kubernetes.io/enforce-version": "latest",
		},
	}}
	_, err := clientK8s.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		panic(err)
	}
}

// restrictedPodSpec is a pod that already satisfies the restricted Pod Security
// Standard on its own, so anything the mutation adds is what decides whether it
// is admitted.
func restrictedPodSpec(name string, annotations map[string]string) *corev1.Pod {
	p := podSpec(name, annotations, nil)
	p.Spec.SecurityContext = &corev1.PodSecurityContext{
		RunAsNonRoot:   new(true),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	for i := range p.Spec.Containers {
		p.Spec.Containers[i].SecurityContext = &corev1.SecurityContext{
			AllowPrivilegeEscalation: new(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		}
	}
	return p
}

func createPod(t *testing.T, ns string, p *corev1.Pod) *corev1.Pod {
	t.Helper()
	got, err := clientK8s.CoreV1().Pods(ns).Create(context.Background(), p, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod %s/%s: %v", ns, p.Name, err)
	}
	return got
}

func hasInitContainer(p *corev1.Pod, name string) bool {
	for _, c := range p.Spec.InitContainers {
		if c.Name == name {
			return true
		}
	}
	return false
}

func initContainerByName(t *testing.T, p *corev1.Pod, name string) *corev1.Container {
	t.Helper()
	for i := range p.Spec.InitContainers {
		if p.Spec.InitContainers[i].Name == name {
			return &p.Spec.InitContainers[i]
		}
	}
	t.Fatalf("init container %q missing from pod %s", name, p.Name)
	return nil
}

// checkHardenedInit asserts the injected init container carries the hardening the
// chart ships by default. The literals are pinned rather than derived from the
// rendered policy on purpose: the mirror-image assertion in the webhook suite
// (injectwebhook/envtest_test.go) pins the same ones, so a chart default that
// moves for only one backend fails one of the two suites.
func checkHardenedInit(t *testing.T, c *corev1.Container) {
	t.Helper()
	sc := c.SecurityContext
	if sc == nil {
		t.Fatalf("injected init container has no securityContext; it is rejected wherever restricted Pod Security is enforced")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Errorf("allowPrivilegeEscalation = %v; want false (required by restricted PSS)", sc.AllowPrivilegeEscalation)
	}
	if sc.Privileged == nil || *sc.Privileged {
		t.Errorf("privileged = %v; want false", sc.Privileged)
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Errorf("readOnlyRootFilesystem = %v; want true", sc.ReadOnlyRootFilesystem)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("runAsNonRoot = %v; want true (required by restricted PSS)", sc.RunAsNonRoot)
	}
	// Pinned, not inherited: a pod-level runAsUser: 0 would otherwise contradict
	// runAsNonRoot and the kubelet would refuse to start the container.
	if sc.RunAsUser == nil || *sc.RunAsUser != 65532 {
		t.Errorf("runAsUser = %v; want 65532 (the agent image's own USER)", sc.RunAsUser)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != 65532 {
		t.Errorf("runAsGroup = %v; want 65532", sc.RunAsGroup)
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("capabilities = %+v; want drop: [ALL] (required by restricted PSS)", sc.Capabilities)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("seccompProfile = %+v; want RuntimeDefault (required by restricted PSS)", sc.SeccompProfile)
	}

	// Requests AND limits, equal to each other: hardened clusters commonly require
	// both to be declared, and pod QoS counts init containers, so unequal values
	// would demote a Guaranteed pod to Burstable.
	for _, want := range []struct {
		res  corev1.ResourceName
		want string
	}{{corev1.ResourceCPU, "100m"}, {corev1.ResourceMemory, "64Mi"}} {
		req, hasReq := c.Resources.Requests[want.res]
		lim, hasLim := c.Resources.Limits[want.res]
		if !hasReq || !hasLim {
			t.Errorf("resources for %s: request set=%v, limit set=%v; want both", want.res, hasReq, hasLim)
			continue
		}
		if req.String() != want.want || lim.String() != want.want {
			t.Errorf("resources for %s = request %s / limit %s; want %s for both", want.res, req.String(), lim.String(), want.want)
		}
	}
}

func hasVolume(p *corev1.Pod, name string) bool {
	for _, v := range p.Spec.Volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}

func containerByName(p *corev1.Pod, name string) *corev1.Container {
	for i := range p.Spec.Containers {
		if p.Spec.Containers[i].Name == name {
			return &p.Spec.Containers[i]
		}
	}
	return nil
}

func envValue(c *corev1.Container, name string) (string, bool) {
	for _, e := range c.Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func hasVolumeMount(c *corev1.Container, name string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name {
			return true
		}
	}
	return false
}

func requireEnv() func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		if !envReady {
			t.Skipf("envtest not available: %s", skipReason)
		}
	}
}

var skip = requireEnv()

// ---- tests ----

func TestInject_PodAnnotation(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "pod-annot"
	mustNamespace(ctx, ns, nil) // namespace NOT opted in
	pod := createPod(t, ns, podSpec("app", map[string]string{injectAnnot: "true"}, nil))

	if !hasInitContainer(pod, resourceName) {
		t.Errorf("expected init container %q", resourceName)
	}
	if !hasVolume(pod, resourceName) {
		t.Errorf("expected volume %q", resourceName)
	}
	c := containerByName(pod, "app")
	if !hasVolumeMount(c, resourceName) {
		t.Errorf("expected volumeMount %q on container", resourceName)
	}
	if v, ok := envValue(c, "JAVA_TOOL_OPTIONS"); !ok || v != agentArg {
		t.Errorf("JAVA_TOOL_OPTIONS = %q, ok=%v; want %q", v, ok, agentArg)
	}
}

// TestInject_NamespaceAnnotation is the regression test for the bug: opt-in via
// the NAMESPACE annotation (pod itself not annotated). This only works because
// the opt-in is evaluated in a variable/mutation where namespaceObject is
// populated — it failed silently when the check lived in matchConditions.
func TestInject_NamespaceAnnotation(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "ns-annot"
	mustNamespace(ctx, ns, map[string]string{injectAnnot: "true"})
	pod := createPod(t, ns, podSpec("app", nil, nil)) // pod has NO annotation

	if !hasInitContainer(pod, resourceName) {
		t.Fatalf("namespace-annotation opt-in did not inject (the namespaceObject regression)")
	}
	c := containerByName(pod, "app")
	if v, ok := envValue(c, "JAVA_TOOL_OPTIONS"); !ok || v != agentArg {
		t.Errorf("JAVA_TOOL_OPTIONS = %q, ok=%v; want %q", v, ok, agentArg)
	}
}

func TestInject_NotOptedIn(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "no-optin"
	mustNamespace(ctx, ns, nil)
	pod := createPod(t, ns, podSpec("app", nil, nil))

	if hasInitContainer(pod, resourceName) {
		t.Errorf("pod with no opt-in should not be injected")
	}
	if hasVolume(pod, resourceName) {
		t.Errorf("pod with no opt-in should not get the agent volume")
	}
	if c := containerByName(pod, "app"); len(c.Env) != 0 {
		t.Errorf("pod with no opt-in should have no env, got %+v", c.Env)
	}
}

func TestInject_MergesExistingJavaToolOptions(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "merge-env"
	mustNamespace(ctx, ns, nil)
	pod := createPod(t, ns, podSpec("app",
		map[string]string{injectAnnot: "true"},
		map[string]string{"app": "-Xms256m -Xmx512m"}))

	c := containerByName(pod, "app")
	want := "-Xms256m -Xmx512m " + agentArg
	if v, _ := envValue(c, "JAVA_TOOL_OPTIONS"); v != want {
		t.Errorf("merged JAVA_TOOL_OPTIONS = %q; want %q", v, want)
	}
}

func TestInject_MultiContainer(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "multi-c"
	mustNamespace(ctx, ns, nil)
	pod := createPod(t, ns, podSpec("app",
		map[string]string{injectAnnot: "true"},
		map[string]string{"app": "-Xmx512m", "sidecar": ""}))

	for _, name := range []string{"app", "sidecar"} {
		c := containerByName(pod, name)
		if c == nil {
			t.Fatalf("container %q missing", name)
		}
		if !hasVolumeMount(c, resourceName) {
			t.Errorf("container %q missing agent volumeMount", name)
		}
		v, ok := envValue(c, "JAVA_TOOL_OPTIONS")
		if !ok || !strings.Contains(v, agentArg) {
			t.Errorf("container %q JAVA_TOOL_OPTIONS = %q; want it to contain %q", name, v, agentArg)
		}
	}
	// the app container's existing value must be preserved
	if v, _ := envValue(containerByName(pod, "app"), "JAVA_TOOL_OPTIONS"); v != "-Xmx512m "+agentArg {
		t.Errorf("app container merge = %q; want %q", v, "-Xmx512m "+agentArg)
	}
}

func TestInject_Idempotent(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "idem"
	mustNamespace(ctx, ns, nil)
	// pod already carries our agent arg; merge must not duplicate it.
	pod := createPod(t, ns, podSpec("app",
		map[string]string{injectAnnot: "true"},
		map[string]string{"app": agentArg}))

	c := containerByName(pod, "app")
	if v, _ := envValue(c, "JAVA_TOOL_OPTIONS"); v != agentArg {
		t.Errorf("idempotent merge = %q; want unchanged %q", v, agentArg)
	}
	if strings.Count(mustEnv(t, c), agentArg) != 1 {
		t.Errorf("agent arg should appear exactly once, got %q", mustEnv(t, c))
	}
}

// TestInject_InitContainerHardened pins the securityContext and resources the
// policy puts on the injected init container. Mirrors
// TestWebhook_InitContainerHardened in the webhook suite.
func TestInject_InitContainerHardened(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "hardened"
	mustNamespace(ctx, ns, nil)
	pod := createPod(t, ns, podSpec("app", map[string]string{injectAnnot: "true"}, nil))

	checkHardenedInit(t, initContainerByName(t, pod, resourceName))
}

// TestInject_RestrictedNamespaceAdmitted is the regression test for the
// customer-reported failure: with the injected init container unhardened, a pod
// in a namespace enforcing the restricted Pod Security Standard is REJECTED
// outright (this policy runs in mutating admission, before validating admission,
// so the pod the PodSecurity plugin sees is the mutated one) — injection took the
// workload down instead of instrumenting it, and only a policy exception worked
// around it.
func TestInject_RestrictedNamespaceAdmitted(t *testing.T) {
	skip(t)
	ctx := context.Background()
	ns := "restricted"
	mustRestrictedNamespace(ctx, ns)

	pod, err := clientK8s.CoreV1().Pods(ns).Create(ctx,
		restrictedPodSpec("app", map[string]string{injectAnnot: "true"}), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("opted-in pod rejected in a restricted-PSS namespace: %v", err)
	}
	if !hasInitContainer(pod, resourceName) {
		t.Fatalf("pod was admitted but not injected; the test proves nothing about the injected container")
	}
	checkHardenedInit(t, initContainerByName(t, pod, resourceName))
}

func mustEnv(t *testing.T, c *corev1.Container) string {
	t.Helper()
	v, ok := envValue(c, "JAVA_TOOL_OPTIONS")
	if !ok {
		t.Fatal("JAVA_TOOL_OPTIONS missing")
	}
	return v
}

// Reproduces Karpenter's DaemonSet-overhead decision for new nodes, using Karpenter's own
// scheduling library, for three ways of splitting Attribute's sensor DaemonSet.
//
// preFix  mirrors isDaemonPodCompatible(nct, pod) in karpenter <= v1.13.x  (scheduler.go)
// postFix mirrors isDaemonPodCompatible(nct, it, pod) in karpenter >= v1.14.0 (PR kubernetes-sigs/karpenter#2486)
// actual  mirrors isDaemonPodCompatibleWithNode: what lands on the node once it exists (same rule kube-scheduler applies)
package main

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/resources"
)

const aws = "karpenter.k8s.aws/"

func init() {
	// karpenter-provider-aws registers its labels as well-known in its package init (pkg/apis/v1/labels.go).
	v1.WellKnownLabels = v1.WellKnownLabels.Insert(aws+"instance-cpu", aws+"instance-memory", aws+"instance-category",
		aws+"instance-family", aws+"instance-size", aws+"instance-generation")
}

type it struct {
	name string
	cpu  int
	reqs scheduling.Requirements
}

func newIT(name string, cpu int) it {
	fam := strings.Split(name, ".")[0]
	return it{name, cpu, scheduling.NewRequirements(
		scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, name),
		scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpIn, fmt.Sprint(cpu)),
		scheduling.NewRequirement(aws+"instance-family", corev1.NodeSelectorOpIn, fam),
		scheduling.NewRequirement(aws+"instance-category", corev1.NodeSelectorOpIn, fam[:1]),
		scheduling.NewRequirement(aws+"instance-size", corev1.NodeSelectorOpIn, strings.Split(name, ".")[1]),
		scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, "amd64"),
		scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, "linux"),
		scheduling.NewRequirement(v1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, "on-demand", "spot"),
	)}
}

type pool struct {
	name   string
	labels map[string]string       // NodePool spec.template.metadata.labels (+ karpenter.sh/nodepool)
	reqs   scheduling.Requirements // what NewNodeClaimTemplate builds (nodeclaimtemplate.go)
	its    []it
}

func newPool(name string, labels map[string]string, its []it, reqs ...*scheduling.Requirement) pool {
	all := map[string]string{v1.NodePoolLabelKey: name}
	for k, v := range labels {
		all[k] = v
	}
	r := scheduling.NewRequirements(reqs...)
	r.Add(scheduling.NewLabelRequirements(all).Values()...)
	r.Add(scheduling.NewRequirement(v1.NodeRegisteredLabelKey, corev1.NodeSelectorOpIn, "true"))
	r.Add(scheduling.NewRequirement(v1.NodeInitializedLabelKey, corev1.NodeSelectorOpIn, "true"))
	return pool{name, all, r, its}
}

// labels a launched node of this instance type in this pool would carry
func (p pool) nodeLabels(i it) map[string]string {
	l := map[string]string{v1.CapacityTypeLabelKey: "on-demand"}
	for k, v := range p.labels {
		l[k] = v
	}
	for k, req := range i.reqs {
		if req.Operator() == corev1.NodeSelectorOpIn && len(req.Values()) == 1 {
			l[k] = req.Values()[0]
		}
	}
	return l
}

func in(k string, v ...string) corev1.NodeSelectorRequirement {
	return corev1.NodeSelectorRequirement{Key: k, Operator: corev1.NodeSelectorOpIn, Values: v}
}
func notIn(k string, v ...string) corev1.NodeSelectorRequirement {
	return corev1.NodeSelectorRequirement{Key: k, Operator: corev1.NodeSelectorOpNotIn, Values: v}
}

// ds builds the pod a sensor DaemonSet would create: os/arch expressions like Attribute's template, plus tier expressions.
func ds(name, cpu, mem string, extra ...corev1.NodeSelectorRequirement) *corev1.Pod {
	exprs := append([]corev1.NodeSelectorRequirement{
		in(corev1.LabelOSStable, "linux"), in(corev1.LabelArchStable, "amd64", "arm64")}, extra...)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "attribute"},
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: exprs}}}}},
			Containers: []corev1.Container{{Name: "zprobe", Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(mem)}}}},
		},
	}
}

func preFix(p pool, pod *corev1.Pod) bool {
	return p.reqs.IsCompatible(scheduling.NewStrictPodRequirements(pod), scheduling.AllowUndefinedWellKnownLabels)
}
func postFix(p pool, i it, pod *corev1.Pod) bool {
	pr := scheduling.NewStrictPodRequirements(pod)
	return p.reqs.IsCompatible(pr, scheduling.AllowUndefinedWellKnownLabels) && i.reqs.Intersects(pr) == nil
}
func actual(nodeLabels map[string]string, pod *corev1.Pod) bool {
	return scheduling.NewLabelRequirements(nodeLabels).Compatible(scheduling.NewStrictPodRequirements(pod)) == nil
}

func filter(pods []*corev1.Pod, f func(*corev1.Pod) bool) []*corev1.Pod {
	var out []*corev1.Pod
	for _, p := range pods {
		if f(p) {
			out = append(out, p)
		}
	}
	return out
}
func describe(pods []*corev1.Pod) string {
	rl := resources.RequestsForPods(pods...)
	names := make([]string, 0, len(pods))
	for _, p := range pods {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	cpu, mem := rl[corev1.ResourceCPU], rl[corev1.ResourceMemory]
	return fmt.Sprintf("%2d DS  cpu=%-6s mem=%-8s %s", len(pods), cpu.String(), fmt.Sprintf("%dMi", mem.Value()/1024/1024), strings.Join(names, ","))
}

func main() {
	its := map[string]it{"c6i.large": newIT("c6i.large", 2), "c6i.2xlarge": newIT("c6i.2xlarge", 8), "c6i.8xlarge": newIT("c6i.8xlarge", 32), "c6i.16xlarge": newIT("c6i.16xlarge", 64)}
	pools := []pool{
		newPool("general", nil, []it{its["c6i.large"], its["c6i.2xlarge"], its["c6i.8xlarge"], its["c6i.16xlarge"]},
			scheduling.NewRequirement(aws+"instance-category", corev1.NodeSelectorOpIn, "c", "m", "r")),
		newPool("small-nodes", map[string]string{"attrb.io/sensor-size": "small"}, []it{its["c6i.large"], its["c6i.2xlarge"]},
			scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpIn, "2", "4", "8")),
		newPool("big-nodes", map[string]string{"attrb.io/sensor-size": "large"}, []it{its["c6i.8xlarge"], its["c6i.16xlarge"]},
			scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpIn, "32", "48", "64")),
	}

	// Attribute's karpconfig (values.yaml 0.0.98): cpu -> request/mem_request
	karp := [][3]string{{"2", "100m", "300Mi"}, {"4", "100m", "300Mi"}, {"8", "100m", "300Mi"}, {"12", "120m", "300Mi"}, {"16", "160m", "320Mi"}, {"24", "240m", "640Mi"}, {"32", "320m", "640Mi"}, {"48", "480m", "1Gi"}, {"64", "640m", "1280Mi"}, {"72", "720m", "1500Mi"}, {"96", "960m", "1960Mi"}, {"128", "1280m", "2560Mi"}, {"192", "1920m", "4Gi"}}
	var karpDS []*corev1.Pod
	var allCPUs []string
	for _, k := range karp {
		karpDS = append(karpDS, ds("cpu"+k[0], k[1], k[2], in(aws+"instance-cpu", k[0])))
		allCPUs = append(allCPUs, k[0])
	}
	karpDS = append(karpDS, ds("default", "100m", "300Mi", notIn(aws+"instance-cpu", allCPUs...)))

	scenarios := []struct {
		title string
		pods  []*corev1.Pod
	}{
		{"A. Attribute karpconfig mode: 13 tiers + default, keyed on karpenter.k8s.aws/instance-cpu (well-known label)", karpDS},
		{"B. Tiered on a custom NodePool label attrb.io/sensor-size (small / large / default)", []*corev1.Pod{
			ds("small", "100m", "300Mi", in("attrb.io/sensor-size", "small")),
			ds("large", "400m", "1Gi", in("attrb.io/sensor-size", "large")),
			ds("default", "100m", "300Mi", notIn("attrb.io/sensor-size", "small", "large"))}},
		{"C. Tiered on karpenter.sh/nodepool (NodePool name, no NodePool changes needed)", []*corev1.Pod{
			ds("large", "400m", "1Gi", in(v1.NodePoolLabelKey, "big-nodes")),
			ds("default", "100m", "300Mi", notIn(v1.NodePoolLabelKey, "big-nodes"))}},
		{"D. Two tiers keyed on instance-cpu value ranges (well-known label, few DaemonSets)", []*corev1.Pod{
			ds("large", "400m", "1Gi", in(aws+"instance-cpu", "32", "48", "64")),
			ds("default", "100m", "300Mi", notIn(aws+"instance-cpu", "32", "48", "64"))}},
	}

	for _, sc := range scenarios {
		fmt.Printf("\n=== %s\n", sc.title)
		for _, p := range pools {
			fmt.Printf("NodePool %-12s labels=%v\n", p.name, p.labels)
			fmt.Printf("  karpenter <=1.13 plans every new node with : %s\n", describe(filter(sc.pods, func(pod *corev1.Pod) bool { return preFix(p, pod) })))
			for _, i := range p.its {
				post := filter(sc.pods, func(pod *corev1.Pod) bool { return postFix(p, i, pod) })
				act := filter(sc.pods, func(pod *corev1.Pod) bool { return actual(p.nodeLabels(i), pod) })
				verdict := "matches reality"
				if describe(post) != describe(act) {
					verdict = "MISMATCH"
				}
				fmt.Printf("  karpenter >=1.14 %-13s plans with        : %s\n", i.name, describe(post))
				fmt.Printf("                   %-13s actually gets     : %s  -> %s\n", i.name, describe(act), verdict)
			}
		}
	}
}

// Reproduces Karpenter's DaemonSet-overhead decision for new nodes, using Karpenter's own
// scheduling library, for several ways of splitting Attribute's sensor DaemonSet.
//
// preFix  mirrors isDaemonPodCompatible(nct, pod) in karpenter <= v1.13.x  (scheduler.go)
// postFix mirrors isDaemonPodCompatible(nct, it, pod) in karpenter >= v1.14.0 (PR kubernetes-sigs/karpenter#2975)
// actual  mirrors isDaemonPodCompatibleWithNode: what lands on the node once it exists (same rule kube-scheduler applies)
//
// Both Karpenter versions first drop DaemonSet pods that do not tolerate the NodePool's taints
// (scheduling.Taints(...).ToleratesPod), then compare requirements; that order is mirrored here.
//
// Sections A-D use three synthetic NodePools. Section E replays the customer's real on-demand c6i
// NodePools: requirements, template labels and taints copied from their rendered
// karpenter/templates/nodepools.yaml (all five pools share the same requirements and differ only
// in labels and taints, so two of them are enough).
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
	taints []corev1.Taint          // NodePool spec.template.spec.taints
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
	return pool{name: name, labels: all, reqs: r, its: its}
}

func tainted(p pool, taints ...corev1.Taint) pool {
	p.taints = taints
	return p
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

// ds builds the pod a sensor DaemonSet would create: os/arch expressions like Attribute's template, plus tier
// expressions, and the chart's default blanket toleration (daemonset.selectors.tolerations: [{operator: Exists}]).
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

func tolerates(p pool, pod *corev1.Pod) bool {
	return scheduling.Taints(p.taints).ToleratesPod(pod) == nil
}
func preFix(p pool, pod *corev1.Pod) bool {
	return tolerates(p, pod) && p.reqs.IsCompatible(scheduling.NewStrictPodRequirements(pod), scheduling.AllowUndefinedWellKnownLabels)
}
func postFix(p pool, i it, pod *corev1.Pod) bool {
	pr := scheduling.NewStrictPodRequirements(pod)
	return tolerates(p, pod) && p.reqs.IsCompatible(pr, scheduling.AllowUndefinedWellKnownLabels) && i.reqs.Intersects(pr) == nil
}
func actual(p pool, i it, pod *corev1.Pod) bool {
	return tolerates(p, pod) && scheduling.NewLabelRequirements(p.nodeLabels(i)).Compatible(scheduling.NewStrictPodRequirements(pod)) == nil
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

type scenario struct {
	title string
	pods  []*corev1.Pod
}

func report(pools []pool, scenarios []scenario) {
	for _, sc := range scenarios {
		fmt.Printf("\n=== %s\n", sc.title)
		for _, p := range pools {
			fmt.Printf("NodePool %-20s labels=%v", p.name, p.labels)
			if len(p.taints) > 0 {
				fmt.Printf("\n%29staints=%v", "", p.taints)
			}
			fmt.Println()
			fmt.Printf("  karpenter <=1.13 plans every new node with : %s\n", describe(filter(sc.pods, func(pod *corev1.Pod) bool { return preFix(p, pod) })))
			for _, i := range p.its {
				post := filter(sc.pods, func(pod *corev1.Pod) bool { return postFix(p, i, pod) })
				act := filter(sc.pods, func(pod *corev1.Pod) bool { return actual(p, i, pod) })
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

	report(pools, []scenario{
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
	})

	// ---- E. the customer's NodePools -------------------------------------------------------------------------
	// Every c6i size the pool admits: instance-cpu Gt 1 and Lt 34 -> large(2) xlarge(4) 2xlarge(8) 4xlarge(16) 8xlarge(32).
	// c6i.12xlarge (48) and up are excluded by the NodePool itself, so Karpenter never considers them.
	rk := "node.riskified.com/"
	c6i := []it{newIT("c6i.large", 2), newIT("c6i.xlarge", 4), newIT("c6i.2xlarge", 8), newIT("c6i.4xlarge", 16), newIT("c6i.8xlarge", 32)}
	custReqs := func() []*scheduling.Requirement {
		return []*scheduling.Requirement{
			scheduling.NewRequirement(aws+"instance-family", corev1.NodeSelectorOpIn, "c6i"),
			scheduling.NewRequirement(v1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, "on-demand"),
			scheduling.NewRequirement(aws+"instance-generation", corev1.NodeSelectorOpGt, "4"),
			scheduling.NewRequirement(aws+"instance-generation", corev1.NodeSelectorOpLt, "8"),
			scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpLt, "34"),
			scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpGt, "1"),
			scheduling.NewRequirement(rk+"capacity-spread", corev1.NodeSelectorOpIn, "1-ondemand", "2-ondemand"),
			scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, "amd64"),
			scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, "linux"),
			scheduling.NewRequirement("run-with-daemonset", corev1.NodeSelectorOpExists),
			scheduling.NewRequirement(rk+"group", corev1.NodeSelectorOpExists),
		}
	}
	custLabels := func(app string) map[string]string {
		return map[string]string{rk + "instanceCategory": "c", rk + "instanceFamilies": "c6i", rk + "capacityType": "on-demand",
			rk + "nodePoolType": "on-demand", v1.CapacityTypeLabelKey: "on-demand", rk + "Application": app}
	}
	noSchedule := func(k, v string) corev1.Taint {
		return corev1.Taint{Key: k, Value: v, Effect: corev1.TaintEffectNoSchedule}
	}
	custPools := []pool{
		tainted(newPool("on-demand-c6i", custLabels("default"), c6i, custReqs()...),
			noSchedule(rk+"capacityType", "on-demand")),
		tainted(newPool("on-demand-c6i-avro", custLabels("avro"), c6i, custReqs()...),
			noSchedule(rk+"capacityType", "on-demand"), noSchedule(rk+"Application", "avro")),
	}
	poolNames := []string{"on-demand-c6i", "on-demand-c6i-avro"}

	cpuTiers := []*corev1.Pod{
		ds("small", "100m", "300Mi", in(aws+"instance-cpu", "2", "4")),
		ds("medium", "160m", "320Mi", in(aws+"instance-cpu", "8", "16")),
		ds("large", "320m", "640Mi", in(aws+"instance-cpu", "32")),
		ds("default", "100m", "300Mi", notIn(aws+"instance-cpu", "2", "4", "8", "16", "32")),
	}

	fmt.Printf("\n\n##### E. The customer's NodePools (on-demand c6i, 2-32 vCPU inside ONE pool) #####\n")
	report(custPools, []scenario{
		{"E1. Single DaemonSet mode (daemonset.enabled=true): one size for every node, nothing to mis-count", []*corev1.Pod{
			ds("sensor", "100m", "300Mi")}},
		{"E2. Attribute karpconfig mode (daemonset.karpenter=true): 13 CPU tiers + default on karpenter.k8s.aws/instance-cpu", karpDS},
		{"E3. Tiered mode, 3 tiers on karpenter.k8s.aws/instance-cpu: small 2-4 / medium 8-16 / large 32 vCPU (+ catch-all)", cpuTiers},
		{"E4. Tiered mode keyed on the pool itself (karpenter.sh/nodepool): exact on every version, but the same size on a 2 and a 32 vCPU node", []*corev1.Pod{
			ds("c6i", "320m", "640Mi", in(v1.NodePoolLabelKey, poolNames...)),
			ds("default", "100m", "300Mi", notIn(v1.NodePoolLabelKey, poolNames...))}},
	})

	// ---- F. What the customer's NodePools would have to look like for pool-keyed tiers to size the sensor ------------
	// F1 keeps their pool as is and only moves the existing NodePool label nodePoolCoreSize="2_33" into
	// spec.template.metadata.labels, so it reaches nodes and Karpenter's planning. F2-F4 split the pool into
	// three CPU bands, either as discrete instance-cpu sets (F2, F4) or as Gt/Lt ranges (F3).
	withLabel := func(app string, k, v string) map[string]string { l := custLabels(app); l[k] = v; return l }
	coreSizePool := tainted(newPool("on-demand-c6i", withLabel("default", rk+"nodePoolCoreSize", "2_33"), c6i, custReqs()...),
		noSchedule(rk+"capacityType", "on-demand"))
	bandReqs := func(cpus ...string) []*scheduling.Requirement {
		r := custReqs()
		r = append(r, scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpIn, cpus...)) // intersects the Gt1/Lt34 pair
		return r
	}
	rangeReqs := func(gt, lt string) []*scheduling.Requirement {
		r := custReqs()
		r = append(r, scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpGt, gt), scheduling.NewRequirement(aws+"instance-cpu", corev1.NodeSelectorOpLt, lt))
		return r
	}
	t := noSchedule(rk+"capacityType", "on-demand")
	setPools := []pool{
		tainted(newPool("on-demand-c6i-small", withLabel("default", "attrb.io/sensor-size", "small"), c6i[0:2], bandReqs("2", "4")...), t),
		tainted(newPool("on-demand-c6i-medium", withLabel("default", "attrb.io/sensor-size", "medium"), c6i[2:4], bandReqs("8", "16")...), t),
		tainted(newPool("on-demand-c6i-large", withLabel("default", "attrb.io/sensor-size", "large"), c6i[4:5], bandReqs("32")...), t),
	}
	rangePools := []pool{
		tainted(newPool("on-demand-c6i-small", withLabel("default", "attrb.io/sensor-size", "small"), c6i[0:2], rangeReqs("1", "5")...), t),
		tainted(newPool("on-demand-c6i-medium", withLabel("default", "attrb.io/sensor-size", "medium"), c6i[2:4], rangeReqs("5", "17")...), t),
		tainted(newPool("on-demand-c6i-large", withLabel("default", "attrb.io/sensor-size", "large"), c6i[4:5], rangeReqs("17", "34")...), t),
	}
	labelTiers := []*corev1.Pod{
		ds("small", "100m", "300Mi", in("attrb.io/sensor-size", "small")),
		ds("medium", "160m", "320Mi", in("attrb.io/sensor-size", "medium")),
		ds("large", "320m", "640Mi", in("attrb.io/sensor-size", "large")),
		ds("default", "100m", "300Mi", notIn("attrb.io/sensor-size", "small", "medium", "large")),
	}

	fmt.Printf("\n\n##### F. Hypothetical changes to the customer's NodePools #####\n")
	report([]pool{coreSizePool}, []scenario{
		{"F1. Their nodePoolCoreSize=2_33 label moved into template labels, one tier keyed on it: exact, but still one size for 2-32 vCPU", []*corev1.Pod{
			ds("band-2-33", "320m", "640Mi", in(rk+"nodePoolCoreSize", "2_33")),
			ds("default", "100m", "300Mi", notIn(rk+"nodePoolCoreSize", "2_33"))}},
	})
	report(setPools, []scenario{
		{"F2. Pool split into 3 bands as discrete instance-cpu sets (In [2,4] / In [8,16] / In [32]); tiers on instance-cpu", cpuTiers},
	})
	report(rangePools, []scenario{
		{"F3. Pool split into 3 bands as Gt/Lt ranges; tiers on instance-cpu: the NotIn catch-all is still counted before 1.14", cpuTiers},
	})
	report(rangePools, []scenario{
		{"F4. Same Gt/Lt bands, but each band carries a template label attrb.io/sensor-size and tiers key on that label", labelTiers},
	})
}

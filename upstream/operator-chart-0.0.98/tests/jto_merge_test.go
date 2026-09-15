// Tests for the JAVA_TOOL_OPTIONS merge semantics used by the Java
// auto-inject MutatingAdmissionPolicy.
//
// These evaluate the EXACT CEL expression the policy embeds
// (../files/cel/merge_jto.cel) via cel-go, so the behaviour proven here is the
// behaviour the apiserver runs. The policy substitutes __JVAL__ with the
// container's existing JAVA_TOOL_OPTIONS value and __ARG__ with the agent arg;
// here we bind them as string variables.
package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
)

// mergeExprPath is the single source of truth shared with the Helm chart.
const mergeExprPath = "../files/cel/merge_jto.cel"

// agentArg mirrors the value the chart computes from javaAutoInject defaults:
// "-javaagent:<mountPath>/<targetFileName>".
const agentArg = "-javaagent:/attrb-jagent/attribute-jagent.jar"

// newMergeProgram compiles the shared CEL merge expression with __JVAL__ and
// __ARG__ bound as string inputs, exactly as the apiserver would type them.
func newMergeProgram(t *testing.T) cel.Program {
	t.Helper()

	src, err := os.ReadFile(mergeExprPath)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Clean(mergeExprPath), err)
	}

	env, err := cel.NewEnv(
		ext.Strings(),
		cel.Variable("__JVAL__", cel.StringType),
		cel.Variable("__ARG__", cel.StringType),
	)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}

	ast, iss := env.Compile(string(src))
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile merge_jto.cel: %v", iss.Err())
	}
	if ast.OutputType() != cel.StringType {
		t.Fatalf("merge expression must return string, got %s", ast.OutputType())
	}

	prg, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	return prg
}

func merge(t *testing.T, prg cel.Program, jval, arg string) string {
	t.Helper()
	out, _, err := prg.Eval(map[string]any{"__JVAL__": jval, "__ARG__": arg})
	if err != nil {
		t.Fatalf("eval (jval=%q): %v", jval, err)
	}
	s, ok := out.Value().(string)
	if !ok {
		t.Fatalf("eval (jval=%q): result not a string: %T", jval, out.Value())
	}
	return s
}

func TestMergeJavaToolOptions(t *testing.T) {
	prg := newMergeProgram(t)

	cases := []struct {
		name string
		jval string
		want string
	}{
		{
			name: "empty value -> just the agent arg",
			jval: "",
			want: agentArg,
		},
		{
			name: "single existing option -> appended space-separated",
			jval: "-Xmx512m",
			want: "-Xmx512m " + agentArg,
		},
		{
			name: "multiple existing options -> appended once at the end",
			jval: "-Xms256m -Xmx512m -Dspring.profiles.active=prod",
			want: "-Xms256m -Xmx512m -Dspring.profiles.active=prod " + agentArg,
		},
		{
			name: "already contains our agent (exact) -> unchanged (idempotent)",
			jval: agentArg,
			want: agentArg,
		},
		{
			name: "already contains our agent at start -> unchanged",
			jval: agentArg + " -Xmx512m",
			want: agentArg + " -Xmx512m",
		},
		{
			name: "already contains our agent in the middle -> unchanged",
			jval: "-Xmx1g " + agentArg + " -Dfoo=bar",
			want: "-Xmx1g " + agentArg + " -Dfoo=bar",
		},
		{
			name: "a different javaagent present -> ours appended (agents coexist)",
			jval: "-javaagent:/opt/other/agent.jar",
			want: "-javaagent:/opt/other/agent.jar " + agentArg,
		},
		{
			name: "leading and trailing whitespace preserved, arg appended",
			jval: "  -Dfoo=bar ",
			want: "  -Dfoo=bar  " + agentArg,
		},
		{
			name: "quoted property value preserved",
			jval: `-Dmessage="hello world" -Dx=y`,
			want: `-Dmessage="hello world" -Dx=y ` + agentArg,
		},
		{
			name: "tab-separated options preserved",
			jval: "-Da=b\t-Dc=d",
			want: "-Da=b\t-Dc=d " + agentArg,
		},
		{
			name: "value with newline preserved",
			jval: "-Da=b\n-Dc=d",
			want: "-Da=b\n-Dc=d " + agentArg,
		},
		{
			name: "agent flags only (no jvm opts) -> appended",
			jval: "-XX:+UseG1GC",
			want: "-XX:+UseG1GC " + agentArg,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := merge(t, prg, tc.jval, agentArg)
			if got != tc.want {
				t.Errorf("merge(%q)\n  got:  %q\n  want: %q", tc.jval, got, tc.want)
			}
		})
	}
}

// TestMergeIsIdempotent re-applies the merge to its own output and asserts the
// value stops growing — the apiserver may re-evaluate, and pods may be
// recreated, so a second pass must be a no-op.
func TestMergeIsIdempotent(t *testing.T) {
	prg := newMergeProgram(t)

	inputs := []string{
		"",
		"-Xmx512m",
		"-Xms256m -Xmx512m -Dspring.profiles.active=prod",
		"-javaagent:/opt/other/agent.jar",
		"  -Dfoo=bar ",
	}

	for _, in := range inputs {
		first := merge(t, prg, in, agentArg)
		second := merge(t, prg, first, agentArg)
		if first != second {
			t.Errorf("not idempotent for %q\n  first:  %q\n  second: %q", in, first, second)
		}
	}
}

// TestMergeKnownPrefixCollision documents a deliberate limitation: idempotency
// uses substring containment, so a *different* agent whose path has ours as a
// prefix is treated as already-present and skipped. This is an accepted edge
// (the renamed attribute-jagent.jar makes a real collision implausible); the
// test pins the behaviour so a future change to the merge logic is intentional.
func TestMergeKnownPrefixCollision(t *testing.T) {
	prg := newMergeProgram(t)

	collide := agentArg + "-NOT-REALLY-OURS"
	got := merge(t, prg, collide, agentArg)
	if got != collide {
		t.Errorf("prefix-collision behaviour changed: got %q want %q (update this test deliberately)", got, collide)
	}
}

# Summarise results/*.txt written by round.sh into one table (markdown on stdout)
import glob, re, os, sys
d = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "results")
rows = []
for f in sorted(glob.glob(os.path.join(d, "*.txt"))):
    t = open(f).read()
    label = re.search(r"^##### (\S+)", t, re.M).group(1)
    ver = re.search(r"v(1\.\d+\.\d+)", label).group(1)
    m = re.search(r"'created nodeclaim'.*?'requests': \{'cpu': '([^']+)', 'memory': '([^']+)', 'pods': '(\d+)'\}, 'instance-types': '([^']+)'", t)
    planned = m.groups() if m else ("?", "?", "?", "?")
    launched = re.search(r"'launched nodeclaim'.*?'instance-type': '([^']+)'", t)
    sensors = re.findall(r"^  (t-\S+|t) \{'cpu': '([^']+)', 'memory': '([^']+)'\}", t, re.M)
    rows.append((label, ver, planned, launched.group(1) if launched else "?", sensors))
print("| round | karpenter | planned requests cpu / mem / pods | instance types that fit | launched | sensor pod that landed |")
print("|---|---|---|---|---|---|")
for label, ver, p, l, s in rows:
    print(f"| {label} | {ver} | {p[0]} / {p[1]} / {p[2]} | {p[3]} | {l} | {', '.join(n+' '+c+' / '+m for n,c,m in s) or '-'} |")

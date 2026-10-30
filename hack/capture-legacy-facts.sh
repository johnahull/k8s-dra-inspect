#!/usr/bin/env bash
# MANUAL tool, not run in CI. Regenerates testdata/parity/<fixture>.facts.json
# by running the user's legacy dra-verify.sh / dra-counters.py against the
# committed fixtures through a STUB kubectl. Requires python3 with PyYAML.
#
#   DRA_VERIFY_DIR=/path/to/testing/scripts hack/capture-legacy-facts.sh
#
# Safety: the legacy script only ever sees the stub `kubectl`, first on PATH.
# The stub serves `kubectl get <resource> -o json` lists built from the
# fixtures and exits non-zero (loudly) for any other verb or resource.
# HOME and KUBECONFIG point at an empty scratch dir, so no real cluster or
# kubeconfig is reachable. Read the legacy script before running it.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$PWD
LEGACY=${DRA_VERIFY_DIR:-$HOME/redhat/dra-topology-aware-co-placement/testing/scripts}
[ -f "$LEGACY/dra-verify.sh" ] && [ -f "$LEGACY/dra-counters.py" ] || {
  echo "set DRA_VERIFY_DIR to the directory holding dra-verify.sh and dra-counters.py" >&2; exit 1; }
SCRATCH=${PARITY_SCRATCH:-$(mktemp -d)}
mkdir -p "$SCRATCH/bin" "$SCRATCH/home" "$SCRATCH/raw" "$ROOT/testdata/parity"

: >"$SCRATCH/refusals.txt"
cat >"$SCRATCH/bin/kubectl" <<'PY'
#!/usr/bin/env python3
"""Stub kubectl: serves fixture YAML as `get <resource> -o json` lists."""
import json, os, sys, glob
import yaml

def die(msg):
    if os.environ.get("PARITY_REFUSALS"):  # the legacy script may swallow stderr
        with open(os.environ["PARITY_REFUSALS"], "a") as f:
            f.write("%s (argv=%s)\n" % (msg, sys.argv[1:]))
    sys.stderr.write("STUB-KUBECTL REFUSED: " + msg + " (argv=%s)\n" % sys.argv[1:])
    sys.exit(97)

args = sys.argv[1:]
if not args or args[0] != "get":
    die("only `get` is served; mutating or unknown verb")
KINDS = {"resourceslices": "ResourceSlice", "resourceclaims": "ResourceClaim",
         "deviceclasses": "DeviceClass", "vmi": "VirtualMachineInstance",
         "pods": "Pod"}
EMPTY_OK = {"ds", "namespaces"}  # drivers section: no DaemonSets in fixtures
res = args[1] if len(args) > 1 else die("no resource")
rest = args[2:]
sel = None
out = None
i = 0
while i < len(rest):
    a = rest[i]
    if a in ("-A", "--all-namespaces"):
        pass
    elif a in ("-n", "--namespace"):
        i += 1
    elif a == "-l":
        sel = rest[i + 1]; i += 1
    elif a == "-o":
        out = rest[i + 1]; i += 1
    else:
        die("unrecognized argument %r" % a)
    i += 1
if res in EMPTY_OK or (res == "pods" and out == "wide"):  # drivers section lists pods as a table
    if out == "json":
        print(json.dumps({"items": []}))
    sys.exit(0)  # `-o wide` table: empty, like a cluster with none
if res not in KINDS:
    die("unrecognized resource %r" % res)
if out != "json":
    die("only -o json is served for %s" % res)
items = []
for d in os.environ["PARITY_FIXTURE_DIRS"].split(":"):
    for f in sorted(glob.glob(os.path.join(d, "*.yaml"))):
        doc = yaml.safe_load(open(f))
        for it in (doc.get("items") if doc.get("kind") == "List" else [doc]):
            if it.get("kind") == KINDS[res]:
                items.append(it)
if sel:
    k, _, v = sel.partition("=")
    items = [x for x in items if x.get("metadata", {}).get("labels", {}).get(k) == v]
if res == "resourceclaims" and os.environ.get("PARITY_INJECT_RESERVED"):
    # The legacy script ignores allocated claims that have no reservedFor.
    for x in items:
        if x.get("status", {}).get("allocation") and not x["status"].get("reservedFor"):
            x["status"]["reservedFor"] = [{"resource": "pods", "name": "stub-pod"}]
if os.environ.get("PARITY_SERVED"):  # names handed to the legacy script
    with open(os.environ["PARITY_SERVED"], "a") as f:
        f.write(json.dumps({"resource": res, "selector": sel, "names": [x["metadata"]["name"] for x in items]}) + "\n")
print(json.dumps({"apiVersion": "v1", "kind": "List", "items": items}))
PY
chmod +x "$SCRATCH/bin/kubectl"

# fixture name -> colon-separated fixture directories fed to the stub.
declare -A DIRS=(
  [xe9785l]="$ROOT/testdata/fixtures/xe9785l"
  [xe9680]="$ROOT/testdata/fixtures/xe9680"
  [classes-claims]="$ROOT/testdata/fixtures/classes-claims"
  [multinode]="$ROOT/testdata/fixtures/multinode"
)
COUNTERS=(exactly-full fits none overcommit stale-generation unknown-counter unknown-set)
for c in "${COUNTERS[@]}"; do DIRS[counters-$c]="$ROOT/testdata/fixtures/counters/$c"; done

legacy() { # fixture name, output file, args...; records exit code in <file>.rc
  local fx=$1 out=$2; shift 2
  set +e
  rm -f "$out.served"
  env -i PATH="$SCRATCH/bin:/usr/bin:/bin" HOME="$SCRATCH/home" KUBECONFIG="$SCRATCH/home/none" \
    PARITY_FIXTURE_DIRS="${DIRS[$fx]}" PARITY_SERVED="$out.served" PARITY_REFUSALS="$SCRATCH/refusals.txt" NO_COLOR=1 \
    ${PARITY_INJECT_RESERVED:+PARITY_INJECT_RESERVED=1} \
    bash "$LEGACY/dra-verify.sh" "$@" >"$out" 2>"$out.err"
  echo $? >"$out.rc"
  set -e
}

for fx in "${!DIRS[@]}"; do
  mkdir -p "$SCRATCH/raw/$fx"
  if [[ $fx == counters-* ]]; then
    legacy "$fx" "$SCRATCH/raw/$fx/counters.txt" counters
    PARITY_INJECT_RESERVED=1 legacy "$fx" "$SCRATCH/raw/$fx/counters-reserved.txt" counters
    continue
  fi
  for cmd in drivers topology claims alignment deviceclasses attributes slices; do
    extra=(); [[ $cmd == topology ]] && extra=(-t)
    legacy "$fx" "$SCRATCH/raw/$fx/$cmd.txt" "$cmd" "${extra[@]}"
  done
done
if [ -s "$SCRATCH/refusals.txt" ] || grep -l "STUB-KUBECTL REFUSED" "$SCRATCH"/raw/*/*.err 2>/dev/null; then
  echo "stub kubectl refused a call:" >&2; cat "$SCRATCH/refusals.txt" >&2; exit 1
fi

python3 - "$SCRATCH/raw" "$ROOT" "$LEGACY" <<'PY'
import glob, hashlib, json, os, re, sys
import yaml
raw, root, legacy = sys.argv[1:4]
outdir = os.path.join(root, "testdata", "parity")
ANSI = re.compile(r"\x1b\[[0-9;]*m")

def read(fx, cmd):
    p = os.path.join(raw, fx, cmd + ".txt")
    return ANSI.sub("", open(p).read()), int(open(p + ".rc").read())

def sha(f):
    return hashlib.sha256(open(os.path.join(legacy, f), "rb").read()).hexdigest()

def fixture_dir(fx):
    if fx.startswith("counters-"):
        return os.path.join(root, "testdata", "fixtures", "counters", fx[len("counters-"):])
    return os.path.join(root, "testdata", "fixtures", fx)

def objects(fx):
    for f in sorted(glob.glob(os.path.join(fixture_dir(fx), "*.yaml"))):
        doc = yaml.safe_load(open(f))
        yield from (doc["items"] if doc.get("kind") == "List" else [doc])

def known_devices(fx):  # names to look for in fixed-width legacy tables
    names = set()
    for o in objects(fx):
        if o["kind"] == "ResourceSlice":
            names |= {d["name"] for d in o["spec"].get("devices") or []}
        if o["kind"] == "ResourceClaim":
            res = o.get("status", {}).get("allocation", {}).get("devices", {}).get("results", [])
            names |= {r["device"] for r in res}
    return names

def devices_in(line, names):
    # Legacy columns overflow into each other, so match known device names at
    # token boundaries instead of slicing columns.
    return {n for n in names if re.search(r"(?<![\w-])" + re.escape(n) + r"(?![\w-])", line)}

def drivers(txt):
    out = {}
    for m in re.finditer(r"^  (\S+): (\d+) devices on (.+)$", txt, re.M):
        out[m.group(1)] = {"devices": int(m.group(2)), "nodes": sorted(m.group(3).split(", "))}
    return out

def topology(txt):  # `topology -t`: Socket / NUMA / pcieRoot / label: names
    out, numa, root_ = {}, None, None
    for line in txt.splitlines():
        if m := re.match(r"^  NUMA (\S+)$", line): numa = m.group(1)
        elif m := re.match(r"^    pcieRoot: (\S+)$", line): root_ = m.group(1)
        elif m := re.match(r"^      \S+: (.+)$", line):
            for item in re.split(r", (?=\S+(?: \(|,|$))", m.group(1)):
                name = re.sub(r" \(.*\)$", "", item.strip())
                out[name] = {"primaryNuma": numa,
                             "pcieRoots": [] if root_ == "-" else root_.split(",")}
    return out

def alignment(txt, names):
    out, pod, in_rows = {}, None, False
    for line in txt.splitlines():
        if m := re.match(r"^(\S+/\S+)$", line):
            pod, in_rows = m.group(1), True; out[pod] = {"devices": set()}
        elif pod and in_rows and line.strip() and not line.lstrip().startswith(("Request", "─")) and "aligned" not in line and "SPLIT" not in line and "differs" not in line and "unknown" not in line:
            out[pod]["devices"] |= devices_in(line, names)
        elif pod and "numaNode aligned" in line: out[pod]["numa"] = "aligned"
        elif pod and "numaNode SPLIT" in line: out[pod]["numa"] = "split"
        elif pod and "numaNode unknown" in line: out[pod]["numa"] = "unknown"
        elif pod and "pcieRoot aligned" in line: out[pod]["pcieRoot"] = "aligned"
        elif pod and "pcieRoot differs" in line: out[pod]["pcieRoot"] = "differs"
    for v in out.values(): v["devices"] = sorted(v["devices"])
    return out

def claims(txt, names):  # header "ns/name  ->  pod|VM ..." or "ns/name: pending"
    out, cur = {}, None
    for line in txt.splitlines():
        if m := re.match(r"^(\S+/[^\s:]+)(?:: pending|  →  )", line):
            cur = m.group(1); out[cur] = set(); continue
        if cur and line.strip() and not line.lstrip().startswith(("Request", "─")):
            out[cur] |= devices_in(line, names)
    return {k: sorted(v) for k, v in out.items()}

def served(fx, cmd, resource):
    names = []
    for l in open(os.path.join(raw, fx, cmd + ".txt.served")):
        j = json.loads(l)
        if j["resource"] == resource:
            names = j["names"]
    return sorted(names)

def counters(fx, name):
    txt, rc = read(fx, name)
    sets = {}
    for m in re.finditer(r"^  (\S+)  \[(.+)\]$", txt, re.M):
        sets[m.group(1)] = dict(p.split(": ") for p in m.group(2).split(", "))
    return {"exit": rc, "sets": sets, "stale": "Stale allocation results" in txt,
            "diagnostics": sorted(re.findall(r"^  ! (.+)$", txt, re.M))}

for fx in sorted(os.listdir(raw)):
    facts = {"fixture": fx, "legacySha256": {"dra-verify.sh": sha("dra-verify.sh"), "dra-counters.py": sha("dra-counters.py")}}
    if fx.startswith("counters-"):
        facts["counters"] = counters(fx, "counters")
        facts["countersWithReservedFor"] = counters(fx, "counters-reserved")
    else:
        names = known_devices(fx)
        facts["drivers"] = drivers(read(fx, "drivers")[0])
        facts["topology"] = topology(read(fx, "topology")[0])
        facts["alignment"] = alignment(read(fx, "alignment")[0], names)
        facts["claims"] = claims(read(fx, "claims")[0], names)
        # `deviceclasses` needs Python >= 3.12 (nested f-string quotes), so its
        # table is not scraped: the facts are the names its kubectl query
        # (-l nodepartition.dra.k8s.io/managed=true) was served by the stub.
        facts["legacyExit"] = {c: read(fx, c)[1] for c in ("drivers", "topology", "claims", "alignment", "deviceclasses")}
        facts["deviceclassesQueried"] = served(fx, "deviceclasses", "deviceclasses")
    with open(os.path.join(outdir, fx + ".facts.json"), "w") as f:
        json.dump(facts, f, indent=2, sort_keys=True); f.write("\n")
    print("wrote", fx)
PY

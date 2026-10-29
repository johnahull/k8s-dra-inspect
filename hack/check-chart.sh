#!/usr/bin/env bash
# Renders the Helm chart for every profile and asserts the read-only invariants
# (docs/rbac.md rules, Job hardening, no workload kinds). Needs helm and python3
# with PyYAML. CHART and RBAC_DOC may be overridden (used for mutation tests).
cd "$(dirname "$0")/.." || exit 1
export CHART="${CHART:-deploy/helm/dra-inspect}" RBAC_DOC="${RBAC_DOC:-docs/rbac.md}"
exec python3 -I - <<'PY'
import os, re, subprocess, sys, yaml

CHART, DOC = os.environ["CHART"], os.environ["RBAC_DOC"]
fails = []

def fail(inv, msg):
    fails.append(f"FAIL inv{inv}: {msg}")
    print(fails[-1])

def helm(*args):
    return subprocess.run(["helm", *args], capture_output=True, text=True)

def render(sets):
    cmd = ["template", "rel", CHART, "-n", "relns"]
    for s in sets:
        cmd += ["--set", s]
    r = helm(*cmd)
    if r.returncode:
        return None, r.stderr.strip()
    return [d for d in yaml.safe_load_all(r.stdout) if d], ""

# Fenced yaml blocks of docs/rbac.md are the source of truth for the rules.
text = open(DOC).read()
blocks = [yaml.safe_load(b) for b in re.findall(r"```yaml\n(.*?)```", text, re.S)]
want = {b["kind"]: b["rules"] for b in blocks if b.get("kind") in ("ClusterRole", "Role")}
if set(want) != {"ClusterRole", "Role"}:
    fail(2, f"docs/rbac.md must have ClusterRole and Role yaml blocks, found {sorted(want)}")
    want = {"ClusterRole": [], "Role": []}

ALLOWED = {"ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "Job"}
BAD_KEYS = {"hostPath", "hostNetwork", "hostPID", "hostIPC", "hostPort", "privileged"}
FORBID_ARGS = ["--host-root", "--node-name", "cpupinning", "vfio", "report support", "--with-adapters"]
DEFAULT_ARGS = ["report", "cluster", "-o", "json", "--timeout", "60s"]

def walk(o, path=""):
    if isinstance(o, dict):
        for k, v in o.items():
            yield path + "/" + k, k, v
            yield from walk(v, path + "/" + k)
    elif isinstance(o, list):
        for i, v in enumerate(o):
            yield from walk(v, f"{path}[{i}]")

def check_rbac(tag, docs, profile, kubevirt, nss):
    kinds = [d["kind"] for d in docs]
    cl = [d for d in docs if d["kind"] == "ClusterRole"]
    roles = [d for d in docs if d["kind"] == "Role"]
    if profile == "cluster":
        if len(cl) != 1 or kinds.count("ClusterRoleBinding") != 1 or roles or "RoleBinding" in kinds:
            fail(2, f"{tag}: cluster profile must render exactly ClusterRole+ClusterRoleBinding and no Role/RoleBinding")
        exp, got = want["ClusterRole"], cl
    else:
        if cl or "ClusterRoleBinding" in kinds:
            fail(2, f"{tag}: namespace profile must not render ClusterRole/ClusterRoleBinding")
        if sorted(r["metadata"]["namespace"] for r in roles) != sorted(nss) or \
           sorted(d["metadata"]["namespace"] for d in docs if d["kind"] == "RoleBinding") != sorted(nss):
            fail(2, f"{tag}: Role+RoleBinding must exist exactly in namespaces {nss}")
        exp, got = want["Role"], roles
    exp = [r for r in exp if kubevirt or "kubevirt.io" not in r["apiGroups"]]
    for r in got:
        rules = r.get("rules") or []
        for rule in rules:
            if not set(rule.get("verbs", [])) <= {"get", "list"} or not rule.get("verbs"):
                fail(2, f"{tag}: verbs outside get/list: {rule.get('verbs')}")
            for res in rule.get("resources", []):
                if res == "*" or "/" in res or res in ("secrets", "configmaps"):
                    fail(2, f"{tag}: forbidden resource {res!r}")
            if "*" in rule.get("apiGroups", []) or "*" in rule.get("verbs", []):
                fail(2, f"{tag}: wildcard in apiGroups/verbs")
        if rules != exp:
            fail(2, f"{tag}: rendered rules differ from docs/rbac.md")

def check_job(tag, docs, profile, nss):
    jobs = [d for d in docs if d["kind"] == "Job"]
    if len(jobs) != 1:
        fail(3, f"{tag}: expected exactly one Job, got {len(jobs)}")
        return
    j = jobs[0]; s = j["spec"]; pod = s["template"]["spec"]; c = pod["containers"][0]
    sc, psc = c.get("securityContext", {}), pod.get("securityContext", {})
    def need(cond, msg):
        if not cond: fail(3, f"{tag}: {msg}")
    need(pod.get("restartPolicy") == "Never", "restartPolicy must be Never")
    need(s.get("backoffLimit") == 0, "backoffLimit must be 0")
    need(s.get("ttlSecondsAfterFinished") == 3600, "ttlSecondsAfterFinished must default to 3600")
    need(bool(s.get("activeDeadlineSeconds")), "activeDeadlineSeconds must be set")
    need(pod.get("automountServiceAccountToken") is True, "pod must set automountServiceAccountToken: true")
    sa = [d for d in docs if d["kind"] == "ServiceAccount"]
    need(all(d.get("automountServiceAccountToken") is False for d in sa), "ServiceAccount must set automountServiceAccountToken: false")
    need(psc.get("runAsNonRoot") is True and psc.get("runAsUser") == 65532 and psc.get("runAsGroup") == 65532, "pod runAsNonRoot/65532 required")
    need((psc.get("seccompProfile") or {}).get("type") == "RuntimeDefault", "pod seccompProfile RuntimeDefault required")
    need(sc.get("allowPrivilegeEscalation") is False, "allowPrivilegeEscalation must be false")
    need(sc.get("readOnlyRootFilesystem") is True, "readOnlyRootFilesystem must be true")
    need((sc.get("capabilities") or {}).get("drop") == ["ALL"], "capabilities.drop must be [ALL]")
    need(not sc.get("privileged"), "privileged must be absent/false")
    for p, k, v in walk(j):
        if k in BAD_KEYS and k != "privileged" and v not in (None, False, [], {}):
            fail(3, f"{tag}: host access field {p}")
    need(not any("hostPath" in v for v in pod.get("volumes", [])), "hostPath volume")
    r = c.get("resources", {})
    need(bool(r.get("requests")) and bool(r.get("limits")), "resource requests and limits required")
    need(re.fullmatch(r"ghcr\.io/johnahull/k8s-dra-inspect:0\.1\.0", c.get("image", "")) is not None, f"image must be repository:appVersion, got {c.get('image')}")
    args = c.get("args", [])
    exp = DEFAULT_ARGS + (["-n", nss[0]] if profile == "namespace" else [])
    need(args == exp, f"args must be {exp}, got {args}")
    need(not [f for f in FORBID_ARGS if f in " ".join(args)], "host-only flags/commands in args")
    if "securityContext" in c and sc.get("privileged"):
        fail(4, f"{tag}: not restricted-PSS compliant")

# Matrix: (profile sets, profile, namespaces)
cases = [("cluster", [], "cluster", []),
         ("namespace", ["rbac.profile=namespace"], "namespace", ["relns"]),
         ("namespace-explicit", ["rbac.profile=namespace", "rbac.namespaces={ns1}"], "namespace", ["ns1"])]
for name, sets, profile, nss in cases:
    for kv in (False, True):
        for job in (False, True):
            tag = f"{name} kubevirt={kv} job={job}"
            s = sets + [f"rbac.kubevirt={str(kv).lower()}", f"job.enabled={str(job).lower()}"]
            docs, err = render(s)
            if docs is None:
                fail(1, f"{tag}: helm template failed: {err.splitlines()[-1] if err else ''}")
                continue
            bad = {d["kind"] for d in docs} - ALLOWED
            if bad:
                fail(1, f"{tag}: forbidden kinds {sorted(bad)}")
            check_rbac(tag, docs, profile, kv, nss)
            has = any(d["kind"] == "Job" for d in docs)
            if has != job:
                fail(3, f"{tag}: Job rendered={has}, want {job}")
            if job:
                check_job(tag, docs, profile, nss)
# multi-namespace RBAC
docs, err = render(["rbac.profile=namespace", "rbac.namespaces={ns1,ns2}"])
if docs is None:
    fail(2, "ns1,ns2 render failed")
else:
    check_rbac("namespace ns1,ns2", docs, "namespace", False, ["ns1", "ns2"])

# Inputs that must be rejected.
for name, sets in [("bogus profile", ["rbac.profile=bogus"]),
                   ("non-array args", ["job.args=foo"]),
                   ("runAsUser 0", ["job.runAsUser=0"]),
                   ("runAsUser too large", ["job.runAsUser=99999999999"]),
                   ("privileged override", ["job.privileged=true"]),
                   ("cluster profile with namespaces", ["rbac.namespaces={ns1}"]),
                   ("empty namespace name", ["rbac.profile=namespace", "rbac.namespaces={}", "rbac.namespaces[0]="]),
                   ("invalid namespace name", ["rbac.profile=namespace", "rbac.namespaces={Bad_NS}"]),
                   ("job with two namespaces", ["rbac.profile=namespace", "rbac.namespaces={a,b}", "job.enabled=true"]),
                   ("unknown top-level value", ["hostNetwork=true"])]:
    d, _ = render(sets)
    if d is not None:
        fail(5, f"values must be rejected: {name}")

# Lint, both profiles.
for sets in ([], ["rbac.profile=namespace", "rbac.namespaces={ns1,ns2}"]):
    cmd = ["lint", CHART] + [x for s in sets for x in ("--set", s)]
    r = helm(*cmd)
    if r.returncode:
        fail(5, f"helm lint {sets}: {(r.stdout + r.stderr).strip().splitlines()[-1]}")

if fails:
    print(f"{len(fails)} failure(s)"); sys.exit(1)
print("chart invariants OK")
PY

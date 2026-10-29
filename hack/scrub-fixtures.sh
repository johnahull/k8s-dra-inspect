#!/usr/bin/env bash
# Usage: hack/scrub-fixtures.sh <srcdir> <destdir>
# Copies *.yaml as single-List files, drops volatile metadata and renames lab
# hostnames (nodeName / pool / Node names) to node-a.example.com, node-b... in
# first-seen order. Idempotent. Needs python3 with PyYAML.
set -euo pipefail
[ $# -eq 2 ] || { echo "usage: $0 <srcdir> <destdir>" >&2; exit 2; }
exec python3 -I - "$1" "$2" <<'PY'
import re, sys, glob, os, yaml

src, dest = sys.argv[1:]
files = sorted(glob.glob(os.path.join(src, "*.yaml")))
lists = {}
for f in files:
    objs = []
    for doc in yaml.safe_load_all(open(f)):
        if doc:
            objs += doc["items"] if doc.get("kind") == "List" else [doc]
    lists[f] = objs

hosts = []
def add(h):
    if isinstance(h, str) and h and h not in hosts and not h.endswith(".example.com"):
        hosts.append(h)
def walk(o, key=None):
    if isinstance(o, dict):
        if o.get("kind") == "Node" and "metadata" in o:
            add(o["metadata"]["name"])
        for k, v in o.items():
            if k in ("nodeName", "pool") and isinstance(v, str):
                add(v)
            if k == "pool" and isinstance(v, dict):
                add(v.get("name"))
            walk(v, k)
    elif isinstance(o, list):
        for v in o:
            walk(v)
for objs in lists.values():
    walk(objs)

alias = {h: "node-%s.example.com" % chr(ord("a") + i) for i, h in enumerate(hosts)}
prefixes = sorted({(h[:n], h) for h in hosts for n in range(len(h), 11, -1)}, key=lambda p: -len(p[0]))
pat = re.compile("|".join(re.escape(p) for p, _ in prefixes)) if prefixes else None
pmap = dict(prefixes)

def drop(o):
    if isinstance(o, dict):
        m = o.get("metadata")
        if isinstance(m, dict):
            for k in ("uid", "resourceVersion", "creationTimestamp", "ownerReferences", "managedFields", "selfLink"):
                m.pop(k, None)
            a = m.get("annotations")
            if isinstance(a, dict):
                a.pop("kubectl.kubernetes.io/last-applied-configuration", None)
                if not a:
                    del m["annotations"]
        for v in o.values():
            drop(v)
    elif isinstance(o, list):
        for v in o:
            drop(v)

os.makedirs(dest, exist_ok=True)
for f, objs in lists.items():
    drop(objs)
    text = yaml.safe_dump({"apiVersion": "v1", "kind": "List", "items": objs}, sort_keys=True)
    if pat:
        text = pat.sub(lambda m: alias[pmap[m.group(0)]], text)
    open(os.path.join(dest, os.path.basename(f)), "w").write(text)
PY

#!/usr/bin/env bash
# Self-test for scrub-fixtures.sh. Needs python3 + PyYAML (CI: pip install pyyaml==6.0.3).
set -euo pipefail
cd "$(dirname "$0")/.."
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT
mkdir "$t/in"
cat > "$t/in/a.yaml" <<'Y'
apiVersion: v1
kind: List
items:
- kind: ResourceSlice
  metadata: {name: node1-slice}
  spec:
    nodeName: node1
    pool: {name: node1}
    devices:
    - name: d
      attributes:
        uuid: {string: GPU-REAL-SERIAL}
        serialNumber: {string: ABC123}
        other: {string: 8ac3c696-776d-67ac-b1ba-1331a378f423}
- kind: ResourceClaim
  metadata: {name: c, namespace: ns}
  status:
    reservedFor:
    - {resource: pods, name: realpod-xyz, uid: 40d1abda-f8b7-4586-aaba-c0ba5d577a59}
Y
hack/scrub-fixtures.sh "$t/in" "$t/out"
if grep -rE 'node1|GPU-REAL|ABC123|realpod|40d1abda|8ac3c696' "$t/out"; then echo "FAIL: leak" >&2; exit 1; fi
grep -q 'node-a.example.com' "$t/out/a.yaml" || { echo "FAIL: no host alias" >&2; exit 1; }
grep -q 'name: pod-1' "$t/out/a.yaml" || { echo "FAIL: no pod-1" >&2; exit 1; }
if hack/scrub-fixtures.sh "$t/in" "$t/in" 2>/dev/null; then echo "FAIL: same dir accepted" >&2; exit 1; fi
hack/scrub-fixtures.sh "$t/out" "$t/out2"
diff -r "$t/out" "$t/out2" || { echo "FAIL: not idempotent" >&2; exit 1; }
echo ok

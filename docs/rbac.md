# RBAC

dra-inspect is read-only: every rule below grants only `get` and `list`. No
profile grants a mutating verb or `pods/exec`.

## Resources

| API group | Resource | Verbs | Cluster profile | Namespace profile |
|---|---|---|---|---|
| `resource.k8s.io` | resourceslices | get, list | yes | no (reported `unavailable`) |
| `resource.k8s.io` | deviceclasses | get, list | yes | no (reported `unavailable`) |
| `resource.k8s.io` | resourceclaims | get, list | yes | yes |
| core | pods | get, list | yes | yes |
| core | nodes | get, list | yes | no (reported `unavailable`) |
| `kubevirt.io` | virtualmachineinstances | get, list | optional | optional |

In the namespace profile, slice, DeviceClass, and node data is recorded as
`unavailable` (`forbidden` in the snapshot's collection status). dra-inspect
never reports cluster-wide coverage from a namespaced grant: checks that need
ResourceSlices say so instead of guessing.

## Cluster profile

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: dra-inspect
rules:
- apiGroups: ["resource.k8s.io"]
  resources: ["resourceslices", "deviceclasses", "resourceclaims"]
  verbs: ["get", "list"]
- apiGroups: [""]
  resources: ["pods", "nodes"]
  verbs: ["get", "list"]
- apiGroups: ["kubevirt.io"]   # optional
  resources: ["virtualmachineinstances"]
  verbs: ["get", "list"]
```

## Namespace profile

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: dra-inspect
  namespace: <namespace>
rules:
- apiGroups: ["resource.k8s.io"]
  resources: ["resourceclaims"]
  verbs: ["get", "list"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list"]
- apiGroups: ["kubevirt.io"]   # optional
  resources: ["virtualmachineinstances"]
  verbs: ["get", "list"]
```

Run with `-n <namespace>` so claims and pods are listed in that namespace only.

## Version skew

The client libraries are client-go v0.37. They read the `resource.k8s.io/v1`
API, so the target cluster must serve it (Kubernetes 1.34 or newer). Fields a
server does not know about, such as newer attribute list types or consumable
capacity, simply come back empty.

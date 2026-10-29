# classes-claims fixture

Hand-written and synthetic (example.com names only). Covers DeviceClasses with
labels, CEL selectors and opaque config (output must never leak them), a
template-generated claim known only through a Pod's resourceClaimStatuses, an
allocation that references a device absent from the slices, a pending claim,
and a KubeVirt VMI with its virt-launcher pod.

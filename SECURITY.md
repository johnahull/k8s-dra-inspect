# Security policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub security
advisories rather than opening a public issue.

Include the affected version, reproduction steps, and the smallest useful
diagnostic output. Do not include kubeconfigs, tokens, secrets, or unredacted
opaque driver configuration.

## What a snapshot contains

`snapshot capture` removes opaque driver parameters, device status data,
last-applied annotations and `managedFields`, and VMI cloud-init data and
access credentials. Nodes are reduced to name, labels and `status.allocatable`;
VMIs to an allow-list (names, namespaces, UIDs, claim and host-device names,
CPU settings, node name and phase).

CEL selector text is kept in snapshots (DeviceClass and ResourceClaim
selectors) because replay needs it, as are `reservedFor` pod names, Node
labels, and the VMI names, namespaces, UIDs and node names above. Review a
snapshot before sharing it outside the team that owns the cluster.
`--no-redact` keeps full objects and should never be shared. Adapter evidence
(`--with-adapters`) is redacted by its producer.

`dra-inspect` is designed to be read-only, but its output can contain cluster
topology and workload metadata. Review and redact support bundles before
sharing them outside the cluster administration team.

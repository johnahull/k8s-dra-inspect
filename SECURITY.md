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
access credentials. Nodes keep only scheduler-relevant name, labels, taints,
allocatable resources, unschedulable state and Ready status; VMIs keep an
allow-list (names, namespaces, UIDs, claim and host-device names, CPU settings,
node name and phase). Pod projections keep DRA claim references and scheduling
fields. `--with-node-plugins` adds a clusterwide pod projection with hostPath
mounts needed by `inspect nodes`.

Namespace-filtered snapshots store device allocation results from other
namespaces without claim names, pod names, node selectors or opaque config.
`--with-events` adds Events; their messages and pod condition messages are
removed by default. Their minimal involved-object reference is kept so replay
can match an Event to its pod. `check why` also omits those messages unless its
`--no-redact` flag is supplied.

CEL selector text is kept in snapshots (DeviceClass and ResourceClaim
selectors) because replay needs it, and DeviceClass selector text is shown by
`inspect deviceclasses`. Opaque driver parameters remain hidden. The same
applies to `reservedFor` pod names, Node labels, and the VMI names,
namespaces, UIDs and node names above. Review a snapshot before sharing it
outside the team that owns the cluster.
`snapshot capture --no-redact` keeps full objects and should never be shared.
Adapter evidence (`--with-adapters`) is redacted by its producer.

`dra-inspect` is designed to be read-only, but its output can contain cluster
topology and workload metadata. Review and redact support bundles before
sharing them outside the cluster administration team.

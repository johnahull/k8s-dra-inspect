# Security policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub security
advisories rather than opening a public issue.

Include the affected version, reproduction steps, and the smallest useful
diagnostic output. Do not include kubeconfigs, tokens, secrets, or unredacted
opaque driver configuration.

`dra-inspect` is designed to be read-only, but its output can contain cluster
topology and workload metadata. Review and redact support bundles before
sharing them outside the cluster administration team.

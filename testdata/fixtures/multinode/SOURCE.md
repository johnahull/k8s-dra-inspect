# multinode fixture

Hand-written and synthetic (example.com names only). One driver on two nodes
(four devices, so a constant node count is caught), including devices that list
four NUMA nodes (`0,1,2,3`) and a two-node set, distinct PCIe roots, and one
allocated two-device claim.

`pods.yaml` holds the pod the claim is reserved for (same UID as
`reservedFor`), so the reservation is not stale.

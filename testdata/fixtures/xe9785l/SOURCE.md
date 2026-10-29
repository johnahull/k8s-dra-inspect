# xe9785l fixture

Origin: `dra-topology-aware-co-placement/testing/results/xe9785l-mi355x-topology-coordinator-2026-07-01-02/`
(`resourceslices.yaml`, `claim-3numa.yaml`, `all-deviceclasses.yaml`), a real
single-node cluster with CPU, memory, GPU, and SR-IOV NIC drivers.

The claim `claim-3numa.yaml` already carries `status.allocation`, and every
allocated driver/pool/device triple exists in `resourceslices.yaml` (checked by
a test). Nothing was hand-written.

Scrub command (source files copied into one directory first):

    hack/scrub-fixtures.sh <srcdir> testdata/fixtures/xe9785l

# xe9680 fixture

Origin: `dra-topology-aware-co-placement/testing/results/xe9680-hardware/`
(`resourceslices.yaml`, `deviceclasses.yaml`), a real single-node cluster.
There is no claim in this set.

Scrub command (source files copied into one directory first):

    hack/scrub-fixtures.sh <srcdir> testdata/fixtures/xe9680

The script needs python3 with PyYAML (`pip install pyyaml`). It also replaces
UUIDs, `uuid`/`shareID`/`serial*` attributes and `reservedFor` pod identities
with deterministic fakes. `hack/test-scrub.sh` self-tests it.

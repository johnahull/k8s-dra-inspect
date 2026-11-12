# OpenShift Helm Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the optional Helm Job compatible with OpenShift project-assigned UIDs while preserving the existing non-root hardening and explicit UID override.

**Architecture:** Keep the Go CLI and Kubernetes API collection unchanged. Make the Helm Job's pod-level UID/GID fields opt-in; when unset, the image remains non-root and OpenShift SCC admission can assign the project UID range. Extend the existing chart invariant script to prove both the OpenShift-safe default and the explicit override.

**Tech Stack:** Helm v3/v4 templates, Helm JSON schema, Bash, Python/PyYAML chart invariant tests, Markdown documentation.

**Spec:** OpenShift architect assessment from the user request; the repository's existing Helm/RBAC contract in `docs/rbac.md`.

## Global Constraints

- Keep the chart read-only: only `get` and `list` RBAC permissions.
- Keep the Job compatible with restricted security: non-root, RuntimeDefault seccomp, no privilege escalation, read-only root filesystem, and all capabilities dropped.
- Do not add an SCC, privileged access, host mounts, host namespaces, Routes, or ImageStreams.
- Preserve `job.runAsUser` as an explicit escape hatch for clusters that require a fixed non-root UID; render the matching group only when that value is set.
- Do not change the Go API client or add support for a second DRA API version in this compatibility patch; the chart continues to require `resource.k8s.io/v1`.

## Review Focus

- Default Job rendering omits both `runAsUser` and `runAsGroup`, allowing OpenShift SCC UID allocation — chart invariant test.
- Explicit `job.runAsUser=65532` still renders both fields — chart invariant test.
- Invalid UID values remain rejected by the values schema — existing mutation cases plus the new default path.
- Existing security hardening and RBAC remain unchanged — existing chart invariant matrix and full Go test suite.
- Documentation does not imply that the tool installs or configures OpenShift DRA drivers — README review and chart lint.

---

### Task 1: Make Job UID assignment OpenShift-safe

**Files:**
- Modify: `hack/check-chart.sh`
- Modify: `deploy/helm/dra-inspect/templates/job.yaml`
- Modify: `deploy/helm/dra-inspect/values.yaml`
- Modify: `deploy/helm/dra-inspect/values.schema.json`

**Interfaces:**
- Consumes: existing `job.runAsUser` Helm value.
- Produces: a Job that always requests non-root execution, but emits `runAsUser`/`runAsGroup` only when `job.runAsUser` is non-null.

- [ ] **Step 1: Extend the chart invariant test first.**
  Update `check_job` to accept the expected fixed UID (or no fixed UID), assert that the default render omits both pod UID fields, and add a render case with `job.runAsUser=65532` that requires both fields.
- [ ] **Step 2: Run `hack/check-chart.sh` and verify it fails because the current default still renders UID/GID 65532.**
- [ ] **Step 3: Implement the smallest template/value/schema change.**
  Set `job.runAsUser` to `null` by default, allow `integer` or `null` in the schema, and wrap the two pod UID fields in a Helm `with` block. Keep `runAsNonRoot: true` unconditional.
- [ ] **Step 4: Run `hack/check-chart.sh` and verify all chart invariants pass.**
- [ ] **Step 5: Commit the chart compatibility change.**

### Task 2: Document OpenShift usage and boundaries

**Files:**
- Modify: `README.md`
- Modify: `deploy/helm/dra-inspect/values.yaml`

**Interfaces:**
- Consumes: the optional UID behavior from Task 1 and existing `imagePullSecrets`, `rbac.kubevirt`, and DRA API requirements.
- Produces: operator-facing installation guidance for OpenShift without introducing OpenShift-only resources.

- [ ] **Step 1: Add a concise OpenShift section.**
  Document `oc`/Helm installation, project UID allocation, GHCR pull-secret configuration, `rbac.kubevirt=true`, and the requirement that `/apis/resource.k8s.io/v1` be served.
- [ ] **Step 2: Update chart comments and the Job security description to say that UID 65532 is the image default, not a required pod UID.**
- [ ] **Step 3: Run `helm lint`, default and namespace `helm template`, and the chart invariant script.**
- [ ] **Step 4: Commit the documentation change.**

### Task 3: Whole-branch verification and review

**Files:**
- Review all changes from the branch base through `HEAD`.

- [ ] **Step 1: Run `go test ./...`, `go vet ./...`, and all chart checks.**
- [ ] **Step 2: Perform a read-only code review against this plan, focusing on SCC behavior, schema validation, security hardening, and backward compatibility.**
- [ ] **Step 3: Fix any Critical or Important findings with a failing test first, then rerun the full verification suite.**
- [ ] **Step 4: Commit review fixes if needed, push the branch, and open a PR against `main`.**

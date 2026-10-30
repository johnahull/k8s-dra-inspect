package check

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

var (
	numaNames = []string{"numaNode", "numa", "numaNodeID"}
	pcieNames = []string{"pcieRoot"}
	attrName  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// parseMatchAttribute validates a FullyQualifiedName ("domain/name").
func parseMatchAttribute(s string) (domain, name string, err error) {
	// The name pattern excludes "/", so a missing or extra slash fails it.
	domain, name, _ = strings.Cut(s, "/")
	if !attrName.MatchString(name) || len(validation.IsDNS1123Subdomain(domain)) > 0 {
		return "", "", fmt.Errorf("invalid matchAttribute %q: want domain/name", s)
	}
	return domain, name, nil
}

// inScope reports whether an allocation result belongs to a constraint:
// requests are matched exactly or as a "<request>/<subrequest>" prefix, and
// no requests means every result.
func inScope(request string, requests []string) bool {
	if len(requests) == 0 {
		return true
	}
	for _, r := range requests {
		if request == r || strings.HasPrefix(request, r+"/") {
			return true
		}
	}
	return false
}

// intersect returns the values common to every set; nil sets contribute nothing.
func intersect(sets [][]string) []string {
	if len(sets) == 0 {
		return []string{}
	}
	out := slices.Clone(sets[0])
	for _, s := range sets[1:] {
		out = slices.DeleteFunc(out, func(v string) bool { return !slices.Contains(s, v) })
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// rollupRank orders statuses for a claim: error, then fail, unavailable, warn, pass.
// This differs from the exit-code precedence on purpose: the exit code ranks
// unavailable only with --strict, but a claim that could not be verified is
// less alarming than one proven wrong and more than one with a soft warning.
var rollupRank = map[model.Status]int{
	model.StatusError: 4, model.StatusFail: 3, model.StatusUnavailable: 2, model.StatusWarn: 1, model.StatusPass: 0,
}

type finding struct {
	status model.Status
	reason string
}

func worst(fs []finding) model.Status {
	st := model.StatusPass
	for _, f := range fs {
		if rollupRank[f.status] > rollupRank[st] {
			st = f.status
		}
	}
	return st
}

// findingsOf converts fs for Result.Findings; reasons(fs) is their joined text.
func findingsOf(fs []finding) []model.Finding {
	out := make([]model.Finding, len(fs))
	for i, f := range fs {
		out[i] = model.Finding{Status: f.status, Text: f.reason}
	}
	return out
}

func reasons(fs []finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.reason)
	}
	return strings.Join(out, "; ")
}

type allocDev struct {
	name   string // driver/pool/device
	driver string
	req    string
	ref    deviceRef
}

// evalConstraint evaluates one matchAttribute constraint over devs.
func evalConstraint(c resourcev1.DeviceConstraint, devs []allocDev, derived map[string]bool) (map[string]any, finding) {
	attr := string(*c.MatchAttribute)
	det := map[string]any{"attribute": attr, "requests": append([]string{}, c.Requests...)}
	if _, _, err := parseMatchAttribute(attr); err != nil {
		det["status"] = model.StatusError
		return det, finding{model.StatusError, err.Error()}
	}
	if derived[attr] {
		det["status"] = model.StatusUnavailable
		return det, finding{model.StatusUnavailable, attr + ": derived attribute, not evaluated"}
	}
	values := map[string][]string{}
	var sets, typed [][]string
	var missing []string
	for _, d := range devs {
		if d.ref.state == deviceMissing || !inScope(d.req, c.Requests) {
			continue
		}
		a, ok := lookupKey(d.ref.dev.Attributes, d.driver, attr)
		set := model.AttrSet(a)
		if !ok || len(set) == 0 {
			missing = append(missing, d.name)
			continue
		}
		values[d.name] = set
		sets = append(sets, set)
		typed = append(typed, model.AttrTypedSet(a))
	}
	inter := intersect(sets)
	det["valuesByDevice"], det["intersection"] = values, inter
	switch {
	case len(sets) > 0 && len(inter) == 0:
		det["status"] = model.StatusFail
		return det, finding{model.StatusFail, fmt.Sprintf("matchAttribute %s violated: no value common to all devices in scope", attr)}
	case len(sets) > 0 && len(intersect(typed)) == 0:
		det["status"] = model.StatusFail
		return det, finding{model.StatusFail, fmt.Sprintf("matchAttribute %s violated: type mismatch (values are equal as text but differ in type)", attr)}
	case len(missing) > 0:
		det["status"] = model.StatusUnavailable
		return det, finding{model.StatusUnavailable, fmt.Sprintf("%s not published by %s", attr, strings.Join(missing, ", "))}
	}
	det["status"] = model.StatusPass
	return det, finding{}
}

// topoSummary compares one topology attribute (NUMA or PCIe root) across devs.
type topoSummary struct {
	det     map[string]any
	aligned bool
	missing []string
}

func summarize(devs []allocDev, pick func(model.Topology) model.Resolved) (topoSummary, error) {
	values := map[string][]string{}
	var sets [][]string
	var missing []string
	keys := map[string]bool{}
	for _, d := range devs {
		if d.ref.state == deviceMissing {
			continue
		}
		top, err := model.TopologyOf(d.ref.dev, d.driver)
		if err != nil {
			return topoSummary{}, err
		}
		r := pick(top)
		if len(r.Values) == 0 {
			missing = append(missing, d.name)
			continue
		}
		keys[r.Key] = true
		values[d.name] = r.Values
		sets = append(sets, r.Values)
	}
	inter := intersect(sets)
	k := []string{}
	for key := range keys {
		k = append(k, key)
	}
	slices.Sort(k)
	return topoSummary{
		det:     map[string]any{"key": strings.Join(k, ", "), "valuesByDevice": values, "intersection": inter, "aligned": len(inter) > 0 || len(sets) < 2, "missing": append([]string{}, missing...)},
		aligned: len(inter) > 0 || len(sets) < 2, missing: missing,
	}, nil
}

// coveredBy reports whether constraints on one of names cover every found device.
func coveredBy(cons []resourcev1.DeviceConstraint, names []string, devs []allocDev) bool {
	covered := map[string]bool{}
	for _, c := range cons {
		if c.MatchAttribute == nil {
			continue
		}
		_, n, err := parseMatchAttribute(string(*c.MatchAttribute))
		if err != nil || !slices.Contains(names, n) {
			continue
		}
		for _, d := range devs {
			if inScope(d.req, c.Requests) {
				covered[d.name+"|"+d.req] = true
			}
		}
	}
	for _, d := range devs {
		if d.ref.state != deviceMissing && !covered[d.name+"|"+d.req] {
			return false
		}
	}
	return true
}

// derivedNames lists derived attribute names declared by the claim's requests.
func derivedNames(c resourcev1.ResourceClaim) map[string]bool {
	out := map[string]bool{}
	for _, r := range c.Spec.Devices.Requests {
		if r.Exactly != nil {
			for _, d := range r.Exactly.DerivedAttributes {
				out[string(d.Name)] = true
			}
		}
		for _, sub := range r.FirstAvailable {
			for _, d := range sub.DerivedAttributes {
				out[string(d.Name)] = true
			}
		}
	}
	return out
}

func alignClaim(c resourcev1.ResourceClaim, idx deviceIndex) model.Result {
	var devs []allocDev
	var fs []finding
	var deviceNames []string
	for _, r := range c.Status.Allocation.Devices.Results {
		ref := idx.find(r.Driver, r.Pool, r.Device)
		d := allocDev{name: devName(r), driver: r.Driver, req: r.Request, ref: ref}
		devs = append(devs, d)
		deviceNames = append(deviceNames, d.name)
		switch ref.state {
		case deviceMissing:
			fs = append(fs, finding{model.StatusFail, fmt.Sprintf("allocated device %s is not in any ResourceSlice (data inconsistency)", d.name)})
		case deviceStale:
			fs = append(fs, finding{model.StatusWarn, fmt.Sprintf("allocated device %s is only in a stale generation of its pool", d.name)})
		}
	}
	det := map[string]any{"devices": deviceNames}
	cons := []map[string]any{}
	derived := derivedNames(c)
	for _, c := range c.Spec.Devices.Constraints {
		if c.DistinctAttribute != nil {
			fs = append(fs, finding{model.StatusUnavailable, fmt.Sprintf("distinctAttribute %s is not evaluated", *c.DistinctAttribute)})
		}
		if c.MatchAttribute == nil {
			continue
		}
		d, f := evalConstraint(c, devs, derived)
		cons = append(cons, d)
		if f.status != "" {
			fs = append(fs, f)
		}
	}
	det["constraints"] = cons

	if len(devs) >= 2 {
		spec := c.Spec.Devices.Constraints
		if n, err := summarize(devs, func(t model.Topology) model.Resolved { return t.NUMA }); err != nil {
			fs = append(fs, finding{model.StatusUnavailable, err.Error()})
		} else {
			n.det["constrained"] = coveredBy(spec, numaNames, devs)
			det["numa"] = n.det
			if n.det["constrained"] == false {
				if len(n.missing) > 0 {
					fs = append(fs, finding{model.StatusUnavailable, "NUMA node not published by " + strings.Join(n.missing, ", ")})
				}
				if !n.aligned {
					fs = append(fs, finding{model.StatusWarn, "devices span NUMA nodes (no common NUMA node)"})
				}
			}
		}
		if p, err := summarize(devs, func(t model.Topology) model.Resolved { return t.PCIeRoot }); err != nil {
			fs = append(fs, finding{model.StatusUnavailable, err.Error()})
		} else {
			p.det["constrained"] = coveredBy(spec, pcieNames, devs)
			if !p.aligned && p.det["constrained"] == false {
				p.det["note"] = "devices span PCIe roots (informational)"
			}
			det["pcieRoot"] = p.det
		}
	}
	st := worst(fs)
	det["summary"] = fmt.Sprintf("%d devices, %d constraints", len(devs), len(cons))
	res := model.Result{Check: NameAlignment, Status: st, Subject: c.Namespace + "/" + c.Name, Details: det}
	if st != model.StatusPass {
		det["reason"] = reasons(fs)
		res.Findings = findingsOf(fs)
	}
	return res
}

// podClaims lists the claim names a pod resolves to.
func podClaims(p model.PodRef) []string {
	var out []string
	for _, rc := range p.ResourceClaims {
		if rc.ResourceClaimName != nil {
			out = append(out, *rc.ResourceClaimName)
		}
	}
	for _, st := range p.ResourceClaimStatuses {
		if st.ResourceClaimName != nil {
			out = append(out, *st.ResourceClaimName)
		}
	}
	return out
}

// Alignment checks the allocated devices of each claim against its
// matchAttribute constraints, plus NUMA and PCIe-root placement for claims
// whose constraints do not cover them. target is an optional claim name
// ("name" or "ns/name") or pod name; namespace (-n) scopes both. Claim
// status is the worst of its parts (error, fail, unavailable, warn, pass).
func Alignment(target, namespace string) Check {
	return func(_ context.Context, snap model.Snapshot) []model.Result {
		if r, ok := need(snap, NameAlignment, "resourceslices", "resourceclaims"); ok {
			return r
		}
		idx := newDeviceIndex(snap)
		ns, name := namespace, target
		if a, b, ok := strings.Cut(target, "/"); ok {
			ns, name = a, b
		}
		inNS := func(n string) bool { return ns == "" || n == ns }

		var claims []resourcev1.ResourceClaim
		var matched []string
		if target == "" {
			for _, c := range allocated(snap) {
				if inNS(c.Namespace) {
					claims = append(claims, c)
				}
			}
		} else {
			// A name may be a claim, a pod, or both; take the union of their claims.
			want := map[[2]string]bool{}
			for _, c := range snap.ResourceClaims {
				if c.Name == name && inNS(c.Namespace) {
					want[[2]string{c.Namespace, c.Name}] = true
					matched = []string{"claim"}
				}
			}
			podFound := false
			if r, bad := need(snap, NameAlignment, "pods"); bad {
				if len(want) == 0 {
					return r
				}
			} else {
				for _, p := range snap.Pods {
					if p.Name != name || !inNS(p.Namespace) {
						continue
					}
					podFound = true
					for _, cn := range podClaims(p) {
						want[[2]string{p.Namespace, cn}] = true
					}
				}
			}
			if podFound && len(matched) > 0 {
				matched = append(matched, "pod")
			} else {
				matched = nil
			}
			for _, c := range snap.ResourceClaims {
				if want[[2]string{c.Namespace, c.Name}] {
					claims = append(claims, c)
				}
			}
			if len(claims) == 0 {
				msg := "no claim or pod named " + target
				if podFound {
					msg = "pod " + target + " has no ResourceClaims in the snapshot"
				}
				return single(NameAlignment, model.StatusUnavailable, "", msg)
			}
		}
		slices.SortFunc(claims, func(a, b resourcev1.ResourceClaim) int {
			return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
		})
		out := []model.Result{}
		for _, c := range claims {
			if c.Status.Allocation == nil {
				out = append(out, single(NameAlignment, model.StatusUnavailable, c.Namespace+"/"+c.Name, "claim is not allocated")...)
				continue
			}
			out = append(out, alignClaim(c, idx))
		}
		if matched != nil {
			for _, r := range out {
				r.Details["matched"] = matched
			}
		}
		return out
	}
}

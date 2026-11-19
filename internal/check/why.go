package check

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/analyze"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Why explains why a claim is pending or a pod cannot proceed with its claims.
func Why(ctx context.Context, snap model.Snapshot, target string) []model.Result {
	const name = "check.why"
	keys := []string{"resourceslices", "deviceclasses", "nodes", "pods", "resourceclaims"}
	if snap.Namespace != "" {
		keys = append(keys, "globalallocations")
	}
	keys = append(keys, "devicetaintrules")
	if r, ok := need(snap, name, keys...); ok {
		return r
	}
	data := analyze.FromModel(snap)
	parts := strings.Split(target, "/")
	kind, namespace, objectName := "resourceclaim", snap.Namespace, ""
	switch len(parts) {
	case 1:
		objectName = parts[0]
	case 2:
		if isPodKind(parts[0]) {
			kind, objectName = "pod", parts[1]
		} else if parts[0] == "resourceclaim" || parts[0] == "resourceclaims" {
			objectName = parts[1]
		} else {
			namespace, objectName = parts[0], parts[1]
		}
	case 3:
		if !isPodKind(parts[0]) && parts[0] != "resourceclaim" && parts[0] != "resourceclaims" {
			return whyError(target, "use CLAIM, ns/CLAIM, pod/POD, pod/ns/POD, or resourceclaim/CLAIM")
		}
		kind, namespace, objectName = parts[0], parts[1], parts[2]
	default:
		return whyError(target, "use CLAIM, ns/CLAIM, pod/POD, pod/ns/POD, or resourceclaim/CLAIM")
	}
	if isPodKind(kind) {
		pod := findWhyPod(data.Pods, namespace, objectName)
		if pod == nil {
			return whyError(target, fmt.Sprintf("pod %q not found", target))
		}
		mapped := analyze.MapClaims([]*corev1.Pod{pod}, claimsInNamespace(data.ResourceClaims, pod.Namespace))
		var out []model.Result
		for _, use := range mapped.Claims {
			for _, consumer := range use.Consumers {
				if consumer.Pod == pod {
					out = append(out, whyResult(name, analyze.Explain(ctx, data, use.Claim, pod)))
					break
				}
			}
		}
		for _, missing := range mapped.Missing {
			e := &analyze.Explanation{Claim: missing.Pod.Namespace + "/" + missing.ClaimName, Pod: missing.Pod.Namespace + "/" + missing.Pod.Name,
				Verdict: analyze.VerdictNotAllocatable, Summary: fmt.Sprintf("pod claim %q has no ResourceClaim: %s", missing.PodClaim, missing.Reason)}
			if missing.ClaimName == "" {
				e.Claim = missing.Pod.Namespace + "/<from template " + missing.Template + ">"
			}
			out = append(out, whyResult(name, e))
		}
		if len(out) == 0 {
			return whyError(target, "pod has no ResourceClaims")
		}
		return out
	}
	var matches []*resourcev1.ResourceClaim
	for i := range data.ResourceClaims {
		c := data.ResourceClaims[i]
		if c.Name == objectName && (namespace == "" || c.Namespace == namespace) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return whyError(target, fmt.Sprintf("ResourceClaim %q not found", target))
	}
	if len(matches) > 1 {
		return whyError(target, "claim name is ambiguous; use ns/CLAIM or -n")
	}
	claim := matches[0]
	var pod *corev1.Pod
	for _, use := range analyze.MapClaims(data.Pods, []*resourcev1.ResourceClaim{claim}).Claims {
		for _, consumer := range use.Consumers {
			if consumer.Pod != nil {
				pod = consumer.Pod
				break
			}
		}
	}
	return []model.Result{whyResult(name, analyze.Explain(ctx, data, claim, pod))}
}

func isPodKind(kind string) bool { return kind == "pod" || kind == "pods" || kind == "po" }

func whyError(target, reason string) []model.Result {
	return []model.Result{{Check: "check.why", Status: model.StatusError, Subject: target, Details: map[string]any{"reason": reason}}}
}

func whyResult(check string, explanation *analyze.Explanation) model.Result {
	status := model.StatusPass
	switch explanation.Verdict {
	case analyze.VerdictAllocatedPodPending:
		status = model.StatusWarn
	case analyze.VerdictNotAllocatable:
		status = model.StatusFail
	case analyze.VerdictError:
		status = model.StatusError
	}
	return model.Result{Check: check, Status: status, Subject: explanation.Claim,
		Details: map[string]any{"verdict": explanation.Verdict, "summary": explanation.Summary, "explanation": explanation}}
}

func findWhyPod(pods []*corev1.Pod, namespace, name string) *corev1.Pod {
	var found *corev1.Pod
	for _, p := range pods {
		if p.Name == name && (namespace == "" || p.Namespace == namespace) {
			if found != nil {
				return nil
			}
			found = p
		}
	}
	return found
}

func claimsInNamespace(claims []*resourcev1.ResourceClaim, namespace string) []*resourcev1.ResourceClaim {
	var out []*resourcev1.ResourceClaim
	for _, c := range claims {
		if c.Namespace == namespace {
			out = append(out, c)
		}
	}
	return out
}

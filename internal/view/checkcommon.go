package view

import (
	"fmt"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// eachResult prints the banner, then f for every result that is not
// unavailable (those are printed as UNAVAILABLE lines before the view), each
// after a blank line. A pass result without a subject carries only a reason
// ("no shared counters published"): it prints as a neutral note, never a
// check mark, and so does the empty case (none).
func eachResult(p *render.Printer, title string, results []model.Result, none string, f func(r model.Result)) {
	p.Banner(title)
	n := 0
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		n++
		p.Blank()
		if r.Subject == "" && r.Status == model.StatusPass {
			p.Note(0, render.Info, reasonOf(r))
			continue
		}
		f(r)
	}
	if n == 0 {
		p.Blank()
		p.Note(0, render.Info, none)
	}
}

// reasonOf is the result's reason, else its summary.
func reasonOf(r model.Result) string {
	if s := detStr(r, "reason"); s != "" {
		return s
	}
	return detStr(r, "summary")
}

// notes prints one sentence per Finding. A pass Finding is informational (a
// note on a passing result, such as "cannot compare"), so it gets the neutral
// marker, not a check mark. A non-pass result without Findings prints its
// reason. It returns how many sentences it printed.
func notes(p *render.Printer, indent int, r model.Result, extend func(model.Finding) string) int {
	fs := r.Findings
	if len(fs) == 0 && r.Status != model.StatusPass && reasonOf(r) != "" {
		fs = []model.Finding{{Status: r.Status, Text: reasonOf(r)}}
	}
	for _, f := range fs {
		text, st := f.Text, f.Status
		if extend != nil {
			text = extend(f)
		}
		if st == model.StatusPass {
			st = render.Info
		}
		p.Note(indent, st, text)
	}
	return len(fs)
}

func strs(v any) []string {
	s, _ := v.([]string)
	return s
}

func ints(v any) []string {
	var out []string
	for _, n := range func() []int { x, _ := v.([]int); return x }() {
		out = append(out, fmt.Sprint(n))
	}
	return out
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// short is a device's last path segment: driver/pool/device -> device.
func short(full string) string {
	_, last, _ := cutLast(full)
	return last
}

func cutLast(s string) (before, after string, ok bool) {
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return "", s, false
	}
	return s[:i], s[i+1:], true
}

// byDevice lists "device<sep>values" for a valuesByDevice map, devices sorted.
func byDevice(m map[string][]string, one func(dev, vals string) string) []string {
	var out []string
	for _, d := range slices.Sorted(func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}) {
		out = append(out, one(short(d), numSet(m[d])))
	}
	return out
}

// bySet groups devices that share a value set: "on NUMA 0: a, b; on NUMA 1: c".
func bySet(m map[string][]string, prefix string) string {
	groups := map[string][]string{}
	for d, v := range m {
		k := numSet(v)
		groups[k] = append(groups[k], short(d))
	}
	var out []string
	for _, k := range slices.SortedFunc(func(yield func(string) bool) {
		for k := range groups {
			if !yield(k) {
				return
			}
		}
	}, cmpSets) {
		slices.Sort(groups[k])
		out = append(out, "on "+prefix+k+": "+strings.Join(groups[k], ", "))
	}
	return strings.Join(out, "; ")
}

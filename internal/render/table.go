package render

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Truncation selects where an over-wide cell loses text.
type Truncation int

const (
	TruncateTail   Truncation = iota // "head…"
	TruncateMiddle                   // "head…tail", for identifiers whose unique part is last
)

// Column describes one table column. Max 0 means unbounded.
type Column struct {
	Header   string
	Min, Max int
	Truncate Truncation
}

// TableOptions controls layout. Width 0 means 120.
type TableOptions struct {
	Width int
	Color bool
}

const (
	defaultWidth = 120
	gap          = "  "
	ellipsis     = "…"
)

// Fixed narrow widths: the host locale must not change the layout.
var cond = &runewidth.Condition{}

// clean replaces control characters (including ESC, so ANSI sequences
// become inert text), format characters (Cf: bidi overrides, zero-width
// space, ...) and the line/paragraph separators with a visible placeholder.
// Cf includes ZWJ (U+200D), so emoji ZWJ sequences render split; spoofing
// resistance wins over emoji fidelity in a diagnostic tool.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return '�'
		}
		return r
	}, s)
}

func fit(s string, w int, mode Truncation) string {
	if cond.StringWidth(s) > w {
		if mode == TruncateMiddle && w > 1 {
			s = middle(s, w)
		} else {
			s = cond.Truncate(s, w, ellipsis)
		}
	}
	return s + strings.Repeat(" ", max(w-cond.StringWidth(s), 0))
}

// middle returns head+"…"+tail within width w (w > 1), cutting only on rune
// boundaries; combining marks stay with their base and are never orphaned.
func middle(s string, w int) string {
	rs := []rune(s)
	avail := w - cond.StringWidth(ellipsis)
	headW, tailW := (avail+1)/2, avail/2
	i, hw := 0, 0
	for i < len(rs) {
		rw := cond.RuneWidth(rs[i])
		if hw+rw > headW {
			break
		}
		hw += rw
		i++
	}
	j, tw := len(rs), 0
	for j > i {
		rw := cond.RuneWidth(rs[j-1])
		if tw+rw > tailW {
			break
		}
		tw += rw
		j--
	}
	for j < len(rs) && cond.RuneWidth(rs[j]) == 0 { // tail must not start with an orphan mark
		j++
	}
	return string(rs[:i]) + ellipsis + string(rs[j:])
}

// Table prints rows under cols, shrinking the widest columns (down to their
// Min) until the table fits opt.Width.
func Table(w io.Writer, cols []Column, rows [][]string, opt TableOptions) error {
	width := opt.Width
	if width <= 0 {
		width = defaultWidth
	}
	cells := make([][]string, 0, len(rows)+1)
	hdr := make([]string, len(cols))
	for i, c := range cols {
		hdr[i] = clean(c.Header)
	}
	cells = append(cells, hdr)
	for _, r := range rows {
		row := make([]string, len(cols))
		for i := range row {
			if i < len(r) {
				row[i] = clean(r[i])
			}
		}
		cells = append(cells, row)
	}

	widths := make([]int, len(cols))
	floor := make([]int, len(cols))
	total := gapWidth * max(len(cols)-1, 0)
	for i, c := range cols {
		for _, r := range cells {
			widths[i] = max(widths[i], cond.StringWidth(r[i]))
		}
		if c.Max > 0 {
			widths[i] = min(widths[i], c.Max)
		}
		widths[i] = max(widths[i], c.Min)
		floor[i] = min(max(c.Min, 1), widths[i])
		total += widths[i]
	}
	for total > width {
		widest := -1
		for i := range widths {
			if widths[i] > floor[i] && (widest < 0 || widths[i] > widths[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			break // Mins exceed Width; overflow rather than hide columns
		}
		widths[widest]--
		total--
	}

	for n, r := range cells {
		parts := make([]string, len(cols))
		for i := range parts {
			parts[i] = fit(r[i], widths[i], cols[i].Truncate)
		}
		line := strings.TrimRight(strings.Join(parts, gap), " ")
		if n == 0 && opt.Color {
			line = "\x1b[1m" + line + "\x1b[0m"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

const gapWidth = len(gap)

// Unavailable prints one line per unavailable result.
func Unavailable(w io.Writer, results []model.Result) error {
	for _, r := range results {
		if r.Status != model.StatusUnavailable {
			continue
		}
		reason := "no reason given"
		if v, ok := r.Details["reason"]; ok {
			reason = fmt.Sprint(v)
		}
		who := clean(r.Check)
		if r.Subject != "" {
			who += " " + clean(r.Subject)
		}
		if _, err := fmt.Fprintf(w, "UNAVAILABLE: %s: %s\n", who, clean(reason)); err != nil {
			return err
		}
	}
	return nil
}

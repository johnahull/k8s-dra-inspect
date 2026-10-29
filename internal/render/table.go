package render

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Column describes one table column. Max 0 means unbounded.
type Column struct {
	Header   string
	Min, Max int
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
// become inert text) with a visible placeholder.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}

func fit(s string, w int) string {
	if cond.StringWidth(s) > w {
		s = cond.Truncate(s, w, ellipsis)
	}
	return s + strings.Repeat(" ", w-cond.StringWidth(s))
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
			parts[i] = fit(r[i], widths[i])
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
		if _, err := fmt.Fprintf(w, "UNAVAILABLE: %s: %s\n", clean(r.Check), clean(reason)); err != nil {
			return err
		}
	}
	return nil
}

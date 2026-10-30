package render

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func newP(st Style) (*Printer, *bytes.Buffer) {
	var b bytes.Buffer
	return NewPrinter(&b, st), &b
}

func TestMarker(t *testing.T) {
	cases := []struct {
		s          model.Status
		plain, col string
	}{
		{model.StatusPass, "✓", "\x1b[32m✓\x1b[0m"},
		{model.StatusWarn, "!", "\x1b[33m!\x1b[0m"},
		{model.StatusFail, "✗", "\x1b[31m✗\x1b[0m"},
		{model.StatusUnavailable, "?", "\x1b[2m?\x1b[0m"},
		{model.StatusError, "E", "\x1b[31mE\x1b[0m"},
		{Info, "·", "\x1b[2m·\x1b[0m"},
	}
	off, _ := newP(Style{})
	on, _ := newP(Style{Color: true})
	for _, c := range cases {
		if got := off.Marker(c.s).String(); got != c.plain {
			t.Errorf("%s off: got %q want %q", c.s, got, c.plain)
		}
		if got := on.Marker(c.s).String(); got != c.col {
			t.Errorf("%s on: got %q want %q", c.s, got, c.col)
		}
	}
}

func TestBanner(t *testing.T) {
	p, b := newP(Style{})
	p.Banner("Hardware")
	if b.String() != "═══ Hardware ═══\n" {
		t.Errorf("got %q", b.String())
	}
	p, b = newP(Style{Color: true})
	p.Banner("Hardware")
	if b.String() != "\x1b[1;36m═══ Hardware ═══\x1b[0m\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestLineNeverWraps(t *testing.T) {
	p, b := newP(Style{Width: 20})
	long := strings.Repeat("word ", 20)
	p.Line(2, "%s|%d", long, 7)
	want := "  " + long + "|7\n"
	if b.String() != want {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

func TestProseWrapsWithHangingIndent(t *testing.T) {
	p, b := newP(Style{Width: 40})
	p.Prose(2, 4, "devices on NUMA 0,1 and devices on NUMA 2,3 must share a numaNode but do not")
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("not wrapped: %q", b.String())
	}
	for i, l := range lines {
		if w := cond.StringWidth(l); w > 40 {
			t.Errorf("line %d width %d: %q", i, w, l)
		}
		wantPre := "  "
		if i > 0 {
			wantPre = "      "
		}
		if !strings.HasPrefix(l, wantPre) || strings.HasPrefix(l, wantPre+" ") {
			t.Errorf("line %d indent: %q", i, l)
		}
	}
	if strings.Join(strings.Fields(b.String()), " ") != "devices on NUMA 0,1 and devices on NUMA 2,3 must share a numaNode but do not" {
		t.Errorf("text changed: %q", b.String())
	}
}

func TestProseDisplayWidth(t *testing.T) {
	p, b := newP(Style{Width: 40})
	p.Prose(0, 2, strings.Repeat("示例加速器 ", 8)+"café café café")
	for i, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if w := cond.StringWidth(l); w > 40 {
			t.Errorf("line %d width %d: %q", i, w, l)
		}
	}
	if !strings.Contains(b.String(), "café") {
		t.Errorf("combining mark lost: %q", b.String())
	}
	if strings.Count(b.String(), "\n") < 2 {
		t.Errorf("CJK not wrapped: %q", b.String())
	}
}

func TestProseWidthZeroNeverWraps(t *testing.T) {
	p, b := newP(Style{})
	txt := strings.Repeat("word ", 60)
	p.Prose(2, 4, txt)
	if want := "  " + strings.TrimRight(txt, " ") + "\n"; b.String() != want {
		t.Errorf("got %q", b.String())
	}
}

func TestProseOverflowToken(t *testing.T) {
	p, b := newP(Style{Width: 20})
	tok := strings.Repeat("x", 50)
	p.Prose(0, 2, "see "+tok+" now")
	if b.String() != "see\n  "+tok+"\n  now\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestProseWidthIgnoresColour(t *testing.T) {
	p, b := newP(Style{Width: 12, Color: true})
	p.Prose(0, 0, "aaaa bbbb cccc")
	if b.String() != "aaaa bbbb\ncccc\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestColourOnlyWhenEnabled(t *testing.T) {
	p, b := newP(Style{})
	p.Line(0, "%s %s %s %s", p.Bold("a"), p.Dim("b"), p.Marker(model.StatusFail), p.Branch(true))
	p.Banner("x")
	if strings.Contains(b.String(), "\x1b") {
		t.Errorf("colour off emitted ESC: %q", b.String())
	}
	p, b = newP(Style{Color: true})
	p.Line(0, "%s %s", p.Bold("a"), p.Dim("b"))
	if b.String() != "\x1b[1ma\x1b[0m \x1b[2mb\x1b[0m\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestTreeGlyphsAndBlank(t *testing.T) {
	p, b := newP(Style{})
	p.Line(0, "%sa", p.Branch(false))
	p.Line(0, "%sb", p.Branch(true))
	p.Line(0, "%sc", p.Trunk(false))
	p.Line(0, "%sd", p.Trunk(true))
	p.Blank()
	if b.String() != "├─ a\n└─ b\n│  c\n   d\n\n" {
		t.Errorf("got %q", b.String())
	}
}

type strer struct{ s string }

func (s strer) String() string { return s.s }

func TestInjectionNeverReachesOutput(t *testing.T) {
	evil := "a\x1bb\u202ec\u2028d\u2029e\x07f\u200bg\nh"
	for _, color := range []bool{false, true} {
		for _, width := range []int{0, 30} {
			p, b := newP(Style{Color: color, Width: width})
			p.Banner(evil)
			p.Line(1, "x "+evil+" %s %v %s %q", evil, fmt.Errorf("%s", evil), strer{evil}, evil)
			p.Line(0, "%s", p.Bold(evil))
			p.Line(0, "%s", p.Dim(evil))
			p.Prose(1, 1, evil+" "+evil)
			p.Line(0, "%v", []string{evil})
			out := b.String()
			for _, bad := range []string{"\u202e", "\u2028", "\u2029", "\x07", "\u200b"} {
				if strings.Contains(out, bad) {
					t.Errorf("color=%v width=%d: %q reached output: %q", color, width, bad, out)
				}
			}
			stripped := out
			if color { // only the Printer's own SGR sequences may remain
				for _, sgr := range []string{"\x1b[1;36m", "\x1b[1m", "\x1b[2m", "\x1b[0m"} {
					stripped = strings.ReplaceAll(stripped, sgr, "")
				}
			}
			if strings.Contains(stripped, "\x1b") {
				t.Errorf("color=%v width=%d: foreign ESC in %q", color, width, out)
			}
			if got := strings.Count(out, "\n"); got < 5 || strings.Contains(out, "\r") {
				t.Errorf("injected newline split a Line: %q", out)
			}
		}
	}
}

func TestInjectionNoExtraLines(t *testing.T) {
	p, b := newP(Style{})
	p.Line(0, "%s", "one\ntwo")
	if strings.Count(b.String(), "\n") != 1 {
		t.Errorf("got %q", b.String())
	}
}

func TestNoteWrapsUnderTheTextNotTheMarker(t *testing.T) {
	p, b := newP(Style{Width: 24})
	p.Note(2, model.StatusWarn, "devices on NUMA 0,1 and NUMA 2,3 must share a numaNode")
	want := "  ! devices on NUMA 0,1\n    and NUMA 2,3 must\n    share a numaNode\n"
	if b.String() != want {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

func TestNoteColoursMarkerAndCleansText(t *testing.T) {
	p, b := newP(Style{Color: true, Width: 12})
	p.Note(0, model.StatusFail, "bad\x1b[2Jthing here")
	if want := "\x1b[31m✗\x1b[0m bad�[2Jthing\n  here\n"; b.String() != want {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

func TestInfoNoteIsNeutralNotGreen(t *testing.T) {
	p, b := newP(Style{Color: true})
	p.Note(0, Info, "no shared counters published")
	if want := "\x1b[2m·\x1b[0m no shared counters published\n"; b.String() != want || strings.Contains(b.String(), "32m") {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

// The wrap limit is inclusive: a line exactly Width columns wide stays whole.
func TestWrapBoundaryIsInclusive(t *testing.T) {
	cases := []struct {
		name  string
		width int
		run   func(p *Printer)
		want  string
	}{
		{"prose fits", 9, func(p *Printer) { p.Prose(0, 0, "aaaa bbbb") }, "aaaa bbbb\n"},
		{"prose one over", 8, func(p *Printer) { p.Prose(0, 0, "aaaa bbbb") }, "aaaa\nbbbb\n"},
		{"note fits", 11, func(p *Printer) { p.Note(0, model.StatusWarn, "aaaa bbbb") }, "! aaaa bbbb\n"},
		{"note one over", 10, func(p *Printer) { p.Note(0, model.StatusWarn, "aaaa bbbb") }, "! aaaa\n  bbbb\n"},
		{"list fits", 25, func(p *Printer) { p.List(2, "NUMA 0,1", []string{"gpu-0", "gpu-1", "gpu-2"}) }, "  NUMA 0,1: gpu-0, gpu-1,\n    gpu-2\n"},
		{"list one over", 24, func(p *Printer) { p.List(2, "NUMA 0,1", []string{"gpu-0", "gpu-1", "gpu-2"}) }, "  NUMA 0,1: gpu-0,\n    gpu-1, gpu-2\n"},
	}
	for _, c := range cases {
		p, b := newP(Style{Width: c.width})
		c.run(p)
		if b.String() != c.want {
			t.Errorf("%s: got %q want %q", c.name, b.String(), c.want)
		}
	}
}

func TestListOneGroupLineWhenItFits(t *testing.T) {
	p, b := newP(Style{})
	p.List(4, "NUMA 0,1,2,3", []string{"a", "b", "c"})
	if want := "    NUMA 0,1,2,3: a, b, c\n"; b.String() != want {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

func TestListWrapsAt100WhenWidthIsZero(t *testing.T) {
	var items []string
	for i := range 30 {
		items = append(items, fmt.Sprintf("device-%02d", i))
	}
	p, b := newP(Style{})
	p.List(2, "pcieRoot pci0000:00", items)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("not wrapped: %q", b.String())
	}
	for i, l := range lines {
		if w := cond.StringWidth(l); w > 100 || (i < len(lines)-1 && w < 90) {
			t.Errorf("line %d width %d: %q", i, w, l)
		}
		if i > 0 && !strings.HasPrefix(l, "    device-") {
			t.Errorf("line %d has no hanging indent: %q", i, l)
		}
	}
	if !strings.HasPrefix(lines[0], "  pcieRoot pci0000:00: device-00, ") {
		t.Errorf("group line does not start with its key: %q", lines[0])
	}
	if got := strings.Join(strings.Fields(strings.ReplaceAll(strings.Join(lines, " "), ",", " ")), " "); !strings.HasSuffix(got, "device-29") {
		t.Errorf("items lost: %q", got)
	}
}

func TestListDisplayWidthCJKAndCombining(t *testing.T) {
	p, b := newP(Style{Width: 30})
	p.List(2, "NUMA 0", []string{"示例加速器-1", "示例加速器-2", "示例加速器-3", "café-4", "café-5"})
	for i, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if w := cond.StringWidth(l); w > 30 {
			t.Errorf("line %d width %d: %q", i, w, l)
		}
	}
	if !strings.Contains(b.String(), "café-4") || strings.Count(b.String(), "\n") < 2 {
		t.Errorf("got %q", b.String())
	}
}

func TestListEmptySingleAndLongToken(t *testing.T) {
	p, b := newP(Style{Width: 20})
	p.List(2, "NUMA 0", nil)
	p.List(2, "", nil)
	p.List(2, "NUMA 1", []string{"solo"})
	p.List(2, "", []string{"x", "y"})
	tok := strings.Repeat("d", 60)
	p.List(2, "NUMA 2", []string{tok, "e"})
	want := "  NUMA 0: (none)\n  NUMA 1: solo\n  x, y\n  NUMA 2:\n    " + tok + ",\n    e\n"
	if b.String() != want {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

func TestListCleansItemsAndLabel(t *testing.T) {
	p, b := newP(Style{})
	p.List(0, "k\x1b[2J", []string{"a\u202eb", "c d"})
	if want := "k�[2J: a�b, c d\n"; b.String() != want {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

// Only the Printer may mint a Span: the type has an unexported field, so no
// other package can write Span("\x1b[31m...") around the Printer's cleaning.
func TestSpanCannotBeForged(t *testing.T) {
	ty := reflect.TypeFor[Span]()
	if ty.Kind() != reflect.Struct || ty.NumField() != 1 || ty.Field(0).IsExported() {
		t.Errorf("Span must be a struct with one unexported field, got %v", ty)
	}
}

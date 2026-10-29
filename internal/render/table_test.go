package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func TestTableFitsAndTruncates(t *testing.T) {
	cols := []Column{{Header: "NAME", Min: 4}, {Header: "VALUE", Min: 5}}
	rows := [][]string{
		{"short", "示例加速器カード한국어한국어한국어"},
		{strings.Repeat("n", 60), "Café Ünï"},
	}
	var buf bytes.Buffer
	if err := Table(&buf, cols, rows, TableOptions{Width: 30}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("bad output:\n%s", buf.String())
	}
	for _, l := range lines {
		if w := cond.StringWidth(l); w > 30 {
			t.Errorf("width %d > 30: %q", w, l)
		}
		if !utf8.ValidString(l) || strings.HasSuffix(l, " ") {
			t.Errorf("bad line %q", l)
		}
	}
	if !strings.Contains(lines[2], "…") {
		t.Errorf("long cell not truncated with ellipsis: %q", lines[2])
	}
}

func TestTableSanitizesControlChars(t *testing.T) {
	var buf bytes.Buffer
	_ = Table(&buf, []Column{{Header: "A"}}, [][]string{{"x\ny\t\x1b[31mred"}}, TableOptions{})
	if strings.ContainsAny(buf.String(), "\t\x1b") || strings.Count(buf.String(), "\n") != 2 {
		t.Fatalf("control chars leaked: %q", buf.String())
	}
}

func TestTableColorOnlyHeader(t *testing.T) {
	var buf bytes.Buffer
	_ = Table(&buf, []Column{{Header: "A"}}, [][]string{{"x"}}, TableOptions{Color: true})
	if strings.Count(buf.String(), "\x1b") != 2 {
		t.Fatalf("%q", buf.String())
	}
}

func TestTableDefaultWidth(t *testing.T) {
	var buf bytes.Buffer
	_ = Table(&buf, []Column{{Header: "A"}}, [][]string{{strings.Repeat("a", 200)}}, TableOptions{})
	if w := cond.StringWidth(strings.Split(buf.String(), "\n")[1]); w != 120 {
		t.Fatalf("width %d", w)
	}
}

func TestUnavailable(t *testing.T) {
	var buf bytes.Buffer
	_ = Unavailable(&buf, []model.Result{
		{Check: "c", Status: model.StatusPass},
		{Check: "inspect.x", Status: model.StatusUnavailable, Details: map[string]any{"reason": "forbidden"}},
	})
	if buf.String() != "UNAVAILABLE: inspect.x: forbidden\n" {
		t.Fatalf("%q", buf.String())
	}
}

func TestJSONEnvelope(t *testing.T) {
	var buf bytes.Buffer
	name := strings.Repeat("x", 200)
	err := JSON(&buf, "live", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		[]model.Result{{Check: "c", Status: model.StatusPass, Subject: name, Details: map[string]any{"b": 1, "a": 2}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "schemaVersion": "dra-inspect/v1alpha1",
  "generatedAt": "2026-01-02T03:04:05Z",
  "source": "live",
  "results": [
    {
      "check": "c",
      "status": "pass",
      "subject": "` + name + `",
      "details": {
        "a": 2,
        "b": 1
      }
    }
  ]
}
`
	if buf.String() != want {
		t.Fatalf("got:\n%s", buf.String())
	}
	buf.Reset()
	_ = JSON(&buf, "live", time.Time{}, nil)
	var v struct{ Results []any }
	if json.Unmarshal(buf.Bytes(), &v) != nil || v.Results == nil {
		t.Fatalf("results must be [] not null: %s", buf.String())
	}
}

func TestFitMiddle(t *testing.T) {
	cases := []struct {
		name, in string
		w        int
		want     string
	}{
		{"fits", "abcdef", 6, "abcdef"},
		{"even split", "abcdefghij", 6, "abc…ij"},
		{"cjk at boundary", "ab示例cd示例ef", 8, "ab示…ef"},
		{"cjk straddles head", "a示例示例bcd", 4, "a…d"},
		{"combining kept with base", "e\u0301bcdefgh", 5, "e\u0301b…gh"},
		{"combining not orphaned in tail", "abcdefg\u0301h", 4, "ab…h"},
	}
	for _, c := range cases {
		got := fit(c.in, c.w, TruncateMiddle)
		if strings.TrimRight(got, " ") != c.want || cond.StringWidth(got) != c.w {
			t.Errorf("%s: got %q want %q (width %d)", c.name, got, c.want, cond.StringWidth(got))
		}
	}
}

func TestTableMiddleColumn(t *testing.T) {
	var buf bytes.Buffer
	rows := [][]string{{"driver/pool/" + strings.Repeat("a", 30) + "-gpu-0"}, {"driver/pool/" + strings.Repeat("a", 30) + "-gpu-1"}}
	_ = Table(&buf, []Column{{Header: "D", Min: 5, Truncate: TruncateMiddle}}, rows, TableOptions{Width: 20})
	l := strings.Split(buf.String(), "\n")
	if l[1] == l[2] || !strings.HasSuffix(l[1], "gpu-0") || !strings.HasPrefix(l[1], "driver") {
		t.Fatalf("middle truncation lost distinguishing tail:\n%s", buf.String())
	}
}

func TestTableReplacesFormatChars(t *testing.T) {
	var buf bytes.Buffer
	_ = Table(&buf, []Column{{Header: "A"}}, [][]string{{"a\u202eb\u2028c\u2029d\u200be"}}, TableOptions{})
	want := "a�b�c�d�e\n"
	if !strings.HasSuffix(buf.String(), want) || strings.Count(buf.String(), "\n") != 2 {
		t.Fatalf("%q", buf.String())
	}
}

package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

const (
	maxTextColumns    = 110
	pathologicalToken = 60
)

// No line of a text golden for the real-world fixtures is wider than
// maxTextColumns, unless a single unbreakable token at least pathologicalToken
// columns wide (a device, pool or driver name that long, as in unicode-long) is
// the reason; names are never cut, so such a line may be as wide as its token.
func TestTextGoldensHaveShortLines(t *testing.T) {
	cond := &runewidth.Condition{}
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "golden", "*.text"))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := regexp.MustCompile(`xe9785l|xe9680|classes-claims|multinode|unicode-long`)
	n := 0
	for _, f := range files {
		if !fixtures.MatchString(filepath.Base(f)) {
			continue
		}
		n++
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			w := cond.StringWidth(line)
			if w <= maxTextColumns {
				continue
			}
			widest := 0
			for _, tok := range strings.Fields(line) {
				widest = max(widest, cond.StringWidth(tok))
			}
			if widest < pathologicalToken {
				t.Errorf("%s:%d is %d columns (limit %d): %s", filepath.Base(f), i+1, w, maxTextColumns, line)
			}
		}
	}
	if n < 20 {
		t.Fatalf("only %d goldens matched", n)
	}
}

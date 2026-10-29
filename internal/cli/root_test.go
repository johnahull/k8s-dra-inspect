package cli

import (
	"bytes"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewRootCommand(&Options{}, "v1.2.3")
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestVersion(t *testing.T) {
	out, err := run(t, "version")
	if err != nil || strings.TrimSpace(out) != "v1.2.3" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestInvalidOutput(t *testing.T) {
	_, err := run(t, "-o", "xml", "version")
	if err == nil || !strings.Contains(err.Error(), "xml") {
		t.Fatalf("err=%v", err)
	}
	if _, err := run(t, "-o", "json", "version"); err != nil {
		t.Fatal(err)
	}
}

func TestHelpListsGroups(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"inspect", "check", "report", "snapshot", "completion"} {
		if !strings.Contains(out, g) {
			t.Errorf("help missing %q", g)
		}
	}
}

func TestEmptyGroupPrintsHelp(t *testing.T) {
	out, err := run(t, "inspect")
	if err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestCompletionBash(t *testing.T) {
	out, err := run(t, "completion", "bash")
	if err != nil || !strings.Contains(out, "bash completion") {
		t.Fatalf("err=%v", err)
	}
}

func TestColorEnabled(t *testing.T) {
	env := func(v string) func(string) string {
		return func(string) string { return v }
	}
	tests := []struct {
		name    string
		noColor bool
		env     string
		tty     bool
		want    bool
	}{
		{"tty default", false, "", true, true},
		{"flag", true, "", true, false},
		{"NO_COLOR", false, "1", true, false},
		{"non-tty", false, "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ColorEnabled(tt.noColor, env(tt.env), tt.tty); got != tt.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewRootCommand(&Options{}, "v1.2.3", Deps{})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestVersion(t *testing.T) {
	out, err := runCmd(t, "version")
	if err != nil || strings.TrimSpace(out) != "v1.2.3" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestInvalidOutput(t *testing.T) {
	_, err := runCmd(t, "-o", "xml", "version")
	if err == nil || !strings.Contains(err.Error(), "xml") {
		t.Fatalf("err=%v", err)
	}
	if _, err := runCmd(t, "-o", "json", "version"); err != nil {
		t.Fatal(err)
	}
}

func TestHelpListsGroups(t *testing.T) {
	out, err := runCmd(t, "--help")
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
	out, err := runCmd(t, "inspect")
	if err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestCompletionBash(t *testing.T) {
	out, err := runCmd(t, "completion", "bash")
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

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		want       int
		wantStdout bool
	}{
		{"unknown flag", []string{"--bogus"}, 2, false},
		{"invalid output", []string{"-o", "xml", "version"}, 2, false},
		{"unknown command", []string{"bogus"}, 2, false},
		{"version", []string{"version"}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr, "v1", Deps{}); got != tt.want {
				t.Fatalf("exit %d, want %d", got, tt.want)
			}
			if (stdout.Len() > 0) != tt.wantStdout {
				t.Errorf("stdout=%q", stdout.String())
			}
			if (stderr.Len() > 0) != (tt.want != 0) {
				t.Errorf("stderr=%q", stderr.String())
			}
		})
	}
}

// The text view shows current devices (stale slices only as notes); only the
// table lists every slice generation. The help must say which is which.
func TestSlicesHelpDescribesTextAndTable(t *testing.T) {
	out, err := runCmd(t, "inspect", "--help")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "slices") {
			line = l
		}
	}
	if strings.Contains(line, "List every ResourceSlice generation") || !strings.Contains(line, "current") || !strings.Contains(line, "-o table") {
		t.Errorf("slices help: %q", line)
	}
}

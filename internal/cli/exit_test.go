package cli

import (
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func TestExitCode(t *testing.T) {
	p, w, f, u, e := model.StatusPass, model.StatusWarn, model.StatusFail, model.StatusUnavailable, model.StatusError
	tests := []struct {
		name     string
		statuses []model.Status
		strict   bool
		want     int
	}{
		{"empty", nil, false, 0},
		{"pass warn", []model.Status{p, w}, true, 0},
		{"fail", []model.Status{p, f}, false, 1},
		{"error", []model.Status{p, e}, false, 2},
		{"unavailable lenient", []model.Status{p, u}, false, 0},
		{"unavailable strict", []model.Status{p, u}, true, 3},
		{"error beats fail", []model.Status{f, e, u}, true, 2},
		{"fail beats unavailable strict", []model.Status{u, f}, true, 1},
		{"unknown status is error", []model.Status{p, "bogus"}, false, 2},
		{"empty status is error", []model.Status{""}, false, 2},
		{"fail lenient with unavailable", []model.Status{u, f}, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.statuses, tt.strict); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

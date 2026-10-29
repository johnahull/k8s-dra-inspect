package cli

import "github.com/johnahull/k8s-dra-inspect/internal/model"

// ExitCode maps result statuses to a process exit code: error (2) beats
// fail (1) beats unavailable with strict (3) beats 0.
func ExitCode(statuses []model.Status, strict bool) int {
	var fail, unavailable bool
	for _, s := range statuses {
		switch s {
		case model.StatusError:
			return 2
		case model.StatusFail:
			fail = true
		case model.StatusUnavailable:
			unavailable = true
		}
	}
	switch {
	case fail:
		return 1
	case unavailable && strict:
		return 3
	}
	return 0
}

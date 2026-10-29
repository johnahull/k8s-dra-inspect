package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// --timeout bounds the whole collection, not each request.
func TestTimeoutIsOverallDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	kc := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster: {server: %q}\ncontexts:\n- name: x\n  context: {cluster: c, user: u}\nusers:\n- name: u\n  user: {}\ncurrent-context: x\n", srv.URL)
	if err := os.WriteFile(kc, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := Deps{NewSource: func(o *Options) (model.Source, error) { return liveSource(o) }, Now: time.Now}
	for _, c := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"--strict"}, 3}} {
		var out, errb bytes.Buffer
		args := append([]string{"--kubeconfig", kc, "--timeout", "200ms"}, c.args...)
		start := time.Now()
		code := run(append(args, "inspect", "slices"), &out, &errb, "v", deps)
		if el := time.Since(start); el > 500*time.Millisecond {
			t.Errorf("%v: took %v, want < 500ms", c.args, el)
		}
		if code != c.code || !strings.Contains(out.String(), "UNAVAILABLE: inspect.slices: timed out after 200ms") {
			t.Errorf("%v: code=%d out=%q err=%q", c.args, code, out.String(), errb.String())
		}
	}
}

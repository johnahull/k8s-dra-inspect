package host

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// readSmall reads a regular file of at most maxRead bytes and trims it. It
// never follows a final symlink and never opens anything but regular files.
func readSmall(path string) (string, error) { return readCapped(path, maxRead) }

func readCapped(path string, limit int64) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > limit {
		return "", errors.New("file exceeds size cap")
	}
	return strings.TrimSpace(string(b)), nil
}

// confine resolves path and reports whether the result stays inside the
// resolved root, so a symlinked directory cannot lead outside it. For root "/"
// every resolved path qualifies. It returns the resolved path.
func (a *Adapter) confine(path string) (string, bool) {
	root, err := filepath.EvalSymlinks(a.root())
	if err != nil {
		return "", false
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil || (root != "/" && p != root && !strings.HasPrefix(p, root+string(filepath.Separator))) {
		return "", false
	}
	return p, true
}

// scan lists a directory confined to the root; it returns the resolved
// directory and its entries sorted by name, or nothing when it is absent,
// unreadable or outside the root.
func (a *Adapter) scan(path string) (string, []os.DirEntry) {
	dir, ok := a.confine(path)
	if !ok {
		return "", nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	return dir, ents
}

func names(ents []os.DirEntry) []string {
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

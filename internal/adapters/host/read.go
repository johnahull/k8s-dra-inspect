package host

import (
	"errors"
	"io"
	"os"
	"slices"
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

// readDirNames lists a directory's entry names, sorted; errors yield nil.
func readDirNames(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

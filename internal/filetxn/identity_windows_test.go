//go:build windows

package filetxn

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, output)
	}
}

// pinnedLstat returns an Lstat result whose lazily loaded Windows identity is
// already fixed, the way callers hold it after an os.SameFile comparison.
func pinnedLstat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	must(t, err)
	if !os.SameFile(info, info) {
		t.Fatalf("identity is unavailable: %s", path)
	}
	return info
}

func TestDirectoryIdentityWindowsRefusals(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file.txt")
		writeFile(t, path, []byte("x"), 0o600)
		if _, _, ok := directoryIdentity(path, pinnedLstat(t, path)); ok {
			t.Fatal("regular file reported a directory identity")
		}
	})
	t.Run("junction", func(t *testing.T) {
		root := t.TempDir()
		must(t, os.Mkdir(filepath.Join(root, "real"), 0o700))
		makeJunction(t, filepath.Join(root, "link"), filepath.Join(root, "real"))
		info, err := os.Lstat(filepath.Join(root, "link"))
		must(t, err)
		if _, _, ok := directoryIdentity(filepath.Join(root, "link"), info); ok {
			t.Fatal("junction reported a directory identity")
		}
	})
	t.Run("directory replaced by a junction after lstat", func(t *testing.T) {
		root := t.TempDir()
		must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
		must(t, os.Mkdir(filepath.Join(root, "elsewhere"), 0o700))
		stale := pinnedLstat(t, filepath.Join(root, "made"))
		must(t, os.Rename(filepath.Join(root, "made"), filepath.Join(root, "retired")))
		makeJunction(t, filepath.Join(root, "made"), filepath.Join(root, "elsewhere"))
		if _, _, ok := directoryIdentity(filepath.Join(root, "made"), stale); ok {
			t.Fatal("junction replacement reported the stale directory identity")
		}
	})
	t.Run("directory replaced by another directory after lstat", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "made")
		must(t, os.Mkdir(path, 0o700))
		stale := pinnedLstat(t, path)
		replaceDirectoryWithDistinctIdentity(t, root, "made")
		if _, _, ok := directoryIdentity(path, pinnedLstat(t, path)); !ok {
			t.Fatal("fresh lstat of the replacement has no identity")
		}
		if _, _, ok := directoryIdentity(path, stale); ok {
			t.Fatal("stale lstat reported an identity for the replacement")
		}
	})
	// The Go runtime opts every process into long-path support at startup
	// (initLongPathSupport sets the PEB flag on Windows 10 1703 and later),
	// and the os package then skips its own \\?\ prefix, so os.MkdirAll and
	// syscall.CreateFile resolve long paths the same way regardless of the
	// LongPathsEnabled registry value.
	t.Run("path longer than MAX_PATH", func(t *testing.T) {
		path := t.TempDir()
		for len(path) <= 300 {
			path = filepath.Join(path, strings.Repeat("d", 40))
		}
		must(t, os.MkdirAll(path, 0o700))
		if _, _, ok := directoryIdentity(path, pinnedLstat(t, path)); !ok {
			t.Fatalf("long path (%d chars) has no identity", len(path))
		}
	})
}

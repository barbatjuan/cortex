package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// directoryLifecycleInstall installs the shipped catalog into a target
// directory through the production dispatch, so lifecycle tests start from a
// real installed state.
func directoryLifecycleInstall(t *testing.T, home, target string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"install", "--target-dir", target}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK {
		t.Fatalf("install exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestUpdateDirectoryTargetPlansAndApplies(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "agent-skills")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	directoryLifecycleInstall(t, home, target)
	fixture := newUpdateFixture(t)

	skill := func() string {
		data, err := os.ReadFile(filepath.Join(target, "skills", "cortex-requirements-analyst", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	before := skill()

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"update", "--target-dir", target, "--catalog", fixture.patched}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("plan exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "runtime=directory") || !strings.Contains(stdout.String(), "mode=plan") || !strings.Contains(stdout.String(), "status=planned") {
		t.Fatalf("plan output = %q", stdout.String())
	}
	if skill() != before {
		t.Fatal("a read-only plan must not write")
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithDependencies(context.Background(), []string{"update", "--target-dir", target, "--catalog", fixture.patched, "--apply"}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("apply exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status=applied") || !strings.Contains(stdout.String(), "runtime=directory") {
		t.Fatalf("apply output = %q", stdout.String())
	}
	if skill() == before {
		t.Fatal("apply must refresh the projected skill")
	}
}

func TestUpdateDirectoryTargetFailsClosedOnUnknownState(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "never-installed")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"update", "--target-dir", target, "--catalog", "../../catalog"}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code == exitOK || !strings.Contains(stdout.String(), "status=not_applied") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestUninstallDirectoryTargetRemovesOnlyOwnedFiles(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "agent-skills")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	directoryLifecycleInstall(t, home, target)
	sentinel := filepath.Join(target, "foreign-sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("user bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"uninstall", "--target-dir", target}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "runtime=directory uninstall=completed") {
		t.Fatalf("uninstall output = %q", stdout.String())
	}
	remaining, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	// Only the foreign file, the shared skills directory, and the .cortex
	// directory holding the uninstall backup may remain: every Cortex-owned
	// skill and the state file are gone.
	names := map[string]bool{}
	for _, entry := range remaining {
		names[entry.Name()] = true
	}
	if !names["foreign-sentinel.txt"] || !names["skills"] || !names[".cortex"] || len(names) != 3 {
		t.Fatalf("uninstall must keep foreign files and remove owned ones, left: %v", remaining)
	}
	if _, err := os.Stat(filepath.Join(target, ".cortex", "install-state.json")); !os.IsNotExist(err) {
		t.Fatalf("state file must be removed: %v", err)
	}
	// The uninstall transaction removes files, not directories (no directory
	// removal authority): empty cortex-* directories may remain, but no owned
	// file may survive anywhere under skills/.
	leftover, err := filepath.Glob(filepath.Join(target, "skills", "cortex-*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftover) != 0 {
		t.Fatalf("owned skill files must be removed, left: %v", leftover)
	}
}

// TestDirectoryTargetCommandsRefuseRelativeTargets pins issue #372: every
// command that consumes --target-dir refuses a path install would refuse,
// with the same diagnostic and exit code, and writes nothing.
func TestDirectoryTargetCommandsRefuseRelativeTargets(t *testing.T) {
	const existing = "test-relative-target-372"
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(existing) })
	if err := os.WriteFile(filepath.Join(existing, "foreign-sentinel.txt"), []byte("user bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	listing := func() []string {
		var names []string
		err := filepath.WalkDir(existing, func(path string, _ os.DirEntry, err error) error {
			names = append(names, path)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return names
	}
	before := listing()
	// Extra arguments make each command reach its target check: update gets a
	// valid catalog and --apply so a refusal cannot come from the catalog.
	commands := map[string][]string{
		"install":   nil,
		"doctor":    nil,
		"update":    {"--catalog", "../../catalog", "--apply"},
		"uninstall": nil,
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// unclean is absolute and names the existing directory, so only the clean
	// check can refuse it.
	unclean := cwd + string(filepath.Separator) + "." + string(filepath.Separator) + existing
	targets := map[string]string{"existing": existing, "missing": "relative/missing-target", "unclean": unclean}
	for command, extra := range commands {
		for name, target := range targets {
			t.Run(command+"/"+name, func(t *testing.T) {
				home := canonicalHome(t)
				var stdout, stderr bytes.Buffer
				args := append([]string{command, "--target-dir", target}, extra...)
				code := runWithDependencies(context.Background(), args, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
				if code != exitUsage || stdout.Len() != 0 || stderr.String() != "error=invalid_target\n" {
					t.Fatalf("exit = %d stdout = %q stderr = %q, want %d \"\" %q", code, stdout.String(), stderr.String(), exitUsage, "error=invalid_target\n")
				}
				if after := listing(); strings.Join(after, "\n") != strings.Join(before, "\n") {
					t.Fatalf("relative target changed: %q, want %q", after, before)
				}
				if _, err := os.Stat("relative"); !os.IsNotExist(err) {
					t.Fatalf("missing relative target was created: %v", err)
				}
			})
		}
	}
}

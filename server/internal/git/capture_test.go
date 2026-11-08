package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type testRepo struct {
	t    *testing.T
	root string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &testRepo{t: t, root: filepath.Join(root, "repo")}
	if err := os.Mkdir(r.root, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(path, content string) {
	r.t.Helper()
	abs := filepath.Join(r.root, path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) commitAll(message string) string {
	r.git("add", "-A")
	r.git("commit", "-q", "-m", message)
	return r.git("rev-parse", "HEAD")
}

func filesByPath(s Snapshot) map[string]File {
	m := map[string]File{}
	for _, f := range s.Files {
		m[f.Path] = f
	}
	return m
}

func TestCaptureCoversCommittedStagedUnstagedAndUntrackedChanges(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	r.write("a.txt", "a1\n")
	r.write("b.txt", "b1\n")
	r.write("c.txt", "c1\n")
	r.write("rename_me.txt", "same content\nacross rename\n")
	r.write("bin.dat", "bin\x00ary")
	r.write(".gitignore", "*.log\n")
	if err := os.Symlink("a.txt", filepath.Join(r.root, "link")); err != nil {
		t.Fatal(err)
	}
	forkPoint := r.commitAll("base")

	r.git("switch", "-q", "-c", "feature")
	r.write("a.txt", "a2\n")
	r.write("bin.dat", "bin\x00ary2")
	r.commitAll("feature commit")

	r.git("switch", "-q", "main")
	r.write("z.txt", "only on main\n")
	r.commitAll("main moves on")
	r.git("switch", "-q", "feature")

	r.write("new.txt", "staged\n")
	r.git("add", "new.txt")
	r.git("mv", "rename_me.txt", "renamed.txt")
	r.write("b.txt", "b2 unstaged\n")
	if err := os.Remove(filepath.Join(r.root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(r.root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b.txt", filepath.Join(r.root, "link")); err != nil {
		t.Fatal(err)
	}
	r.write("u.txt", "untracked\n")
	r.write("debug.log", "ignored\n")

	s, err := Capture(ctx, r.root, Options{BaseRef: "main", IncludeUntracked: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseSHA != forkPoint {
		t.Fatalf("base = %s, want merge-base %s", s.BaseSHA, forkPoint)
	}
	if !s.Consistent || s.Branch != "feature" {
		t.Fatalf("consistent=%v branch=%q", s.Consistent, s.Branch)
	}
	files := filesByPath(s)
	want := map[string]FileStatus{
		"a.txt": StatusModified, "b.txt": StatusModified, "c.txt": StatusDeleted, "new.txt": StatusAdded,
		"renamed.txt": StatusRenamed, "bin.dat": StatusModified, "link": StatusModified, "u.txt": StatusUntracked,
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v", keys(files))
	}
	for path, status := range want {
		if files[path].Status != status {
			t.Errorf("%s status = %q, want %q", path, files[path].Status, status)
		}
	}
	if got := string(files["b.txt"].Head.Data); got != "b2 unstaged\n" {
		t.Errorf("b.txt head = %q", got)
	}
	if got := string(files["a.txt"].Base.Data); got != "a1\n" {
		t.Errorf("a.txt base = %q", got)
	}
	if f := files["c.txt"]; f.Head != nil || string(f.Base.Data) != "c1\n" {
		t.Errorf("deleted file = %+v", f)
	}
	if f := files["renamed.txt"]; f.OldPath != "rename_me.txt" || f.Base == nil || f.Head == nil {
		t.Errorf("rename = %+v", f)
	}
	if f := files["bin.dat"]; !f.Binary || f.Base != nil || f.Head != nil || f.Omitted != OmittedBinary {
		t.Errorf("binary = %+v", f)
	}
	if f := files["link"]; f.HeadMode != modeSymlink || string(f.Head.Data) != "b.txt" {
		t.Errorf("symlink = %+v", f)
	}
	if f := files["a.txt"]; f.HeadOID != r.git("hash-object", "a.txt") {
		t.Errorf("head oid %s does not match git", f.HeadOID)
	}
	if s.StagedCount != 2 || s.UnstagedCount != 3 || s.UntrackedCount != 1 {
		t.Errorf("counts staged=%d unstaged=%d untracked=%d", s.StagedCount, s.UnstagedCount, s.UntrackedCount)
	}

	s, err = Capture(ctx, r.root, Options{BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filesByPath(s)["u.txt"]; ok {
		t.Error("untracked file captured although excluded")
	}
	if !containsWarning(s, "1 untracked files not included") {
		t.Errorf("warnings = %v", s.Warnings)
	}
}

func TestCaptureOmitsLargeFiles(t *testing.T) {
	r := newTestRepo(t)
	r.write("big.txt", "small\n")
	r.commitAll("base")
	r.write("big.txt", strings.Repeat("x", 100))

	s, err := Capture(context.Background(), r.root, Options{BaseRef: "main", MaxFileBytes: 50})
	if err != nil {
		t.Fatal(err)
	}
	f := s.Files[0]
	if f.Omitted != OmittedTooLarge || f.Base != nil || f.Head != nil || f.HeadOID != r.git("hash-object", "big.txt") {
		t.Fatalf("large file = %+v", f)
	}
}

func TestCaptureRetriesWhenWorktreeChangesThenSucceeds(t *testing.T) {
	r := newTestRepo(t)
	r.write("a.txt", "a1\n")
	r.commitAll("base")
	r.write("a.txt", "a2\n")

	opts := Options{BaseRef: "main", afterRead: func(attempt int) {
		if attempt == 1 {
			r.write("a.txt", "a3\n")
		}
	}}
	s, err := Capture(context.Background(), r.root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Consistent || string(s.Files[0].Head.Data) != "a3\n" {
		t.Fatalf("consistent=%v head=%q", s.Consistent, s.Files[0].Head.Data)
	}
}

func TestCaptureMarksSnapshotInconsistentWhenWorktreeKeepsChanging(t *testing.T) {
	r := newTestRepo(t)
	r.write("a.txt", "a1\n")
	r.commitAll("base")

	opts := Options{BaseRef: "main", afterRead: func(attempt int) {
		r.write("a.txt", strings.Repeat("x", attempt)+"\n")
	}}
	s, err := Capture(context.Background(), r.root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if s.Consistent || !containsWarning(s, "changed during capture") {
		t.Fatalf("consistent=%v warnings=%v", s.Consistent, s.Warnings)
	}
}

func TestCaptureRejectsOptionLikeBaseRef(t *testing.T) {
	r := newTestRepo(t)
	r.write("a.txt", "a\n")
	r.commitAll("base")
	if _, err := Capture(context.Background(), r.root, Options{BaseRef: "--output=/tmp/x"}); err == nil {
		t.Fatal("expected invalid ref error")
	}
}

func TestCaptureRejectsRepositoryWithoutCommits(t *testing.T) {
	r := newTestRepo(t)
	if _, err := Capture(context.Background(), r.root, Options{BaseRef: "main"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadWorktreeFileRefusesSymlinkedParentOutsideRepo(t *testing.T) {
	r := newTestRepo(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.root, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorktreeFile(r.root, "out/secret", DefaultMaxFileBytes, nil); err == nil {
		t.Fatal("expected escape error")
	}
}

func TestResolveRoot(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	r.write("sub/a.txt", "a\n")
	r.commitAll("base")
	allowed := filepath.Dir(r.root)

	got, err := ResolveRoot(ctx, filepath.Join(r.root, "sub"), []string{allowed})
	if err != nil || got != r.root {
		t.Fatalf("ResolveRoot(sub) = %q, %v", got, err)
	}
	if _, err := ResolveRoot(ctx, r.root, nil); !errors.Is(err, ErrOutsideAllowedRoots) {
		t.Fatalf("no allowed roots: %v", err)
	}
	other := t.TempDir()
	if _, err := ResolveRoot(ctx, r.root, []string{other}); !errors.Is(err, ErrOutsideAllowedRoots) {
		t.Fatalf("outside allowed root: %v", err)
	}
	link := filepath.Join(other, "link")
	if err := os.Symlink(r.root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoot(ctx, link, []string{other}); !errors.Is(err, ErrOutsideAllowedRoots) {
		t.Fatalf("symlink escape: %v", err)
	}
	notRepo := filepath.Join(allowed, "plain")
	if err := os.Mkdir(notRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoot(ctx, notRepo, []string{notRepo}); err == nil {
		t.Fatal("expected error for non-repository")
	}
}

func containsWarning(s Snapshot, substr string) bool {
	for _, w := range s.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func keys(m map[string]File) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

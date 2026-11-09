package git

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ResolveRoot validates a non-bare Git worktree and returns its canonical top-level directory.
// Filesystem authority belongs to the authenticated server caller, not a discovery-root list.
func ResolveRoot(ctx context.Context, path string) (string, error) {
	resolved, err := canonical(path)
	if err != nil {
		return "", err
	}
	if bare, err := runString(ctx, resolved, "rev-parse", "--is-bare-repository"); err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	} else if bare == "true" {
		return "", errors.New("bare repositories are not supported")
	}
	top, err := runString(ctx, resolved, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git work tree: %w", err)
	}
	if top, err = canonical(top); err != nil {
		return "", err
	}
	return top, nil
}

func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// within reports whether path equals root or lies below it. Both must be canonical.
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

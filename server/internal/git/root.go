package git

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutsideAllowedRoots reports a repository path outside every configured allowed root.
var ErrOutsideAllowedRoots = errors.New("path is outside the configured allowed roots")

// ResolveRoot returns the canonical top-level directory of the repository containing path.
// Both path and the resulting top level must lie inside one of allowedRoots after resolving symlinks.
func ResolveRoot(ctx context.Context, path string, allowedRoots []string) (string, error) {
	if len(allowedRoots) == 0 {
		return "", fmt.Errorf("%w: no workspace_root_paths configured", ErrOutsideAllowedRoots)
	}
	canonicalAllowed := make([]string, 0, len(allowedRoots))
	for _, root := range allowedRoots {
		resolved, err := canonical(root)
		if err != nil {
			return "", fmt.Errorf("allowed root %s: %w", root, err)
		}
		canonicalAllowed = append(canonicalAllowed, resolved)
	}

	resolved, err := canonical(path)
	if err != nil {
		return "", err
	}
	if !withinAny(resolved, canonicalAllowed) {
		return "", ErrOutsideAllowedRoots
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
	if !withinAny(top, canonicalAllowed) {
		return "", ErrOutsideAllowedRoots
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

func withinAny(path string, roots []string) bool {
	for _, root := range roots {
		if within(path, root) {
			return true
		}
	}
	return false
}

// within reports whether path equals root or lies below it. Both must be canonical.
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

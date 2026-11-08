// Package git captures repository state by running the git CLI with fixed arguments.
// Callers never supply raw arguments; every command is built here.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// gitEnvBlocklist removes variables that would redirect git to another repository or index.
var gitEnvBlocklist = []string{
	"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES=", "GIT_COMMON_DIR=", "GIT_NAMESPACE=",
	"GIT_CEILING_DIRECTORIES=", "GIT_DISCOVERY_ACROSS_FILESYSTEM=",
}

func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		blocked := false
		for _, prefix := range gitEnvBlocklist {
			if strings.HasPrefix(kv, prefix) {
				blocked = true
				break
			}
		}
		if !blocked {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

// run executes git in root and returns stdout.
func run(ctx context.Context, root string, stdin []byte, args ...string) ([]byte, error) {
	full := append([]string{"-C", root, "-c", "core.fsmonitor=false", "-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = gitEnv()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func runString(ctx context.Context, root string, args ...string) (string, error) {
	out, err := run(ctx, root, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// splitNUL splits NUL-terminated output, dropping the trailing empty element.
func splitNUL(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	parts := strings.Split(string(out), "\x00")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

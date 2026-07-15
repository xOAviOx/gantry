package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/avishuklacode/gantry/services/controld/internal/logs"
)

var excludedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".next":        true,
}

// fetch materializes the build context into dst and returns the resolved commit
// SHA and message. A local directory repo_url is copied (dev); anything else is
// shallow-cloned with git.
func fetch(ctx context.Context, req Request, dst string, sink logs.Sink) (sha, msg string, err error) {
	if isLocalDir(req.RepoURL) {
		sink.System(fmt.Sprintf("copying local context from %s", req.RepoURL))
		if err := copyTree(req.RepoURL, dst); err != nil {
			return "", "", fmt.Errorf("copy local context: %w", err)
		}
		sha = req.SHA
		if sha == "" {
			sha = "local"
		}
		return sha, "local build", nil
	}
	return cloneGit(ctx, req, dst, sink)
}

func isLocalDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if d.IsDir() {
			if excludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func cloneGit(ctx context.Context, req Request, dst string, sink logs.Sink) (sha, msg string, err error) {
	branch := req.Branch
	if branch == "" {
		branch = "main"
	}
	sink.System(fmt.Sprintf("git clone --depth 1 --branch %s %s", branch, req.RepoURL))
	if err := runGit(ctx, sink, "clone", "--depth", "1", "--branch", branch, req.RepoURL, dst); err != nil {
		return "", "", fmt.Errorf("git clone: %w", err)
	}
	if req.SHA != "" {
		// Best-effort checkout of a specific commit (needs it to be fetchable).
		_ = runGit(ctx, sink, "-C", dst, "fetch", "--depth", "1", "origin", req.SHA)
		if err := runGit(ctx, sink, "-C", dst, "checkout", req.SHA); err != nil {
			return "", "", fmt.Errorf("git checkout %s: %w", req.SHA, err)
		}
	}
	sha = strings.TrimSpace(gitOutput(ctx, dst, "rev-parse", "HEAD"))
	msg = strings.TrimSpace(gitOutput(ctx, dst, "log", "-1", "--pretty=%s"))
	return sha, msg, nil
}

func runGit(ctx context.Context, sink logs.Sink, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = sink.StreamWriter("git")
	cmd.Stderr = sink.StreamWriter("git")
	return cmd.Run()
}

func gitOutput(ctx context.Context, dir string, args ...string) string {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ShellResult struct {
	ExitCode int
	Output   string
}

// Shell runs script with sh in dir under macOS Seatbelt, because the verify
// command runs code the model wrote: no outbound network but localhost, no
// keychain (so no gh or git credentials), writes only to dir, the cache and
// TMPDIR, and none of DenyRead. A non-zero exit is a result, not an error.
func (r Runner) Shell(ctx context.Context, dir, script string, timeout time.Duration) (ShellResult, error) {
	if r.Cache == "" {
		return ShellResult{}, errors.New("runner needs a cache directory")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := r.childEnv()
	profile, err := seatbelt([]string{dir, r.Cache, tempDir(env)}, r.DenyRead)
	if err != nil {
		return ShellResult{}, err
	}
	cmd := r.command(ctx, dir, "/usr/bin/sandbox-exec", "-p", profile, "/bin/sh", "-c", script)
	cmd.Env = withoutVar(env, "SSH_AUTH_SOCK")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = runGroup(ctx, cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		return ShellResult{ExitCode: exit.ExitCode(), Output: out.String()}, nil
	}
	if err != nil {
		return ShellResult{}, err
	}
	return ShellResult{Output: out.String()}, nil
}

// seatbelt builds the Shell profile. Later rules override earlier ones.
func seatbelt(writable, denyRead []string) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	b.WriteString("(deny network-outbound)\n(allow network-outbound (remote ip \"localhost:*\"))\n")
	b.WriteString("(deny mach-lookup (global-name \"com.apple.SecurityServer\") (global-name \"com.apple.securityd\"))\n")
	b.WriteString("(deny file-write*)\n(allow file-write* (subpath \"/dev\")")
	for _, dir := range writable {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", fmt.Errorf("sandbox path %s: %w", dir, err)
		}
		b.WriteString(" (subpath " + strconv.Quote(real) + ")")
	}
	b.WriteString(")\n")
	for _, dir := range denyRead {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		b.WriteString("(deny file-read* (subpath " + strconv.Quote(dir) + "))\n")
	}
	return b.String(), nil
}

func withoutVar(env []string, name string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return out
}

package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CheckGH fails unless the gh binary reports a logged-in user.
func CheckGH(ctx context.Context, gh string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gh, "auth", "status").CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh is not ready (%v): %s\ninstall gh and run `gh auth login`", err, strings.TrimSpace(string(out)))
	}
	return nil
}

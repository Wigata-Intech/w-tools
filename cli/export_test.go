package cli

import (
	"context"
	"io"
)

// SetIO redirects a Command's output streams for tests.
func SetIO(c *Command, stdout, stderr io.Writer) {
	c.stdout, c.stderr = stdout, stderr
}

// ExecuteArgs runs Execute with explicit args instead of os.Args.
func ExecuteArgs(ctx context.Context, c *Command, args []string) int {
	return c.execute(ctx, args)
}

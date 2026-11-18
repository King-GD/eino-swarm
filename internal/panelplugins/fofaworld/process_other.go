//go:build !unix && !windows

package fofaworld

import (
	"context"
	"os/exec"
)

func runCommand(context.Context, *exec.Cmd) error { return failure("unsupported_platform") }

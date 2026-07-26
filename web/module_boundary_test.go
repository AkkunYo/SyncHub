package web

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoListExcludesNodeModules(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, "go", "list", "./...")
	command.Dir = filepath.Clean("..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, output)
	}

	for _, packagePath := range strings.Fields(string(output)) {
		if strings.Contains(packagePath, "/web/node_modules/") {
			t.Fatalf("go list ./... included frontend dependency %q", packagePath)
		}
	}
}

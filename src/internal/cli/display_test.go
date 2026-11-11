// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"path/filepath"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/display"
)

func TestCLIDisplayPathUsesCurrentDirectoryPolicy(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "snowops")
	t.Setenv("HOME", home)
	got := display.Text("bash "+filepath.Join(root, "scenarios", "demo", "run.sh"), root, "", root)
	if got != "bash scenarios/demo/run.sh" {
		t.Errorf("displayed command = %q", got)
	}
}

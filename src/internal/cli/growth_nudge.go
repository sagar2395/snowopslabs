// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/snowops-oss/snowopslabs/internal/config"
)

// growthNudgeMarker flags that printSignupNudge has already run on this
// machine. Its presence, not its content, is what matters.
const growthNudgeMarker = "growth-nudge-shown"

// printSignupNudge prints a one-line pointer to the site's email signup the
// first time any of scenario/incident/challenge/learn reports a genuine
// success, then never again on this machine. Scenario and incident checks in
// particular can pass many times in one session, so this is gated on a
// marker file rather than printed on every call - once is a nudge, every
// time is a nag.
//
// Failures reading or writing the marker are not fatal to the command that
// called this: at worst the nudge shows again next time, or not at all.
func printSignupNudge(out io.Writer) {
	home, err := config.Home()
	if err != nil {
		return
	}
	marker := filepath.Join(home, growthNudgeMarker)
	if _, err := os.Stat(marker); err == nil {
		return
	}

	fmt.Fprintln(out, "\nLike finding these? The Kubernetes Incident Response Field Guide has the fix for more of them: https://labs.snowops.net/#updates")

	if err := os.MkdirAll(home, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(marker, []byte{}, 0o644)
}

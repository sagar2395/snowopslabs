// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// Challenges and learning paths must always use the default workload, so
// their scores stay comparable.
func TestPinsWorkload(t *testing.T) {
	newTree := func(parent string, child string) *cobra.Command {
		p := &cobra.Command{Use: parent}
		c := &cobra.Command{Use: child}
		p.AddCommand(c)
		return c
	}

	tests := []struct {
		name string
		cmd  *cobra.Command
		want bool
	}{
		{name: "challenge start is pinned", cmd: newTree("challenge", "start"), want: true},
		{name: "challenge submit is pinned", cmd: newTree("challenge", "submit"), want: true},
		{name: "learn start is pinned", cmd: newTree("learn", "start"), want: true},
		{name: "the challenge command itself is pinned", cmd: &cobra.Command{Use: "challenge"}, want: true},
		{name: "scenario up is bindable", cmd: newTree("scenario", "up"), want: false},
		{name: "incident inject is bindable", cmd: newTree("incident", "inject"), want: false},
		{name: "app verify is bindable", cmd: newTree("app", "verify"), want: false},
		{name: "a bare command is bindable", cmd: &cobra.Command{Use: "status"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pinsWorkload(tc.cmd); got != tc.want {
				t.Errorf("pinsWorkload(%q) = %v, want %v", tc.cmd.CommandPath(), got, tc.want)
			}
		})
	}
}

// A deeply nested subcommand must still be pinned — the check walks to the root
// rather than looking only at the immediate parent.
func TestPinsWorkloadWalksToRoot(t *testing.T) {
	root := &cobra.Command{Use: "labctl"}
	ch := &cobra.Command{Use: "challenge"}
	sub := &cobra.Command{Use: "hint"}
	deep := &cobra.Command{Use: "next"}
	root.AddCommand(ch)
	ch.AddCommand(sub)
	sub.AddCommand(deep)
	if !pinsWorkload(deep) {
		t.Errorf("pinsWorkload(%q) = false, want true", deep.CommandPath())
	}
}

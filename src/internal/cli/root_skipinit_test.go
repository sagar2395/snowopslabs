// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// A leaf command name like "list" is shared by many parents (scenario, app,
// service, incident, …). Only the store-backed `runs` subcommands may skip the
// shared engine initialisation; every other "list"/"logs"/"cancel" must NOT,
// or it nil-panics when it reaches for an engine that was never constructed.
func TestSkipSharedInit_LeafNameCollisions(t *testing.T) {
	newTree := func(parent, child string) *cobra.Command {
		p := &cobra.Command{Use: parent}
		c := &cobra.Command{Use: child}
		p.AddCommand(c)
		return c
	}

	cases := []struct {
		name string
		cmd  *cobra.Command
		want bool
	}{
		{"runs list skips", newTree("runs", "list"), true},
		{"runs logs skips", newTree("runs", "logs"), true},
		{"runs cancel skips", newTree("runs", "cancel"), true},
		{"scenario list does NOT skip", newTree("scenario", "list"), false},
		{"app list does NOT skip", newTree("app", "list"), false},
		{"service list does NOT skip", newTree("service", "list"), false},
		{"incident list does NOT skip", newTree("incident", "list"), false},
		{"doctor skips", &cobra.Command{Use: "doctor"}, true},
		{"validate skips", &cobra.Command{Use: "validate"}, true},
		{"scenario up does NOT skip", newTree("scenario", "up"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipSharedInit(tc.cmd); got != tc.want {
				t.Errorf("skipSharedInit(%q) = %v, want %v", tc.cmd.Name(), got, tc.want)
			}
		})
	}
}

// Walk the real, registered command tree: no command that needs the engines may
// be marked skippable. This catches a future command whose leaf name collides
// with the runs subcommands.
func TestSkipSharedInit_RealTree(t *testing.T) {
	// Commands that legitimately skip init, by full "parent leaf" (or bare leaf
	// for top-level commands).
	allowedSkips := map[string]bool{
		"completion": true, "help": true, "doctor": true,
		"runs": true, "validate": true,
		"runs list": true, "runs logs": true, "runs cancel": true,
	}

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			key := sub.Name()
			if p := sub.Parent(); p != nil && p != rootCmd {
				key = p.Name() + " " + sub.Name()
			}
			if skipSharedInit(sub) && !allowedSkips[key] {
				t.Errorf("command %q skips shared init but is not in the allow-list; "+
					"if it needs the engines this is the scenario-list nil-panic bug", key)
			}
			walk(sub)
		}
	}
	walk(rootCmd)
}

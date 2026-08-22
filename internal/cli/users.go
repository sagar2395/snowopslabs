// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/auth"
)

var (
	usersRole     string
	usersPassword string
)

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage API/UI users for team mode (requires LABCTL_AUTH=true to take effect)",
	Long: `Manage the static users file (.labctl/users.yaml) used when authentication
is enabled (LABCTL_AUTH=true). Passwords are stored as PBKDF2-HMAC-SHA256
hashes — never in plain text.

Two roles are supported:
  operator     full control (platform, runtime, lab, apps, services, …)
  participant  run challenges/incidents/learn + read status; cannot mutate
               platform/runtime/lab/apps/services

Auth is OFF by default; these commands only edit the file. Start the server
with LABCTL_AUTH=true to enforce it.`,
}

var usersAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add or update a user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !auth.ValidRole(usersRole) {
			return fmt.Errorf("invalid role %q (want operator or participant)", usersRole)
		}
		pw, err := resolvePassword(cmd)
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		path := auth.DefaultUsersPath(cfg.ProjectRoot)
		uf, err := auth.LoadUsersFile(path)
		if err != nil {
			return err
		}
		existed := uf.Find(name) >= 0
		uf.Upsert(auth.User{Name: name, PasswordHash: hash, Role: usersRole})
		if err := auth.SaveUsersFile(path, uf); err != nil {
			return err
		}
		verb := "added"
		if existed {
			verb = "updated"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "User %q %s (role: %s)\n", name, verb, usersRole)
		return nil
	},
}

var usersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List users and their roles (never prints password hashes)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := auth.DefaultUsersPath(cfg.ProjectRoot)
		uf, err := auth.LoadUsersFile(path)
		if err != nil {
			return err
		}
		if len(uf.Users) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No users defined. Add one with: labctl users add <name> --role operator")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(tw, "NAME\tROLE")
		for _, u := range uf.Users {
			fmt.Fprintf(tw, "%s\t%s\n", u.Name, u.Role)
		}
		return tw.Flush()
	},
}

var usersRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		path := auth.DefaultUsersPath(cfg.ProjectRoot)
		uf, err := auth.LoadUsersFile(path)
		if err != nil {
			return err
		}
		if !uf.Remove(name) {
			return fmt.Errorf("user %q not found", name)
		}
		if err := auth.SaveUsersFile(path, uf); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "User %q removed\n", name)
		return nil
	},
}

// resolvePassword reads the new password from --password, then the
// LABCTL_PASSWORD env var, then a single line on stdin. No-echo terminal input
// is intentionally not used to avoid an external dependency; document that the
// stdin path echoes and is meant for scripted/local lab use.
func resolvePassword(cmd *cobra.Command) (string, error) {
	if usersPassword != "" {
		return usersPassword, nil
	}
	if env := os.Getenv("LABCTL_PASSWORD"); env != "" {
		return env, nil
	}
	fmt.Fprint(cmd.OutOrStdout(), "Password: ")
	sc := bufio.NewScanner(cmd.InOrStdin())
	if !sc.Scan() {
		return "", fmt.Errorf("no password provided (use --password, LABCTL_PASSWORD, or stdin)")
	}
	pw := strings.TrimSpace(sc.Text())
	if pw == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	return pw, nil
}

func init() {
	usersAddCmd.Flags().StringVar(&usersRole, "role", auth.RoleParticipant, "role: operator or participant")
	usersAddCmd.Flags().StringVar(&usersPassword, "password", "", "password (or set LABCTL_PASSWORD, or pipe via stdin)")
	usersCmd.AddCommand(usersAddCmd)
	usersCmd.AddCommand(usersListCmd)
	usersCmd.AddCommand(usersRemoveCmd)
	rootCmd.AddCommand(usersCmd)
}

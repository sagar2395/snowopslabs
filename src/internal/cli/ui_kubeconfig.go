// SPDX-License-Identifier: Apache-2.0
package cli
import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)
func newUIKubeconfigCmd() *cobra.Command {
	var address string; cmd := &cobra.Command{Use: "kubeconfig", Short: "Write a kubeconfig for another machine", RunE: func(cmd *cobra.Command, _ []string) error {
		if address == "" { address, _ = lanAddress("") }
		if address == "" { return fmt.Errorf("no LAN address found; pass --address") }
		if _, ok := lanAddress(address); !ok { return fmt.Errorf("address %s is not one of this machine's", address) }
		cluster := os.Getenv("CLUSTER_NAME"); if cluster == "" { cluster = "snowops" }
		raw, err := exec.Command("k3d", "kubeconfig", "get", cluster).Output()
		if err != nil { return fmt.Errorf("cluster %q is not running: %w", cluster, err) }
		out, host, err := rewriteKubeconfig(raw, address); if err != nil { return err }
		conn, err := net.Dial("tcp", host); if err != nil { return fmt.Errorf("cannot reach the API at %s: %w", host, err) }
		conn.Close()
		_, err = cmd.OutOrStdout().Write(out); return err
	}}
	cmd.Flags().StringVar(&address, "address", "", "LAN address participants use")
	return cmd
}
func init() { if ui, _, err := rootCmd.Find([]string{"ui"}); err == nil { ui.AddCommand(newUIKubeconfigCmd()) } }
func lanAddress(addr string) (string, bool) {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ip, _, _ := net.ParseCIDR(a.String())
		if ip == nil { continue }
		if addr == "" && ip.To4() != nil && !ip.IsLoopback() { return ip.String(), true }
		if ip.String() == addr { return addr, true }
	}
	return "", false
}
func rewriteKubeconfig(in []byte, addr string) ([]byte, string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(in, &doc); err != nil { return nil, "", err }
	clusters, _ := doc["clusters"].([]any)
	var host string
	for _, c := range clusters {
		cm, _ := c.(map[string]any); cl, _ := cm["cluster"].(map[string]any); if cl == nil { continue }
		s, _ := cl["server"].(string); u, err := url.Parse(s); if err != nil { return nil, "", err }
		host = addr; if p := u.Port(); p != "" { host = net.JoinHostPort(addr, p) }
		u.Host = host; cl["server"] = u.String(); cl["tls-server-name"] = "kubernetes"
	}
	out, err := yaml.Marshal(doc); return out, host, err
}

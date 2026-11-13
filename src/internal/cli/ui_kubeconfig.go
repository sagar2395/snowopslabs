// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/snowops-oss/snowopslabs/internal/run"
)

// kubeconfigOptions are the flags of `labctl ui kubeconfig`.
type kubeconfigOptions struct {
	address    string
	serverName string
	out        string
	timeout    time.Duration
}

// uiKubeconfigCmd builds `labctl ui kubeconfig`, which rewrites the k3d
// kubeconfig so another machine on the LAN can reach the API server.
func uiKubeconfigCmd() *cobra.Command {
	var o kubeconfigOptions
	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Write a kubeconfig that reaches the lab's API server over the LAN",
		Long: `Rewrite the k3d kubeconfig so participants can reach the API server
from another machine on the network.

The server address defaults to the first LAN address ` + "`labctl ui`" + ` prints.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addrs, _ := net.InterfaceAddrs()
			return writeKubeconfig(cmd.Context(), cmd.OutOrStdout(), o, addrs)
		},
	}
	cmd.Flags().StringVar(&o.address, "address", "", "LAN address to write into the kubeconfig (default: the first address `labctl ui` prints)")
	cmd.Flags().StringVar(&o.serverName, "tls-server-name", "", "TLS server name to set in the kubeconfig")
	cmd.Flags().StringVar(&o.out, "out", "", "file to write the kubeconfig to (default: ~/.kube/config)")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 10*time.Second, "how long to wait for the API server to answer")
	return cmd
}

// writeKubeconfig fetches the k3d kubeconfig, points it at address, sets the
// TLS server name, writes it and checks the API server answers.
func writeKubeconfig(ctx context.Context, out io.Writer, o kubeconfigOptions, addrs []net.Addr) error {
	address := o.address
	if address == "" {
		lan := lanAddresses(addrs)
		if len(lan) == 0 {
			return errors.New("no LAN address found; pass --address")
		}
		address = lan[0]
	}
	if net.ParseIP(address) == nil {
		return fmt.Errorf("--address %q is not an IP address", address)
	}

	raw, err := run.K3d(ctx, o.timeout, "k3d:"+cfg.ClusterName, "kubeconfig", "get", cfg.ClusterName)
	if err != nil {
		return fmt.Errorf("reading the k3d kubeconfig: %w", err)
	}
	rewritten, port, err := rewriteKubeconfig([]byte(raw), address, o.serverName)
	if err != nil {
		return err
	}

	path := o.out
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding the home directory: %w", err)
		}
		path = filepath.Join(home, ".kube", "config")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(out, "Wrote %s (server https://%s)\n", path, net.JoinHostPort(address, port))

	if err := checkAPI(ctx, address, port, o.serverName, o.timeout); err != nil {
		return err
	}
	fmt.Fprintf(out, "The API server at %s answered.\n", net.JoinHostPort(address, port))
	return nil
}

// rewriteKubeconfig points every cluster in the kubeconfig at address,
// keeping the API server's port, and sets the TLS server name. It returns the
// rewritten YAML and the port it used.
func rewriteKubeconfig(data []byte, address, serverName string) ([]byte, string, error) {
	var kc kubeconfigFile
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return nil, "", fmt.Errorf("parsing the k3d kubeconfig: %w", err)
	}
	if len(kc.Clusters) == 0 {
		return nil, "", errors.New("the k3d kubeconfig has no clusters")
	}
	port := "6443"
	for i := range kc.Clusters {
		u, err := url.Parse(kc.Clusters[i].Cluster.Server)
		if err != nil {
			return nil, "", fmt.Errorf("parsing server URL %q: %w", kc.Clusters[i].Cluster.Server, err)
		}
		if p := u.Port(); p != "" {
			port = p
		}
		u.Host = net.JoinHostPort(address, port)
		kc.Clusters[i].Cluster.Server = u.String()
		if serverName != "" {
			kc.Clusters[i].Cluster.TLSServerName = serverName
		}
	}
	out, err := yaml.Marshal(kc)
	if err != nil {
		return nil, "", fmt.Errorf("encoding the kubeconfig: %w", err)
	}
	return out, port, nil
}

// checkAPI dials the API server with a timeout and completes a TLS handshake,
// so a wrong address or server name fails here instead of in kubectl.
func checkAPI(ctx context.Context, host, port, serverName string, timeout time.Duration) error {
	d := &net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("the API server at %s did not answer: %w", net.JoinHostPort(host, port), err)
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true, // the k3d API server uses a self-signed certificate
	})
	defer conn.Close()
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("the TLS handshake with %s failed: %w", net.JoinHostPort(host, port), err)
	}
	return nil
}

// kubeconfigFile is the subset of a kubeconfig file labctl rewrites.
type kubeconfigFile struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	Clusters       []namedCluster `yaml:"clusters"`
	Contexts       []namedContext `yaml:"contexts"`
	CurrentContext string         `yaml:"current-context"`
	Users          []namedUser    `yaml:"users"`
}

type namedCluster struct {
	Name    string      `yaml:"name"`
	Cluster clusterData `yaml:"cluster"`
}

type clusterData struct {
	Server                   string `yaml:"server"`
	CertificateAuthorityData string `yaml:"certificate-authority-data,omitempty"`
	InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify,omitempty"`
	TLSServerName            string `yaml:"tls-server-name,omitempty"`
}

type namedContext struct {
	Name    string      `yaml:"name"`
	Context contextData `yaml:"context"`
}

type contextData struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace,omitempty"`
}

type namedUser struct {
	Name string   `yaml:"name"`
	User userData `yaml:"user"`
}

type userData struct {
	ClientCertificateData string `yaml:"client-certificate-data,omitempty"`
	ClientKeyData         string `yaml:"client-key-data,omitempty"`
	Token                 string `yaml:"token,omitempty"`
}
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

// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/snowops-oss/snowopslabs/internal/auth"
	"github.com/snowops-oss/snowopslabs/internal/display"
	"github.com/snowops-oss/snowopslabs/internal/httpapi"
	"github.com/snowops-oss/snowopslabs/internal/metrics"
	"github.com/snowops-oss/snowopslabs/internal/tlscert"
	"github.com/snowops-oss/snowopslabs/internal/webui"
)

// uiOptions are the flags of `labctl ui`.
type uiOptions struct {
	port, bind, tlsCert, tlsKey, dir string
	selfSigned                       bool
}

// uiPlan is what `labctl ui` serves and what it prints before it starts.
type uiPlan struct {
	addr              string
	urls              []string // the addresses people open, best first
	certFile, keyFile string
	notes             []string // printed after the URLs: TLS details and warnings
}

func uiCmd() *cobra.Command {
	var o uiOptions
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Launch the web UI dashboard",
		Long: `Serve the web dashboard and the REST API, and open a browser.

For a team server, bind to the network with authentication and HTTPS:
  LABCTL_AUTH=true labctl ui --bind 0.0.0.0 --tls-self-signed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hostname, _ := os.Hostname()
			addrs, _ := net.InterfaceAddrs()
			plan, err := planUI(o, auth.Enabled(), cfg.StateDir, hostname, addrs, time.Now())
			if err != nil {
				return err
			}
			return serveUI(cmd, o, plan)
		},
	}
	cmd.Flags().StringVar(&o.port, "port", "3939", "port to serve the UI on")
	cmd.Flags().StringVar(&o.bind, "bind", "127.0.0.1", "address to bind (use 0.0.0.0 to expose on the network; requires auth)")
	cmd.Flags().StringVar(&o.tlsCert, "tls-cert", "", "path to a TLS certificate (PEM); enables HTTPS when set with --tls-key")
	cmd.Flags().StringVar(&o.tlsKey, "tls-key", "", "path to the TLS private key (PEM)")
	cmd.Flags().BoolVar(&o.selfSigned, "tls-self-signed", false, "serve HTTPS with a self-signed certificate for this machine's names and addresses, made once and kept in the lab state")
	cmd.Flags().StringVar(&o.dir, "ui-dir", os.Getenv("LABCTL_UI_DIR"), "serve the UI live from this directory (e.g. src/ui/dist) instead of the embedded bundle; defaults to $LABCTL_UI_DIR")
	cmd.AddCommand(uiKubeconfigCmd())
	return cmd
}

// planUI checks the flags and works out the address, the URLs to print and
// the certificate to serve, before anything listens. A network bind needs
// auth; with --tls-self-signed the certificate covers every URL printed.
func planUI(o uiOptions, authOn bool, stateDir, hostname string, addrs []net.Addr, now time.Time) (uiPlan, error) {
	// Refuse to expose an unauthenticated control API to the network.
	if err := httpapi.CheckBind(o.bind, authOn); err != nil {
		return uiPlan{}, err
	}
	if o.selfSigned && (o.tlsCert != "" || o.tlsKey != "") {
		return uiPlan{}, errors.New("--tls-self-signed makes its own certificate: drop --tls-cert and --tls-key, or drop --tls-self-signed")
	}
	if (o.tlsCert == "") != (o.tlsKey == "") {
		return uiPlan{}, errors.New("--tls-cert and --tls-key must be provided together")
	}
	plan := uiPlan{addr: net.JoinHostPort(o.bind, o.port), certFile: o.tlsCert, keyFile: o.tlsKey}
	scheme := "http"
	if o.selfSigned || o.tlsCert != "" {
		scheme = "https"
	}
	network := !httpapi.IsLoopbackHost(o.bind)
	hosts := []string{"localhost"}
	switch {
	case !network:
	case isWildcard(o.bind):
		hosts = append(lanAddresses(addrs), "localhost")
	default:
		hosts = []string{o.bind}
	}
	for _, h := range hosts {
		plan.urls = append(plan.urls, scheme+"://"+net.JoinHostPort(h, o.port))
	}

	if o.selfSigned {
		names := append([]string{}, hosts...)
		names = append(names, "127.0.0.1", "::1")
		if hostname != "" {
			names = append(names, hostname)
		}
		pair, err := tlscert.Ensure(filepath.Join(stateDir, "tls"), names, now)
		if err != nil {
			return uiPlan{}, fmt.Errorf("preparing the self-signed certificate: %w", err)
		}
		plan.certFile, plan.keyFile = pair.CertFile, pair.KeyFile
		plan.notes = append(plan.notes,
			fmt.Sprintf("HTTPS uses a self-signed certificate (%s), valid until %s.", display.Path(pair.CertFile, "", ""), pair.NotAfter.Format(time.DateOnly)),
			"Browsers warn the first time; accept only if the certificate's SHA-256 fingerprint is:",
			"  "+pair.Fingerprint)
	}
	if network && scheme == "http" {
		plan.notes = append(plan.notes, "Warning: this serves the network over plain HTTP, so passwords and session cookies cross it unencrypted. Add --tls-self-signed, or --tls-cert and --tls-key.")
	}
	return plan, nil
}

// isWildcard reports whether host binds every interface.
func isWildcard(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

// lanAddresses returns the addresses other machines can reach this one on:
// every global unicast IP, IPv4 first. Loopback and link-local are left out.
func lanAddresses(addrs []net.Addr) []string {
	var v4, v6 []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || !ipn.IP.IsGlobalUnicast() {
			continue
		}
		if ipn.IP.To4() != nil {
			v4 = append(v4, ipn.IP.String())
		} else {
			v6 = append(v6, ipn.IP.String())
		}
	}
	return append(v4, v6...)
}

// uiKubeconfigCmd writes a kubeconfig participants can use to reach this lab
// from another machine.
func uiKubeconfigCmd() *cobra.Command {
	var (
		server  string
		out     string
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Write a kubeconfig that reaches this lab from another machine",
		Long: `Rewrite the k3d kubeconfig so participants can reach the lab's API
server from another machine on the LAN.

The k3d kubeconfig points at a loopback address, which only works on this
machine. This command replaces that address with a LAN address, pins the TLS
server name so the cluster's certificate still validates, and checks the API
server answers before it writes the file.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeParticipantKubeconfig(cmd, server, out, timeout)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "address participants use to reach the API server, host:port (default: the first LAN address)")
	cmd.Flags().StringVar(&out, "out", "participants.kubeconfig", "write the kubeconfig to this file")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "how long to wait for the API server to answer")
	return cmd
}

// writeParticipantKubeconfig rewrites the k3d kubeconfig to a LAN address,
// checks the API server answers, and writes the result to out.
func writeParticipantKubeconfig(cmd *cobra.Command, server, out string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	// The k3d kubeconfig points at a loopback address; participants need a LAN
	// address, the same one `labctl ui` prints.
	if server == "" {
		addrs, _ := net.InterfaceAddrs()
		lans := lanAddresses(addrs)
		if len(lans) == 0 {
			return errors.New("no LAN address found; pass --server with the address participants use")
		}
		server = lans[0]
	}
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		return fmt.Errorf("--server %q must be host:port: %w", server, err)
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("--server %q is not an IP address participants can dial", host)
	}

	raw, err := k3dKubeconfig(ctx, cfg.ClusterName)
	if err != nil {
		return err
	}
	rewritten, tlsName, err := rewriteKubeconfigServer(raw, host, port)
	if err != nil {
		return err
	}
	if err := checkAPIReachable(ctx, net.JoinHostPort(host, port), tlsName); err != nil {
		return err
	}
	if err := os.WriteFile(out, rewritten, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s for participants at %s\n", out, net.JoinHostPort(host, port))
	return nil
}

// k3dKubeconfig returns the kubeconfig k3d wrote for the cluster.
func k3dKubeconfig(ctx context.Context, cluster string) ([]byte, error) {
	c := exec.CommandContext(ctx, "k3d", "kubeconfig", "get", cluster)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("k3d kubeconfig get %s: %w: %s", cluster, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// rewriteKubeconfigServer replaces the API server address in a kubeconfig with
// host:port and pins the TLS server name to the address the cluster's
// certificate was issued for, so the certificate still validates.
func rewriteKubeconfigServer(raw []byte, host, port string) ([]byte, string, error) {
	lines := strings.Split(string(raw), "\n")
	var tlsName string
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "server:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "server:"))
			if u, err := url.Parse(val); err == nil && u.Host != "" {
				origHost := u.Hostname()
				u.Host = net.JoinHostPort(host, port)
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				out = append(out, indent+"server: "+u.String())
				out = append(out, indent+"tls-server-name: "+origHost)
				if tlsName == "" {
					tlsName = origHost
				}
				continue
			}
		}
		out = append(out, line)
	}
	if tlsName == "" {
		return nil, "", errors.New("the kubeconfig has no API server address")
	}
	return []byte(strings.Join(out, "\n")), tlsName, nil
}

// checkAPIReachable dials the API server and completes a TLS handshake with the
// name the cluster's certificate was issued for, so a wrong address or a
// certificate mismatch fails before the kubeconfig is written.
func checkAPIReachable(ctx context.Context, addr, tlsName string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("the API server at %s is not reachable: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: tlsName, MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("the API server at %s did not complete a TLS handshake for %q: %w", addr, tlsName, err)
	}
	return nil
}

// serveUI prints the plan and serves until the command's context ends.
func serveUI(cmd *cobra.Command, o uiOptions, plan uiPlan) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Starting labctl web UI at %s\n", plan.urls[0])
	for _, u := range plan.urls[1:] {
		fmt.Fprintf(out, "  also at %s\n", u)
	}
	for _, n := range plan.notes {
		fmt.Fprintln(out, n)
	}
	fmt.Fprintln(out, "Press Ctrl+C to stop.")

	var lc net.ListenConfig
	ln, err := lc.Listen(cmd.Context(), "tcp", plan.addr)
	if err != nil {
		return fmt.Errorf("cannot bind %s: %w\nAnother `labctl ui` may already be running - stop it first (e.g. pkill -f 'labctl ui'), or choose another --port", plan.addr, err)
	}
	_ = ln.Close()
	go openBrowser(plan.urls[0])

	uiFS, _ := fs.Sub(webui.DistFS, "dist")

	// Optional Prometheus endpoint, off unless LABCTL_METRICS=true.
	var opts []httpapi.ServerOption
	if o.dir != "" {
		opts = append(opts, httpapi.WithUIDir(o.dir))
	}
	if metrics.Enabled() {
		opts = append(opts, httpapi.WithMetrics(metrics.NewApp(rootCmd.Version)))
		fmt.Fprintf(out, "Metrics enabled at %s/metrics\n", plan.urls[0])
	}

	server := httpapi.NewServer(cfg, scriptExec, reg, scenes, incEng, svcReg, rtm, uiFS, opts...)
	// Print which UI build is served, so an old one is easy to spot.
	fmt.Fprintf(out, "Serving %s\n", server.UIInfo())
	if plan.certFile != "" {
		return server.ServeTLS(cmd.Context(), plan.addr, plan.certFile, plan.keyFile)
	}
	return server.Serve(cmd.Context(), plan.addr)
}

func openBrowser(url string) {
	// Start each opener without waiting for it, and stop at the first that
	// launches. There is nothing to cancel, so no context is used.
	candidates := browserCommands(runtime.GOOS, isWSL(), url)
	var lastErr error
	for _, c := range candidates {
		if len(c) == 0 {
			continue
		}
		err := exec.Command(c[0], c[1:]...).Start() //nolint:noctx
		if err == nil {
			return
		}
		lastErr = err
	}
	if lastErr != nil {
		fmt.Printf("Could not open browser (%v). Open %s manually.\n", lastErr, url)
	}
}

func init() {
	rootCmd.AddCommand(uiCmd())
}

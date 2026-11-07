// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/display"
	"github.com/sagar2395/snowopslabs/internal/httpapi"
	"github.com/sagar2395/snowopslabs/internal/metrics"
	"github.com/sagar2395/snowopslabs/internal/tlscert"
	"github.com/sagar2395/snowopslabs/internal/webui"
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

// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	osExec "os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/httpapi"
	"github.com/sagar2395/snowopslabs/internal/metrics"
	"github.com/sagar2395/snowopslabs/internal/webui"
)

var (
	uiPort    string
	uiBind    string
	uiTLSCert string
	uiTLSKey  string
	uiDir     string
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch the web UI dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Refuse to expose an unauthenticated control API to the network.
		if err := httpapi.CheckBind(uiBind, auth.Enabled()); err != nil {
			return err
		}
		if (uiTLSCert == "") != (uiTLSKey == "") {
			return fmt.Errorf("--tls-cert and --tls-key must be provided together")
		}

		addr := net.JoinHostPort(uiBind, uiPort)
		scheme := "http"
		if uiTLSCert != "" {
			scheme = "https"
		}
		// Show localhost for a loopback bind (friendlier link); otherwise the
		// actual bind host so the user knows what is exposed.
		host := uiBind
		if httpapi.IsLoopbackHost(uiBind) {
			host = "localhost"
		}
		url := fmt.Sprintf("%s://%s:%s", scheme, host, uiPort)

		fmt.Printf("Starting labctl web UI at %s\n", url)
		fmt.Println("Press Ctrl+C to stop.")

		// Try to open browser
		go openBrowser(url)

		// Refuse to start on a port another process already holds — otherwise the
		// old server keeps serving its (now stale) bundle and a "restart" looks
		// like it worked while showing old code.
		if ln, err := net.Listen("tcp", addr); err != nil {
			return fmt.Errorf("cannot bind %s: %w\nAnother `labctl ui` may already be running — stop it first (e.g. pkill -f 'labctl ui'), or choose another --port", addr, err)
		} else {
			_ = ln.Close()
		}

		// Use embedded UI assets (sub-directory "dist" within the embed.FS)
		uiFS, _ := fs.Sub(webui.DistFS, "dist")

		// Optional Prometheus endpoint, off unless LABCTL_METRICS=true.
		var opts []httpapi.ServerOption
		if uiDir != "" {
			opts = append(opts, httpapi.WithUIDir(uiDir))
		}
		if metrics.Enabled() {
			opts = append(opts, httpapi.WithMetrics(metrics.NewApp(rootCmd.Version)))
			fmt.Printf("Metrics enabled at %s/metrics\n", url)
		}

		server := httpapi.NewServer(cfg, exec, reg, scenes, incEng, svcReg, rtm, uiFS, opts...)
		// Name the exact bundle being served so a stale process is obvious.
		fmt.Printf("Serving %s\n", server.UIInfo())
		if uiTLSCert != "" {
			return server.StartTLS(addr, uiTLSCert, uiTLSKey)
		}
		return server.Start(addr)
	},
}

func openBrowser(url string) {
	// Opening the browser is fire-and-forget (Start, not Wait): the launched
	// process outlives this call, so a context would have nothing to cancel.
	// We try candidate openers in order and stop at the first that launches —
	// on WSL the Windows openers are tried before the Linux xdg-open fallback.
	candidates := browserCommands(runtime.GOOS, isWSL(), url)
	var lastErr error
	for _, c := range candidates {
		if len(c) == 0 {
			continue
		}
		if err := osExec.Command(c[0], c[1:]...).Start(); err == nil { //nolint:noctx
			return
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		fmt.Printf("Could not open browser (%v). Open %s manually.\n", lastErr, url)
	}
}

func init() {
	uiCmd.Flags().StringVar(&uiPort, "port", "3939", "port to serve the UI on")
	uiCmd.Flags().StringVar(&uiBind, "bind", "127.0.0.1", "address to bind (use 0.0.0.0 to expose on the network; requires auth)")
	uiCmd.Flags().StringVar(&uiTLSCert, "tls-cert", "", "path to a TLS certificate (PEM); enables HTTPS when set with --tls-key")
	uiCmd.Flags().StringVar(&uiTLSKey, "tls-key", "", "path to the TLS private key (PEM)")
	uiCmd.Flags().StringVar(&uiDir, "ui-dir", os.Getenv("LABCTL_UI_DIR"), "serve the UI live from this directory (e.g. src/ui/dist) instead of the embedded bundle; defaults to $LABCTL_UI_DIR")
	rootCmd.AddCommand(uiCmd)
}

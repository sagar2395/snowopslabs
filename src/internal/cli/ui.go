// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"io/fs"
	osExec "os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/httpapi"
	"github.com/sagar2395/snowopslabs/internal/webui"
)

var uiPort string

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch the web UI dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		addr := ":" + uiPort
		url := fmt.Sprintf("http://localhost:%s", uiPort)

		fmt.Printf("Starting labctl web UI at %s\n", url)
		fmt.Println("Press Ctrl+C to stop.")

		// Try to open browser
		go openBrowser(url)

		// Use embedded UI assets (sub-directory "dist" within the embed.FS)
		uiFS, _ := fs.Sub(webui.DistFS, "dist")
		server := httpapi.NewServer(cfg, exec, reg, scenes, incEng, svcReg, rtm, uiFS)
		return server.Start(addr)
	},
}

func openBrowser(url string) {
	var err error
	// Opening the browser is fire-and-forget (Start, not Wait): the launched
	// process outlives this call, so a context would have nothing to cancel.
	switch runtime.GOOS {
	case "linux":
		err = osExec.Command("xdg-open", url).Start() //nolint:noctx
	case "darwin":
		err = osExec.Command("open", url).Start() //nolint:noctx
	case "windows":
		err = osExec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() //nolint:noctx
	}
	if err != nil {
		fmt.Printf("Could not open browser: %v\n", err)
	}
}

func init() {
	uiCmd.Flags().StringVar(&uiPort, "port", "3939", "port to serve the UI on")
	rootCmd.AddCommand(uiCmd)
}

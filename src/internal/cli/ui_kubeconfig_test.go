// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"
)

func TestRewriteKubeconfig(t *testing.T) {
	const in = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://0.0.0.0:6443
  name: k3d-snowops
contexts:
- context:
    cluster: k3d-snowops
    user: admin@k3d-snowops
  name: k3d-snowops
current-context: k3d-snowops
users:
- name: admin@k3d-snowops
  user:
    client-certificate-data: Y2VydA==
    client-key-data: a2V5
`
	tests := []struct {
		name       string
		address    string
		serverName string
		wantServer string
		wantName   string
	}{
		{
			name:       "rewrites the server and keeps the port",
			address:    "192.168.1.10",
			wantServer: "https://192.168.1.10:6443",
		},
		{
			name:       "sets the TLS server name",
			address:    "192.168.1.10",
			serverName: "snowops.lan",
			wantServer: "https://192.168.1.10:6443",
			wantName:   "snowops.lan",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, port, err := rewriteKubeconfig([]byte(in), tt.address, tt.serverName)
			if err != nil {
				t.Fatalf("rewriteKubeconfig() error = %v", err)
			}
			if port != "6443" {
				t.Errorf("port = %q, want 6443", port)
			}
			if !strings.Contains(string(out), tt.wantServer) {
				t.Errorf("output does not contain %q:\n%s", tt.wantServer, out)
			}
			if tt.wantName != "" && !strings.Contains(string(out), "tls-server-name: "+tt.wantName) {
				t.Errorf("output does not contain the TLS server name %q:\n%s", tt.wantName, out)
			}
		})
	}
}

func TestRewriteKubeconfig_Errors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "not YAML", in: "a: [b", wantErr: "parsing"},
		{name: "no clusters", in: "apiVersion: v1\nkind: Config\n", wantErr: "no clusters"},
		{name: "bad server URL", in: "clusters:\n- cluster:\n    server: \"://bad\"\n  name: x\n", wantErr: "server URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := rewriteKubeconfig([]byte(tt.in), "192.168.1.10", "")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("rewriteKubeconfig() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
// SPDX-License-Identifier: Apache-2.0
package cli
import ("strings"; "testing")
func TestRewriteKubeconfig(t *testing.T) {
	in := []byte("clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n")
	out, host, err := rewriteKubeconfig(in, "192.168.1.10")
	if err != nil { t.Fatal(err) }
	if host != "192.168.1.10:6443" || !strings.Contains(string(out), "https://192.168.1.10:6443") || !strings.Contains(string(out), "tls-server-name: kubernetes") { t.Fatalf("bad rewrite: %s", out) }
}

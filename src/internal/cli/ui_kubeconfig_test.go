// SPDX-License-Identifier: Apache-2.0
package cli
import ("strings"; "testing")
func TestRewriteKubeconfig(t *testing.T) {
	in := []byte("clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n")
	out, host, err := rewriteKubeconfig(in, "192.168.1.10")
	if err != nil { t.Fatal(err) }
	if host != "192.168.1.10:6443" || !strings.Contains(string(out), "https://192.168.1.10:6443") || !strings.Contains(string(out), "tls-server-name: kubernetes") { t.Fatalf("bad rewrite: %s", out) }
}

// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"context"

	"github.com/snowops-oss/snowopslabs/internal/k8s"
	catalog "github.com/snowops-oss/snowopslabs/internal/platform"
)

// Installed returns the "category/provider" name of every provider in reg that
// the cluster holds, however it was installed. It reads the cluster once: a
// provider that shares its namespace counts by its deployed Helm release, any
// other by its namespace.
func Installed(ctx context.Context, reg *catalog.Registry) (map[string]bool, error) {
	inv, err := k8s.ReadInventory(ctx)
	if err != nil {
		return nil, err
	}
	installed := map[string]bool{}
	for _, cat := range reg.Categories() {
		for _, p := range reg.GetProviders(cat) {
			found := inv.NamespaceExists(p.Namespace())
			if p.SharesNamespace() {
				found = inv.HelmReleaseDeployed(p.Namespace(), p.Name)
			}
			if found {
				installed[Component(cat, p.Name)] = true
			}
		}
	}
	return installed, nil
}

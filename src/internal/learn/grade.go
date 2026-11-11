// SPDX-License-Identifier: Apache-2.0

package learn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

// Grade runs the check of module idx and, only when it passes, records the
// module complete for user ("" is the OS user). The CLI's `learn next` and
// the API both complete a module through it, so a module is never recorded
// on a client's word. resolve fills the check's templated fields.
func (e *Engine) Grade(ctx context.Context, p *Path, prog *Progress, idx int, runner *checks.Runner, resolve func(string) string, user string) (checks.Result, error) {
	if idx < 0 || idx >= len(p.Modules) {
		return checks.Result{}, fmt.Errorf("module %d is out of range: learning path %q has %d module(s), numbered from 0", idx, p.Name, len(p.Modules))
	}
	res := runner.Run(ctx, p.Modules[idx].Check.RunnerCheck(p.Dir(), resolve))
	if !res.Pass {
		return res, nil
	}
	return res, e.MarkCompleteModule(p, prog, idx, user)
}

// RunnerCheck converts c for the check runner: templated fields are filled by
// resolve, and a relative script is found under the path's directory.
func (c Check) RunnerCheck(pathDir string, resolve func(string) string) checks.Check {
	result := checks.Check{
		Name:           c.Name,
		Type:           c.Type,
		TimeoutSeconds: c.TimeoutSeconds,
	}
	switch c.Type {
	case "http":
		result.URL = resolve(c.URL)
		result.ExpectStatus = c.Status()
	case "promql":
		result.Query = resolve(c.Query)
		result.Operator = c.Operator
		result.Value = c.Value
	case "script":
		script := c.Script
		if script != "" && !filepath.IsAbs(script) {
			script = filepath.Join(pathDir, script)
		}
		result.Script = script
	}
	return result
}

// Resolver returns the function that fills a check's fields: ${VAR} and
// ${VAR:-default} first, with DOMAIN_SUFFIX taken from domainSuffix and the
// rest from the environment, then the template variables expand fills, such
// as {{.IngressURLSuffix}}, which carries the ingress port as well.
func Resolver(domainSuffix string, expand func(string) string) func(string) string {
	return func(s string) string {
		s = ExpandVars(s, domainSuffix)
		if expand != nil {
			s = expand(s)
		}
		return s
	}
}

// ExpandVars expands ${VAR} and ${VAR:-default} in a check field, using
// domainSuffix for DOMAIN_SUFFIX and the process environment for everything
// else. Paths written before templates keep working this way.
func ExpandVars(s, domainSuffix string) string {
	return os.Expand(s, func(name string) string {
		key, def := name, ""
		if before, after, ok := strings.Cut(name, ":-"); ok {
			key, def = before, after
		}
		v := ""
		if key == "DOMAIN_SUFFIX" {
			v = domainSuffix
		} else {
			v = os.Getenv(key)
		}
		if v == "" {
			return def
		}
		return v
	})
}

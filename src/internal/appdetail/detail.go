// SPDX-License-Identifier: Apache-2.0

// Package appdetail builds the UI's app details page: a short overview plus the
// app's Dockerfile and Helm chart, each with its path from the repo root so the
// learner can find and edit it.
package appdetail

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// maxFileBytes caps how much of one file is included in the response.
const maxFileBytes = 24 * 1024

// FileRef is one source file shown on the details page: its path from the
// repo root and its content, possibly truncated.
type FileRef struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// Detail is the per-app payload: what the app is, the stack it uses, and the
// build/deploy artifacts with their in-repo locations.
type Detail struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Tech           []string `json:"tech"`
	BuildStrategy  string   `json:"buildStrategy"`
	DeployStrategy string   `json:"deployStrategy"`
	Namespace      string   `json:"namespace"`
	Dockerfile     *FileRef `json:"dockerfile,omitempty"`
	ChartYAML      *FileRef `json:"chartYaml,omitempty"`
	ValuesFile     *FileRef `json:"valuesFile,omitempty"`
	HelmChartPath  string   `json:"helmChartPath,omitempty"`
	Templates      []string `json:"templates,omitempty"`
}

// Build reads apps/<name> and returns its details. Missing optional files,
// such as a README or Dockerfile, are left out. valuesFile is the Helm values
// file the app deploys with (HELM_VALUES in app.env).
func Build(projectRoot, name, buildStrategy, deployStrategy, namespace, valuesFile string) Detail {
	appDir := filepath.Join(projectRoot, "apps", name)
	if namespace == "" {
		namespace = name
	}
	d := Detail{
		Name:           name,
		BuildStrategy:  buildStrategy,
		DeployStrategy: deployStrategy,
		Namespace:      namespace,
		Description:    description(appDir),
		Tech:           detectTech(appDir),
	}

	if fr := readFile(projectRoot, filepath.Join(appDir, "Dockerfile")); fr != nil {
		d.Dockerfile = fr
	}

	chartDir := filepath.Join(appDir, "deploy", "helm")
	if _, err := os.Stat(chartDir); err == nil {
		d.HelmChartPath = repoRel(projectRoot, chartDir)
		if fr := readFile(projectRoot, filepath.Join(chartDir, "Chart.yaml")); fr != nil {
			d.ChartYAML = fr
		}
		if valuesFile == "" {
			valuesFile = "values.yaml"
		}
		if fr := readFile(projectRoot, filepath.Join(chartDir, valuesFile)); fr != nil {
			d.ValuesFile = fr
		} else if fr := readFile(projectRoot, filepath.Join(chartDir, "values.yaml")); fr != nil {
			d.ValuesFile = fr
		}
		d.Templates = templateList(projectRoot, filepath.Join(chartDir, "templates"))
	}
	return d
}

// description returns a one-paragraph summary: the first real prose line of the
// app's README, else the Helm chart's description, else "".
func description(appDir string) string {
	if p := firstProse(filepath.Join(appDir, "README.md")); p != "" {
		return p
	}
	f, err := os.Open(filepath.Join(appDir, "deploy", "helm", "Chart.yaml"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if after, ok := strings.CutPrefix(line, "description:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// firstProse returns the first non-empty, non-heading, non-badge line of a
// Markdown file — the sentence a reader would take as the summary.
func firstProse(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "![") || strings.HasPrefix(line, "[!") {
			continue
		}
		return line
	}
	return ""
}

// detectTech infers the stack from files present in the app directory, in a
// stable display order.
func detectTech(appDir string) []string {
	var tech []string
	add := func(t string) { tech = append(tech, t) }
	if exists(filepath.Join(appDir, "go.mod")) {
		add("Go")
	}
	if exists(filepath.Join(appDir, "package.json")) {
		add("Node.js")
	}
	if exists(filepath.Join(appDir, "requirements.txt")) || exists(filepath.Join(appDir, "pyproject.toml")) {
		add("Python")
	}
	if exists(filepath.Join(appDir, "Dockerfile")) {
		add("Docker")
	}
	if exists(filepath.Join(appDir, "deploy", "helm")) {
		add("Helm")
		add("Kubernetes")
	}
	return tech
}

// templateList returns the Helm template filenames (repo-relative), so the page
// can point at the manifests without inlining all of them.
func templateList(projectRoot, templatesDir string) []string {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, repoRel(projectRoot, filepath.Join(templatesDir, e.Name())))
	}
	return out
}

func readFile(projectRoot, path string) *FileRef {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	truncated := false
	if len(data) > maxFileBytes {
		data = data[:maxFileBytes]
		truncated = true
	}
	return &FileRef{Path: repoRel(projectRoot, path), Content: string(data), Truncated: truncated}
}

// repoRel returns path relative to the repo root (falling back to the absolute
// path if it somehow escapes the tree), using forward slashes.
func repoRel(projectRoot, path string) string {
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

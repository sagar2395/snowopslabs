// SPDX-License-Identifier: Apache-2.0
package catalog

import "fmt"

// Problem is a single content defect: a decode failure, a schema violation, a
// broken cross-reference or a bad template. It names the file (and line, when
// known) so an author can jump straight to the fault (W2 exit criteria).
type Problem struct {
	Kind    Kind   `json:"kind"`
	Name    string `json:"name"`           // item name, or "" before it is known
	File    string `json:"file"`           // path to the offending file
	Line    int    `json:"line,omitempty"` // 1-based; 0 when the line is unknown
	Message string `json:"message"`
}

// String renders the problem as "file:line: [kind/name] message", omitting the
// line when it is unknown. This is the golden-file-stable form used by
// `labctl validate`.
func (p Problem) String() string {
	loc := p.File
	if p.Line > 0 {
		loc = fmt.Sprintf("%s:%d", p.File, p.Line)
	}
	who := string(p.Kind)
	if p.Name != "" {
		who += "/" + p.Name
	}
	return fmt.Sprintf("%s: [%s] %s", loc, who, p.Message)
}

// less orders problems by file, then line, then message for stable output.
func (p Problem) less(o Problem) bool {
	if p.File != o.File {
		return p.File < o.File
	}
	if p.Line != o.Line {
		return p.Line < o.Line
	}
	return p.Message < o.Message
}

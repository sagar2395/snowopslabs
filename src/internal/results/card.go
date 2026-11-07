// SPDX-License-Identifier: Apache-2.0

package results

import (
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SelfReported is printed on every card. Records are a local file the player
// can edit, so a card never claims the project checked the result.
const SelfReported = "Self-reported: recorded on a personal lab and not verified by the SnowOps Labs project."

// CardKinds are the kinds a card can be made from.
var CardKinds = []string{KindIncident, KindChallenge, KindScenario}

// CardRow is one labelled value on a card.
type CardRow struct {
	Label string
	Value string
}

// Card is a finished run summarised for posting. It holds only what the
// record says about the run: never the user, the app or anything about the
// machine, unless the player adds a name with By.
type Card struct {
	Title string
	Rows  []CardRow
}

// NewCard builds the card for r. by is a name the player chose to show, or
// "" for none.
func NewCard(r Record, by string) (Card, error) {
	name := strings.ReplaceAll(r.Name, "`", "'")
	var c Card
	switch r.Kind {
	case KindIncident:
		c.Title = "Incident: " + name
		c.Rows = []CardRow{
			{"Time to resolve", FormatElapsed(r.Elapsed)},
			{"Hints used", strconv.Itoa(r.HintsUsed)},
		}
		if resolvedBy(r) == ResolvedByEscapeHatch {
			c.Rows = append(c.Rows, CardRow{"Ended", "escape hatch (labctl incident resolve)"})
		} else {
			c.Rows = append(c.Rows, CardRow{"Ended", "fixed by hand, detection check passed"})
		}
		if silent, _ := r.Meta["silent"].(bool); silent {
			c.Rows = append(c.Rows, CardRow{"Mode", "silent (the fault was not named)"})
		}
	case KindChallenge:
		c.Title = "Challenge: " + name
		c.Rows = []CardRow{
			{"Time", FormatElapsed(r.Elapsed)},
			{"Hints used", strconv.Itoa(r.HintsUsed)},
			{"Score", scoreText(r.Score)},
			{"Outcome", r.Outcome},
		}
	case KindScenario:
		c.Title = "Scenario: " + name
		c.Rows = []CardRow{{"Outcome", r.Outcome}}
		passed, okP := r.Meta["checksPassed"].(float64)
		total, okT := r.Meta["checksTotal"].(float64)
		if okP && okT {
			c.Rows = append(c.Rows, CardRow{"Checks passed", fmt.Sprintf("%d of %d", int(passed), int(total))})
		}
	default:
		return Card{}, fmt.Errorf("a card shows an incident, challenge or scenario run, not a %s run", r.Kind)
	}
	if by = strings.TrimSpace(by); by != "" {
		c.Rows = append(c.Rows, CardRow{"By", by})
	}
	c.Rows = append(c.Rows, CardRow{"Date", r.EndedAt.UTC().Format(time.DateOnly)})
	return c, nil
}

func scoreText(score int) string {
	if score < 0 {
		return "not scored"
	}
	return strconv.Itoa(score) + "/100"
}

// FormatElapsed renders seconds as "45s", "12m 05s" or "1h 02m".
func FormatElapsed(seconds int64) string {
	d := time.Duration(max(seconds, 0)) * time.Second
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// Markdown renders the card as a Markdown table under a heading.
func (c Card) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**SnowOps Labs - %s**\n\n| | |\n|---|---|\n", mdEscape(c.Title))
	for _, r := range c.Rows {
		fmt.Fprintf(&b, "| %s | %s |\n", mdEscape(r.Label), mdEscape(r.Value))
	}
	fmt.Fprintf(&b, "\n_%s_\n", SelfReported)
	return b.String()
}

// mdEscape keeps a value from breaking out of its table cell.
func mdEscape(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ", "_", `\_`, "*", `\*`).Replace(s)
}

// SVG renders the card as a self-contained SVG image that needs no fonts or
// files beyond what any browser has.
func (c Card) SVG() string {
	const (
		width   = 720
		rowH    = 30
		top     = 96
		padding = 32
	)
	height := top + len(c.Rows)*rowH + 64
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s">`+"\n",
		width, height, width, height, html.EscapeString("SnowOps Labs - "+c.Title+". "+SelfReported))
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="12" fill="#0f172a"/>`+"\n", width, height)
	b.WriteString(`<g font-family="-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif">` + "\n")
	fmt.Fprintf(&b, `<text x="%d" y="40" font-size="14" fill="#94a3b8">SnowOps Labs</text>`+"\n", padding)
	fmt.Fprintf(&b, `<text x="%d" y="68" font-size="22" font-weight="600" fill="#f8fafc">%s</text>`+"\n", padding, html.EscapeString(c.Title))
	for i, r := range c.Rows {
		y := top + i*rowH + 10
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="15" fill="#94a3b8">%s</text>`+"\n", padding, y, html.EscapeString(r.Label))
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="15" fill="#e2e8f0">%s</text>`+"\n", padding+170, y, html.EscapeString(r.Value))
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" fill="#fbbf24">%s</text>`+"\n", padding, height-24, html.EscapeString(SelfReported))
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

// Latest returns the most recently ended record a card can be made from,
// optionally limited to one kind and one name.
func Latest(recs []Record, kind, name string) (Record, bool) {
	var best Record
	found := false
	for _, r := range recs {
		if !slices.Contains(CardKinds, r.Kind) || (kind != "" && r.Kind != kind) || (name != "" && r.Name != name) {
			continue
		}
		if !found || !r.EndedAt.Before(best.EndedAt) {
			best, found = r, true
		}
	}
	return best, found
}

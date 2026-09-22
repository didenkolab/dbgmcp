package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// renderMarkdown writes the report a reviewer opens.
//
// The findings come first and the raw transcript last. A reviewer reading a
// pipeline artifact has seconds, not minutes: what was noticed has to be at the
// top, and the values it rests on have to be reachable from there so the claim
// can be checked rather than believed.
func renderMarkdown(req model.LaunchRequest, probes []model.Probe, t model.Transcript) string {
	var b strings.Builder

	b.WriteString("# Runtime trace\n\n")
	fmt.Fprintf(&b, "`%s` in `%s`", req.Mode, req.Target)
	if req.TestRun != "" {
		fmt.Fprintf(&b, ", test `%s`", req.TestRun)
	}
	b.WriteString("\n\n")

	if len(t.Findings) == 0 {
		b.WriteString("Nothing in this transcript stood out. That is not a clean bill of health: these\n")
		b.WriteString("rules notice an anomalous shape, not a wrong value.\n\n")
	} else {
		fmt.Fprintf(&b, "## Noticed (%d)\n\n", len(t.Findings))
		b.WriteString("Observations, not conclusions. Each carries the values it rests on.\n\n")
		for _, f := range t.Findings {
			fmt.Fprintf(&b, "**%s** — %s\n", f.Kind, f.Detail)
			if f.File != "" {
				fmt.Fprintf(&b, "\n`%s:%d`", f.File, f.Line)
				if f.Hit > 0 {
					fmt.Fprintf(&b, ", hit %d", f.Hit)
				}
				b.WriteString("\n")
			}
			if len(f.Evidence) > 0 {
				b.WriteString("\n```\n")
				for _, line := range f.Evidence {
					b.WriteString(line + "\n")
				}
				b.WriteString("```\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## How it was recorded\n\n")
	fmt.Fprintf(&b, "- status: `%s`\n", t.Status)
	fmt.Fprintf(&b, "- mode: `%s`", t.Mode)
	if t.PerturbsTiming {
		b.WriteString(" — the debuggee stopped at every hit, so timing is not what it would be in production\n")
	} else {
		b.WriteString(" — the debuggee ran without stopping\n")
	}
	fmt.Fprintf(&b, "- hits: %d across %d probe(s)\n", len(t.Hits), len(probes))
	if len(t.ProbesNeverHit) > 0 {
		fmt.Fprintf(&b, "- **never fired**: %s — an empty transcript there means a misplaced probe, not absent data\n",
			strings.Join(t.ProbesNeverHit, ", "))
	}
	b.WriteString("\n")

	if len(t.Hits) > 0 {
		b.WriteString("## Transcript\n\n")
		b.WriteString(renderTable(t))
	}
	return b.String()
}

// renderTable lays the transcript out as one row per hit, because a reader
// comparing values across iterations is reading down a column.
func renderTable(t model.Transcript) string {
	columns := map[string]bool{}
	for _, hit := range t.Hits {
		for expr := range hit.Values {
			columns[expr] = true
		}
		for expr := range hit.Absent {
			columns[expr] = true
		}
	}
	names := make([]string, 0, len(columns))
	for expr := range columns {
		names = append(names, expr)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("| hit | where |")
	for _, n := range names {
		fmt.Fprintf(&b, " `%s` |", n)
	}
	b.WriteString("\n|---:|---|")
	for range names {
		b.WriteString("---|")
	}
	b.WriteString("\n")

	for _, hit := range t.Hits {
		where := fmt.Sprintf("%s:%d", shortPath(hit.File), hit.Line)
		if hit.Function != "" {
			where = hit.Function
		}
		fmt.Fprintf(&b, "| %d | %s |", hit.Hit, where)
		for _, n := range names {
			switch {
			case hit.Values[n] != "":
				fmt.Fprintf(&b, " %s |", escapeCell(hit.Values[n]))
			case hit.Absent[n] != "":
				// Spelled out rather than left blank: "nil" and "could not read"
				// are different facts, and an empty cell states neither.
				fmt.Fprintf(&b, " _%s_ |", hit.Absent[n])
			default:
				b.WriteString("  |")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func shortPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

func escapeCell(v string) string {
	v = strings.ReplaceAll(v, "|", "\\|")
	v = strings.ReplaceAll(v, "\n", " ")
	if len(v) > 60 {
		v = v[:57] + "..."
	}
	return v
}

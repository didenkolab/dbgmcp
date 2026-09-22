package model

import "strings"

// PathMapping translates between the paths an agent can read and the paths the
// runtime actually loaded.
//
// This is neutral on purpose. Delve calls it substitute-path rules; a browser
// calls it source maps; a container calls it a bind mount. They are one idea --
// "the source I can see is not the source it runs" -- and modelling it as a
// Delve field would force a retrofit the moment a backend with source maps
// appears. Rules apply in order; the first prefix match wins.
type PathMapping struct {
	Rules []PathRule `json:"rules,omitempty"`
}

type PathRule struct {
	// From is the prefix as the agent sees it, To as the runtime sees it.
	From string `json:"from"`
	To   string `json:"to"`
}

// ToRuntime rewrites an agent-visible path for the debugger.
func (m PathMapping) ToRuntime(p string) string { return apply(m.Rules, p, false) }

// ToAgent rewrites a debugger-reported path back for the agent. It is the exact
// inverse, so a location survives a round trip through the backend unchanged --
// the property that makes "set a breakpoint, then recognise where we stopped"
// work at all.
func (m PathMapping) ToAgent(p string) string { return apply(m.Rules, p, true) }

func apply(rules []PathRule, p string, reverse bool) string {
	for _, r := range rules {
		from, to := r.From, r.To
		if reverse {
			from, to = to, from
		}
		if from != "" && strings.HasPrefix(p, from) {
			return to + strings.TrimPrefix(p, from)
		}
	}
	return p
}

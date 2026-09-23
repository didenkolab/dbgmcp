package delve

import (
	"errors"
	"strings"
	"testing"
)

// Delve's refusals are terse and several of them read like something they are
// not. The advice attached to each is the only thing that tells a reader what to
// do next, so pointing at the wrong constraint is worse than saying nothing: it
// sends them to count watchpoints when the answer is the size of a type.
func TestWatchpointAdviceNamesTheReasonThatApplies(t *testing.T) {
	cases := []struct {
		name        string
		delveSays   string
		mustMention string
		mustNotSay  string
	}{
		{
			name:        "a value wider than a machine word",
			delveSays:   `can not watch variable of type string`,
			mustMention: "machine word",
			mustNotSay:  "at most four",
		},
		{
			name:        "an expression with no address",
			delveSays:   `can not watch "t"`,
			mustMention: "no address of its own",
			mustNotSay:  "at most four",
		},
		{
			name:        "the variable does not exist yet",
			delveSays:   `could not find symbol value for total`,
			mustMention: "before its locals are declared",
			mustNotSay:  "machine word",
		},
		{
			name:        "reads of a stack variable",
			delveSays:   `can not watch stack allocated variable for reads`,
			mustMention: "watch writes instead",
			mustNotSay:  "machine word",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := watchpointAdvice(errors.New(c.delveSays))
			if !strings.Contains(got, c.mustMention) {
				t.Errorf("advice does not mention %q:\n  %s", c.mustMention, got)
			}
			if strings.Contains(got, c.mustNotSay) {
				t.Errorf("advice wrongly brings up %q:\n  %s", c.mustNotSay, got)
			}
		})
	}
}

func TestWatchpointAdviceStillHasSomethingToSayForAnythingElse(t *testing.T) {
	// An unrecognised refusal must not come back bare: the hardware limits are
	// the useful default, and silence would leave a reader with Delve's wording
	// alone.
	got := watchpointAdvice(errors.New("something new in a later Delve"))
	if !strings.Contains(got, "hardware feature") {
		t.Errorf("no fallback advice: %q", got)
	}
}

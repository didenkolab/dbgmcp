package model

import "testing"

func TestWithDefaultsFillsOnlyUnsetFields(t *testing.T) {
	// An agent raising one limit must not silently lose the others.
	b := ValueBudget{MaxDepth: 9}.WithDefaults()

	if b.MaxDepth != 9 {
		t.Fatalf("overwrote a caller-supplied limit: MaxDepth = %d", b.MaxDepth)
	}
	d := DefaultValueBudget()
	if b.MaxStringLen != d.MaxStringLen || b.MaxArrayValues != d.MaxArrayValues || b.MaxStructAttrs != d.MaxStructAttrs {
		t.Fatalf("did not fill the unset limits: %+v", b)
	}
}

func TestZeroBudgetBecomesTheDefault(t *testing.T) {
	if (ValueBudget{}).WithDefaults() != DefaultValueBudget() {
		t.Fatal("a zero budget must mean the default, not no limits")
	}
}

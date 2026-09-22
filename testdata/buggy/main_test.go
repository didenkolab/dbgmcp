package main

import "testing"

func TestSubtotal(t *testing.T) {
	cart := []Item{
		{Name: "mug", Price: 10, Qty: 3},
		{Name: "shirt", Price: 40, Qty: 2},
		{Name: "chair", Price: 150, Qty: 4},
	}
	// 30 + 80 + 600
	if got := Subtotal(cart); got != 710 {
		t.Fatalf("Subtotal = %d, want 710", got)
	}
}

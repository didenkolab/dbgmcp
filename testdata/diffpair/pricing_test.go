package main

import "testing"

func TestOrdinaryCustomerGetsTheCoupon(t *testing.T) {
	if got := FinalPrice(Order{Subtotal: 1000, Coupon: 20}); got != 800 {
		t.Fatalf("FinalPrice = %d, want 800", got)
	}
}

// TestLoyalCustomerAlsoGetsTheCoupon fails, and is meant to. It is the failing
// half of the pair diff_runs is asked to explain: same code, same coupon, one
// flag different, and the coupon quietly disappears.
func TestLoyalCustomerAlsoGetsTheCoupon(t *testing.T) {
	// 1000, 20% off, then 10% loyalty.
	if got := FinalPrice(Order{Subtotal: 1000, Coupon: 20, Loyal: true}); got != 720 {
		t.Fatalf("FinalPrice = %d, want 720", got)
	}
}

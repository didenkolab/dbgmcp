// Package diffpair is the fixture for diff_runs: one input that is priced
// correctly and one that is not, differing only in a flag.
//
// It is deliberately separate from testdata/buggy, which six other suites
// depend on and which must keep passing as a whole. This package contains a
// test that fails on purpose, and that failure is the point.
package main

type Order struct {
	Subtotal int
	Coupon   int // percent off
	Loyal    bool
}

// applyCoupon is correct. The diff test probes it as well as the defective line,
// so the comparison has somewhere that agrees: a tool that only ever reports
// divergences cannot be distinguished from one that reports everything.
func applyCoupon(price, percent int) int {
	if percent <= 0 {
		return price
	}
	return price - price*percent/100 // COUPON
}

// FinalPrice has the bug this fixture exists for: for a loyal customer the
// loyalty price is computed from the original subtotal, silently discarding the
// coupon that was already applied. An ordinary customer is charged correctly, so
// nothing about the one run looks wrong -- the defect only appears when the two
// are compared.
func FinalPrice(o Order) int {
	price := applyCoupon(o.Subtotal, o.Coupon)
	if o.Loyal {
		price = o.Subtotal * 90 / 100
	}
	return price // CHARGE
}

func main() {}

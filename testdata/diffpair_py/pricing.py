"""A Python twin of testdata/diffpair, carrying the same bug.

Two runtimes with the same defect is what lets diff_runs be tested rather than
assumed to be language-neutral: the comparison is computed from transcripts, so
if it works for Delve and not for a DAP adapter, the neutrality was a claim
rather than a fact.

The two cases differ by a command-line flag, which also exercises the path a Go
test filter does not.
"""
import sys


def apply_coupon(price, percent):
    """Correct. Probed alongside the defective line so the comparison has
    somewhere that agrees."""
    if percent <= 0:
        return price
    return price - price * percent // 100  # COUPON


def final_price(subtotal, coupon, loyal):
    """The bug: for a loyal customer the loyalty price is computed from the
    original subtotal, silently discarding the coupon already applied."""
    price = apply_coupon(subtotal, coupon)
    if loyal:
        price = subtotal * 90 // 100
    return price  # CHARGE


def main():
    loyal = "--loyal" in sys.argv
    print("final:", final_price(1000, 20, loyal))


if __name__ == "__main__":
    main()

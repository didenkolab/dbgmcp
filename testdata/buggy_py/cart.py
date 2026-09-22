"""A Python twin of the Go fixture, carrying the same bug.

Having the same defect in two runtimes is what lets the conformance suite ask
the same questions of both backends and compare the answers.
"""
import threading


class Item:
    def __init__(self, name, price, qty):
        self.name = name
        self.price = price
        self.qty = qty


def line_total(it):
    if it.qty < 0:
        return 0  # NEVER-REACHED: no cart here has a negative quantity
    if it.price > 100:
        return it.price  # the bug: quantity is dropped for expensive items
    return it.price * it.qty


def subtotal(items):
    total = 0
    for it in items:
        total += line_total(it)
    return total


def worker(out, items):
    out.append(subtotal(items))


CART = [Item("mug", 10, 3), Item("shirt", 40, 2), Item("chair", 150, 4)]


def main():
    print("subtotal:", subtotal(CART))
    out = []
    t = threading.Thread(target=worker, args=(out, CART))
    t.start()
    t.join()
    print("from worker:", out[0])


if __name__ == "__main__":
    main()

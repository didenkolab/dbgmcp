from cart import CART, subtotal


def test_subtotal():
    # 30 + 80 + 600
    assert subtotal(CART) == 710

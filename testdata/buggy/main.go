package main

import "fmt"

type Item struct {
	Name  string
	Price int
	Qty   int
}

// lineTotal has the bug this fixture exists for: the quantity is dropped for
// expensive items, so a cart is only wrong when it contains one.
func lineTotal(it Item) int {
	if it.Price > 100 {
		return it.Price
	}
	return it.Price * it.Qty
}

func Subtotal(items []Item) int {
	total := 0
	for i := range items {
		total += lineTotal(items[i])
	}
	return total
}

func main() {
	cart := []Item{
		{Name: "mug", Price: 10, Qty: 3},
		{Name: "shirt", Price: 40, Qty: 2},
		{Name: "chair", Price: 150, Qty: 4},
	}
	fmt.Println("subtotal:", Subtotal(cart))
}

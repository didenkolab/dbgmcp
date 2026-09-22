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
	if it.Qty < 0 {
		return 0 // NEVER-REACHED: no cart in this fixture has a negative quantity
	}
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

// worker exists so the conformance suite has a goroutine whose ancestry can be
// asked for: it is created by main, so its chain must lead back there.
func worker(out chan<- int, items []Item) {
	out <- Subtotal(items)
}

func main() {
	cart := []Item{
		{Name: "mug", Price: 10, Qty: 3},
		{Name: "shirt", Price: 40, Qty: 2},
		{Name: "chair", Price: 150, Qty: 4},
	}
	fmt.Println("subtotal:", Subtotal(cart))

	out := make(chan int, 1)
	go worker(out, cart)
	fmt.Println("from worker:", <-out)
}

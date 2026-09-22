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

// refreshToken mimics a credential that silently stops being renewed: it
// advances for a while and then quietly stops. Nothing fails, nothing logs, and
// the value is never obviously wrong -- which is the shape the findings rules
// exist to notice, as distinct from a value that is simply incorrect.
func refreshToken(n int) string {
	if n < 3 {
		return fmt.Sprintf("tok-%d", n)
	}
	return "tok-2" // the bug: stops advancing
}

func Rotate(times int) string {
	token := ""
	for i := 0; i < times; i++ {
		token = refreshToken(i)
	}
	return token
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
	fmt.Println("token:", Rotate(8))
}

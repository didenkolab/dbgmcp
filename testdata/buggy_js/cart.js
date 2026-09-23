// A JavaScript twin of the Go and Python fixtures, carrying the same bug.
//
// Having one defect in three runtimes is what lets the conformance suite ask the
// same questions of every backend and compare the answers.

class Item {
  constructor(name, price, qty) {
    this.name = name;
    this.price = price;
    this.qty = qty;
  }
}

function lineTotal(it) {
  if (it.qty < 0) {
    return 0; // NEVER-REACHED: no cart here has a negative quantity
  }
  if (it.price > 100) {
    return it.price; // the bug: quantity is dropped for expensive items
  }
  return it.price * it.qty;
}

function subtotal(items) {
  let total = 0;
  for (const it of items) {
    total += lineTotal(it);
  }
  return total;
}

// refreshToken advances for a while and then quietly stops, which is the shape
// the findings rules exist to notice, as distinct from a value that is wrong.
function refreshToken(n) {
  if (n < 3) {
    return `tok-${n}`;
  }
  return 'tok-2';
}

function rotate(times) {
  let token = '';
  for (let i = 0; i < times; i++) {
    token = refreshToken(i);
  }
  return token;
}

const CART = [new Item('mug', 10, 3), new Item('shirt', 40, 2), new Item('chair', 150, 4)];

function main() {
  console.log('subtotal:', subtotal(CART));
  console.log('token:', rotate(8));
}

main();
module.exports = { Item, lineTotal, subtotal, rotate, CART };

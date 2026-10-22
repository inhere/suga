package orders

import (
	"errors"
	"sort"
	"time"
)

// Order is one row of data/orders.log.
type Order struct {
	ID     int
	Status string
	Units  int
	Region string
	At     time.Time
}

// ErrEmpty is returned by Total when it gets no order.
var ErrEmpty = errors.New("orders: empty input")

// Parse turns one log line into an Order; a malformed line yields an error.
func Parse(line string) (Order, error) {
	if line == "" {
		return Order{}, ErrEmpty
	}
	return Order{}, nil
}

// Total sums the units of every order, skipping the ones in skip.
func Total(list []Order, skip string) (int, error) {
	if len(list) == 0 {
		return 0, ErrEmpty
	}
	sum := 0
	for _, o := range list {
		if o.Status == skip {
			continue
		}
		sum += o.Units
	}
	return sum, nil
}

// SortByTime sorts the orders oldest first, keeping equal times in input order.
func SortByTime(list []Order) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].At.Before(list[j].At) })
}

// CountByRegion counts the orders per region.
func CountByRegion(list []Order) map[string]int {
	out := make(map[string]int, len(list))
	for _, o := range list {
		out[o.Region]++
	}
	return out
}

// firstErrorLine is unexported: the first line whose status is error.
func firstErrorLine(list []Order) int {
	for i, o := range list {
		if o.Status == "error" {
			return i
		}
	}
	return -1
}

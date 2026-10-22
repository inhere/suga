package orders

import (
	"sync"
)

// Store keeps the orders of one region in memory.
type Store struct {
	mu     sync.RWMutex
	byKey  map[int]Order
	region string
}

// NewStore returns an empty store for region.
func NewStore(region string) *Store {
	return &Store{byKey: map[int]Order{}, region: region}
}

// Put stores o, replacing an order with the same id.
func (s *Store) Put(o Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[o.ID] = o
}

// Get returns the order with id.
func (s *Store) Get(id int) (Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.byKey[id]
	return o, ok
}

// Len is the number of stored orders.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byKey)
}

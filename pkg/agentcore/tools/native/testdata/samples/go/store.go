package store

import "errors"

// Store keeps the items. A reload of the store calls Reload.
type Store struct {
	Items []Item
}

// Item is one stored entry.
type Item struct {
	Name string
}

const (
	// maxItems caps one store.
	maxItems = 10
	version  = "v1"
)

// ErrEmpty is returned by a store without items.
var ErrEmpty = errors.New("store is empty")

// Finder finds items; a reload does not change it.
type Finder interface {
	Find(name string) (Item, bool)
}

// New builds an empty store.
func New() *Store {
	return &Store{}
}

// Reload fills the store again after a reload request.
func (s *Store) Reload() error {
	msg := "reload failed"
	if len(s.Items) > maxItems {
		return errors.New(msg)
	}
	// A local variable is no declaration.
	count := len(s.Items)
	_ = count
	return nil
}

// Load reloads the store through Reload.
func (s *Store) Load() error {
	return s.Reload()
}

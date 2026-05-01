package doctype

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the registered document types. It's process-global because
// types are static — declared in their respective Go files via init() →
// MustRegister.
var (
	mu    sync.RWMutex
	types = make(map[string]DocType)
	order []string // insertion order, used by All() for stable UI listing
)

// MustRegister adds a DocType to the registry. Called from each type's init().
// Panics on duplicate ID — types are declared at compile time, duplicates are
// programmer errors, not runtime conditions.
func MustRegister(t DocType) {
	mu.Lock()
	defer mu.Unlock()
	if t.ID == "" {
		panic("doctype: empty ID")
	}
	if t.Prompt == nil {
		panic(fmt.Sprintf("doctype: %q has nil Prompt", t.ID))
	}
	if _, dup := types[t.ID]; dup {
		panic(fmt.Sprintf("doctype: duplicate registration %q", t.ID))
	}
	types[t.ID] = t
	order = append(order, t.ID)
}

// Get returns the DocType for an ID, or false if unknown. Use this in
// handlers to validate user input before generation.
func Get(id string) (DocType, bool) {
	mu.RLock()
	defer mu.RUnlock()
	t, ok := types[id]
	return t, ok
}

// All returns every registered DocType in registration order. Used by the
// UI's type-catalogue card list and by the chatagent's `generate_document`
// tool (which exposes the IDs to the LLM as an enum).
func All() []DocType {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]DocType, 0, len(order))
	for _, id := range order {
		out = append(out, types[id])
	}
	return out
}

// IDs returns just the registered IDs, sorted lexicographically. Convenient
// for embedding as a JSON-Schema enum on the agent tool definition.
func IDs() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(types))
	for id := range types {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

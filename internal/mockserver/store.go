package mockserver

import (
	"errors"
	"slices"
	"strings"
	"sync"
)

// asciiWhitespace is trimmed from both ends of a requested address.
const asciiWhitespace = " \t\n\v\f\r"

var (
	// ErrInvalidEmail reports an address without exactly one "@" and a dotted
	// domain.
	ErrInvalidEmail = errors.New("email address is not a single address with a dotted domain")
	// ErrInjectedFailure reports a failure requested through fail-next.
	ErrInjectedFailure = errors.New("mock operation failure")
	// ErrListFull reports a full list requested through full-next.
	ErrListFull = errors.New("mock subscriber list is full")
)

// Store is the in-memory newsletter subscriber list behind the mock API.
type Store struct {
	mu          sync.Mutex
	subscribers map[string]struct{}
	failNext    bool
	fullNext    bool
}

// ControlView is the mock-only state returned by /__mock__/control.
type ControlView struct {
	Mode        string   `json:"mode"`
	Subscribers []string `json:"subscribers"`
	FailNext    bool     `json:"failNext"`
	FullNext    bool     `json:"fullNext"`
}

// NewStore returns an empty subscriber list.
func NewStore() *Store {
	return &Store{subscribers: make(map[string]struct{})}
}

// Subscribe adds the address and returns its canonical form. Adding an address
// that is already subscribed succeeds. A pending fail-next is consumed first;
// a pending full-next answers the next valid address as a full list does.
func (store *Store) Subscribe(email string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failNext {
		store.failNext = false
		return "", ErrInjectedFailure
	}
	canonical, ok := canonicalEmail(email)
	if !ok {
		return "", ErrInvalidEmail
	}
	if store.fullNext {
		store.fullNext = false
		return "", ErrListFull
	}
	store.subscribers[canonical] = struct{}{}
	return canonical, nil
}

// Reset clears the subscriber list and any pending failure.
func (store *Store) Reset() ControlView {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.subscribers = make(map[string]struct{})
	store.failNext = false
	store.fullNext = false
	return store.controlView()
}

// FailNext makes the next Subscribe return ErrInjectedFailure.
func (store *Store) FailNext() ControlView {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.failNext = true
	return store.controlView()
}

// FullNext makes the next valid Subscribe return ErrListFull.
func (store *Store) FullNext() ControlView {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.fullNext = true
	return store.controlView()
}

// Control returns the current mock state.
func (store *Store) Control() ControlView {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.controlView()
}

func (store *Store) controlView() ControlView {
	subscribers := make([]string, 0, len(store.subscribers))
	for email := range store.subscribers {
		subscribers = append(subscribers, email)
	}
	slices.Sort(subscribers)
	return ControlView{Mode: "mock", Subscribers: subscribers, FailNext: store.failNext, FullNext: store.fullNext}
}

// canonicalEmail trims ASCII whitespace and lowercases the address. It accepts
// exactly one "@" with a non-empty local part and a domain of at least two
// non-empty dot-separated labels. This is deliberately looser than the real
// subscriber list; only the real stack proves the production address policy.
func canonicalEmail(email string) (string, bool) {
	trimmed := strings.Trim(email, asciiWhitespace)
	if strings.Count(trimmed, "@") != 1 {
		return "", false
	}
	localPart, domain, _ := strings.Cut(trimmed, "@")
	if localPart == "" || !strings.Contains(domain, ".") {
		return "", false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" {
			return "", false
		}
	}
	return strings.ToLower(trimmed), true
}

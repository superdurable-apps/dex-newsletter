package mockserver

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestStoreCanonicalizesAndValidatesAddresses(t *testing.T) {
	cases := []struct {
		email, want string
		valid       bool
	}{
		{"reader@example.com", "reader@example.com", true},
		{"Reader@Example.COM", "reader@example.com", true},
		{" \t\r\n Reader@Example.com \v\f", "reader@example.com", true},
		{"first.last+news@mail.example.co.uk", "first.last+news@mail.example.co.uk", true},
		{"reader@example", "", false},
		{"reader.example.com", "", false},
		{"reader@@example.com", "", false},
		{"reader@one@example.com", "", false},
		{"@example.com", "", false},
		{"reader@", "", false},
		{"reader@.example.com", "", false},
		{"reader@example.com.", "", false},
		{"reader@example..com", "", false},
		{"   ", "", false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.email), func(t *testing.T) {
			got, err := NewStore().Subscribe(tc.email)
			if !tc.valid {
				if !errors.Is(err, ErrInvalidEmail) {
					t.Fatalf("Subscribe(%q) = %q, %v; want ErrInvalidEmail", tc.email, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Subscribe(%q) = %q, %v; want %q", tc.email, got, err, tc.want)
			}
		})
	}
}

func TestStoreSubscribeIsIdempotent(t *testing.T) {
	store := NewStore()
	for _, email := range []string{"reader@example.com", " READER@example.com", "other@example.org"} {
		if _, err := store.Subscribe(email); err != nil {
			t.Fatalf("Subscribe(%q): %v", email, err)
		}
	}
	if _, err := store.Subscribe("reader@example"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("invalid subscribe error = %v", err)
	}
	want := ControlView{Mode: "mock", Subscribers: []string{"other@example.org", "reader@example.com"}}
	if got := store.Control(); !reflect.DeepEqual(got, want) {
		t.Fatalf("control = %+v, want %+v", got, want)
	}
}

func TestStoreFailNextIsConsumedOnce(t *testing.T) {
	store := NewStore()
	if view := store.FailNext(); !view.FailNext {
		t.Fatalf("fail-next view = %+v", view)
	}
	if _, err := store.Subscribe("reader@example.com"); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("first subscribe error = %v", err)
	}
	if got := store.Control(); got.FailNext || len(got.Subscribers) != 0 {
		t.Fatalf("control after injected failure = %+v", got)
	}
	if got, err := store.Subscribe("reader@example.com"); err != nil || got != "reader@example.com" {
		t.Fatalf("second subscribe = %q, %v", got, err)
	}
	// An injected failure applies to the next request whatever its address.
	store.FailNext()
	if _, err := store.Subscribe("reader@example"); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("subscribe after fail-next with an invalid address = %v", err)
	}
}

func TestStoreFullNextAnswersTheNextValidAddressOnce(t *testing.T) {
	store := NewStore()
	if view := store.FullNext(); !view.FullNext {
		t.Fatalf("full-next view = %+v", view)
	}
	if _, err := store.Subscribe("reader@example"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("an invalid address with full-next pending = %v, want ErrInvalidEmail", err)
	}
	if _, err := store.Subscribe("reader@example.com"); !errors.Is(err, ErrListFull) {
		t.Fatalf("subscribe with full-next pending = %v, want ErrListFull", err)
	}
	if got := store.Control(); got.FullNext || len(got.Subscribers) != 0 {
		t.Fatalf("control after the full answer = %+v", got)
	}
	store.FullNext()
	if got := store.Reset(); got.FullNext {
		t.Fatalf("reset kept full-next: %+v", got)
	}
}

func TestStoreResetClearsSubscribersAndPendingFailure(t *testing.T) {
	store := NewStore()
	if _, err := store.Subscribe("reader@example.com"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	store.FailNext()
	want := ControlView{Mode: "mock", Subscribers: []string{}}
	if got := store.Reset(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reset = %+v, want %+v", got, want)
	}
	if _, err := store.Subscribe("reader@example.com"); err != nil {
		t.Fatalf("subscribe after reset: %v", err)
	}
}

func TestStoreAllowsConcurrentSubscriptions(t *testing.T) {
	store := NewStore()
	var wait sync.WaitGroup
	for index := range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := store.Subscribe(fmt.Sprintf("reader-%d@example.com", index%8)); err != nil {
				t.Errorf("subscribe: %v", err)
			}
		}()
	}
	wait.Wait()
	if got := len(store.Control().Subscribers); got != 8 {
		t.Fatalf("subscribers = %d, want 8", got)
	}
}

package subscribers

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalAddress(t *testing.T) {
	for raw, want := range map[string]string{" Ada@Example.COM ": "ada@example.com", "a.b+tag@sub.example.org": "a.b+tag@sub.example.org"} {
		if got, err := CanonicalAddress(raw); err != nil || got != want {
			t.Errorf("CanonicalAddress(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "ada", "ada@localhost", "Ada <ada@example.com>", "a@example.com, b@example.com", "a@example.com\r\nBcc: x@y.z", "a@example."} {
		if _, err := CanonicalAddress(raw); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("CanonicalAddress(%q) error = %v", raw, err)
		}
	}
}

func TestUnsubscribeLinks(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys", "unsubscribe.key")
	links, err := LoadUnsubscribeLinks("https://news.example/", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(keyFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file = %v, %v", info, err)
	}
	reloaded, err := LoadUnsubscribeLinks("https://news.example", keyFile)
	if err != nil || reloaded.Token("a@example.com") != links.Token("a@example.com") {
		t.Fatal("a reloaded key signs differently")
	}
	parsed, err := url.Parse(links.URL("a@example.com"))
	if err != nil || parsed.Host != "news.example" || parsed.Path != "/unsubscribe" || parsed.Query().Get("email") != "a@example.com" {
		t.Fatalf("URL = %v", parsed)
	}
	token := parsed.Query().Get("token")
	if !links.Verify("a@example.com", token) || links.Verify("b@example.com", token) || links.Verify("a@example.com", "zz") {
		t.Fatal("token verification is wrong")
	}
	other := NewUnsubscribeLinks("https://news.example", []byte("another key of sixteen+"))
	if other.Verify("a@example.com", token) {
		t.Fatal("a token verified under another key")
	}
}

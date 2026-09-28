package unsubscribe

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T, fill byte) Key {
	t.Helper()
	key, err := NewKey([]byte(strings.Repeat(string(fill), 32)))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestTokenIsStableShapedAndKeyed(t *testing.T) {
	key := testKey(t, 'k')
	token := key.Token("reader@example.com")
	if token != key.Token("reader@example.com") {
		t.Fatal("the token for one address changed between calls")
	}
	if !ValidToken(token) || len(token) != TokenLength {
		t.Fatalf("token %q does not have the token shape", token)
	}
	if token == key.Token("other@example.com") {
		t.Fatal("two addresses share a token")
	}
	if token == testKey(t, 'o').Token("reader@example.com") {
		t.Fatal("two keys produce the same token")
	}
	if strings.Contains(token, "reader") {
		t.Fatalf("token %q exposes the address", token)
	}
}

func TestMatches(t *testing.T) {
	key := testKey(t, 'k')
	token := key.Token("reader@example.com")
	if !key.Matches(token, "reader@example.com") {
		t.Fatal("a token does not match its own address")
	}
	for _, address := range []string{"other@example.com", "Reader@example.com", ""} {
		if key.Matches(token, address) {
			t.Fatalf("token matched %q", address)
		}
	}
	if (Key{}).Matches(token, "reader@example.com") {
		t.Fatal("a zero Key matched a token")
	}
}

func TestValidToken(t *testing.T) {
	for token, want := range map[string]bool{
		strings.Repeat("a", 22):       true,
		"Ab0-_Ab0-_Ab0-_Ab0-_Ab":      true,
		strings.Repeat("a", 21):       false,
		strings.Repeat("a", 23):       false,
		strings.Repeat("a", 21) + "=": false,
		strings.Repeat("a", 21) + "+": false,
		strings.Repeat("a", 21) + " ": false,
		"":                            false,
	} {
		if got := ValidToken(token); got != want {
			t.Errorf("ValidToken(%q) = %v, want %v", token, got, want)
		}
	}
}

func TestNewKeyRejectsShortSecrets(t *testing.T) {
	if _, err := NewKey(make([]byte, 31)); err == nil {
		t.Fatal("NewKey accepted a 31-byte secret")
	}
}

func TestLoadKeyAcceptsBase64Files(t *testing.T) {
	secret := []byte(strings.Repeat("s", 32))
	for name, encoded := range map[string]string{
		"standard":        base64.StdEncoding.EncodeToString(secret) + "\n",
		"url, no padding": base64.RawURLEncoding.EncodeToString(secret),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
				t.Fatal(err)
			}
			key, err := LoadKey(path)
			if err != nil {
				t.Fatalf("LoadKey: %v", err)
			}
			want, _ := NewKey(secret)
			if key.Token("reader@example.com") != want.Token("reader@example.com") {
				t.Fatal("the loaded key differs from the file's secret")
			}
		})
	}
	for name, contents := range map[string]string{"not base64": "not a key!", "too short": base64.StdEncoding.EncodeToString([]byte("short"))} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadKey(path); err == nil {
				t.Fatal("LoadKey accepted an unusable key file")
			}
		})
	}
	if _, err := LoadKey(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("LoadKey accepted a missing file")
	}
}

func TestLinks(t *testing.T) {
	key := testKey(t, 'k')
	links, err := NewLinks(key, "https://news.example.com/newsletter?ref=mail#top")
	if err != nil {
		t.Fatalf("NewLinks: %v", err)
	}
	link := links.URL("reader@example.com")
	want := "https://news.example.com/newsletter?ref=mail&unsubscribe=" + key.Token("reader@example.com")
	if link != want {
		t.Fatalf("URL = %q, want %q", link, want)
	}
	if example := links.ExampleURL(); !strings.HasSuffix(example, "unsubscribe="+strings.Repeat("x", TokenLength)) {
		t.Fatalf("ExampleURL = %q", example)
	}
	for _, page := range []string{"", "/relative", "ftp://example.com/", "http://"} {
		if _, err := NewLinks(key, page); err == nil {
			t.Errorf("NewLinks accepted %q", page)
		}
	}
	if _, err := NewLinks(Key{}, "https://example.com/"); err == nil {
		t.Error("NewLinks accepted a zero Key")
	}
}

// Package unsubscribe derives the per-subscriber unsubscribe tokens that
// newsletter emails link to, and builds those links.
//
// A token is the first 128 bits of HMAC-SHA256 over the canonical address
// under an application key, base64url-encoded without padding (22
// characters). Nothing is stored per subscriber: the subscriber list Flow
// recomputes the token for each address to find the one a link names. The key
// is a runtime secret read from a file; it never enters Flow state. Rotating
// it invalidates every link already sent.
package unsubscribe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	// TokenLength is the length of every token.
	TokenLength = 22
	// minimumKeyBytes is the shortest accepted key.
	minimumKeyBytes = 32
	// tokenContext separates these tokens from any other use of the key.
	tokenContext = "dex-tech-blog/newsletter-unsubscribe/v1\x00"
	// QueryParameter names the token in an unsubscribe link.
	QueryParameter = "unsubscribe"
)

// Key signs unsubscribe tokens.
type Key struct {
	secret []byte
}

// NewKey returns a Key for secret, which must hold at least 32 bytes.
func NewKey(secret []byte) (Key, error) {
	if len(secret) < minimumKeyBytes {
		return Key{}, fmt.Errorf("unsubscribe key must be at least %d bytes, got %d", minimumKeyBytes, len(secret))
	}
	return Key{secret: append([]byte(nil), secret...)}, nil
}

// LoadKey reads a key file holding the base64 (standard or URL) encoding of
// at least 32 random bytes, such as the output of `openssl rand -base64 32`.
func LoadKey(path string) (Key, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Key{}, fmt.Errorf("read unsubscribe key: %w", err)
	}
	encoded := strings.TrimSpace(string(contents))
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if secret, decodeErr := encoding.DecodeString(encoded); decodeErr == nil {
			return NewKey(secret)
		}
	}
	return Key{}, errors.New("unsubscribe key file is not base64")
}

// Token returns the unsubscribe token for a canonical address.
func (key Key) Token(canonicalAddress string) string {
	mac := hmac.New(sha256.New, key.secret)
	mac.Write([]byte(tokenContext))
	mac.Write([]byte(canonicalAddress))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// Matches reports, in constant time for well-formed tokens, whether token is
// the token of canonicalAddress.
func (key Key) Matches(token string, canonicalAddress string) bool {
	return len(key.secret) > 0 && hmac.Equal([]byte(token), []byte(key.Token(canonicalAddress)))
}

// ValidToken reports whether token has the shape of a token.
func ValidToken(token string) bool {
	if len(token) != TokenLength {
		return false
	}
	for index := 0; index < len(token); index++ {
		character := token[index]
		isAlphanumeric := ('a' <= character && character <= 'z') || ('A' <= character && character <= 'Z') || ('0' <= character && character <= '9')
		if !isAlphanumeric && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

// Links builds unsubscribe links on the newsletter subscription page.
type Links struct {
	key  Key
	page *url.URL
}

// NewLinks returns links to pageURL, an absolute http(s) URL.
func NewLinks(key Key, pageURL string) (Links, error) {
	page, err := url.Parse(pageURL)
	if err != nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" {
		return Links{}, fmt.Errorf("subscription page URL %q must be an absolute http(s) URL", pageURL)
	}
	if len(key.secret) == 0 {
		return Links{}, errors.New("unsubscribe links need a key")
	}
	return Links{key: key, page: page}, nil
}

// URL returns the unsubscribe link for a canonical address.
func (links Links) URL(canonicalAddress string) string {
	return links.withToken(links.key.Token(canonicalAddress))
}

// ExampleURL returns a link of the real shape that unsubscribes nobody, for
// previews shown to editors.
func (links Links) ExampleURL() string {
	return links.withToken(strings.Repeat("x", TokenLength))
}

func (links Links) withToken(token string) string {
	link := *links.page
	query := link.Query()
	query.Set(QueryParameter, token)
	link.RawQuery = query.Encode()
	link.Fragment = ""
	return link.String()
}

package subscribers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidAddress rejects anything that is not one plain email address.
var ErrInvalidAddress = errors.New("enter one email address, such as you@example.com")

// CanonicalAddress trims and lowercases a plain address; display names and lists are rejected.
func CanonicalAddress(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 254 || strings.ContainsAny(trimmed, " <>,;\"\r\n") {
		return "", ErrInvalidAddress
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed.Name != "" || parsed.Address != trimmed {
		return "", ErrInvalidAddress
	}
	local, domain, found := strings.Cut(parsed.Address, "@")
	if !found || local == "" || !strings.Contains(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidAddress
	}
	return strings.ToLower(parsed.Address), nil
}

// UnsubscribeLinks signs and verifies per-address unsubscribe links.
type UnsubscribeLinks struct {
	baseURL string
	key     []byte
}

// LoadUnsubscribeLinks reads the HMAC key, creating a random 32-byte key when the file is absent.
func LoadUnsubscribeLinks(baseURL, keyFile string) (UnsubscribeLinks, error) {
	contents, err := os.ReadFile(keyFile)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return UnsubscribeLinks{}, err
		}
		if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
			return UnsubscribeLinks{}, err
		}
		contents = []byte(hex.EncodeToString(key))
		if err := os.WriteFile(keyFile, contents, 0o600); err != nil {
			return UnsubscribeLinks{}, err
		}
	} else if err != nil {
		return UnsubscribeLinks{}, fmt.Errorf("read unsubscribe key: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(contents)))
	if err != nil || len(key) < 16 {
		return UnsubscribeLinks{}, errors.New("unsubscribe key file must hold at least 16 hex-encoded bytes")
	}
	return NewUnsubscribeLinks(baseURL, key), nil
}

func NewUnsubscribeLinks(baseURL string, key []byte) UnsubscribeLinks {
	return UnsubscribeLinks{baseURL: strings.TrimRight(baseURL, "/"), key: append([]byte(nil), key...)}
}

// Token is the hex HMAC-SHA256 of the canonical address, truncated to 128 bits.
func (links UnsubscribeLinks) Token(address string) string {
	mac := hmac.New(sha256.New, links.key)
	mac.Write([]byte("unsubscribe\x00" + address))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// Verify reports whether token was issued for address.
func (links UnsubscribeLinks) Verify(address, token string) bool {
	expected, err := hex.DecodeString(links.Token(address))
	if err != nil {
		return false
	}
	actual, err := hex.DecodeString(strings.TrimSpace(token))
	return err == nil && hmac.Equal(expected, actual)
}

// URL is the public unsubscribe page for address.
func (links UnsubscribeLinks) URL(address string) string {
	query := url.Values{"email": {address}, "token": {links.Token(address)}}
	return links.baseURL + "/unsubscribe?" + query.Encode()
}

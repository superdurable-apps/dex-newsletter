package notices

import "testing"

// Fixtures in a real provider token format are assembled from two literals, so
// the source never holds a value that secret scanners (GitHub push protection,
// gitleaks, TruffleHog) would flag. None of them is a real credential.
const (
	fakeGoogleClientSecret = "GOCSPX-" + "fake-test-secret-value"
	fakeSlackAppToken      = "xapp-" + "1-A0123-4567-abcdef"
	fakeGoogleAccessToken  = "ya29." + "a0AfH6SMBabcdefghij"
)

func TestRedactSensitiveText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		// Email addresses.
		{name: "email", text: "contact bob@example.com.", want: "contact [redacted email]."},
		{name: "email with tag and subdomain", text: "Bob.Smith+news@mail.example.co.uk", want: "[redacted email]"},
		{name: "internationalized local part", text: "jos\xc3\xa9@example.com", want: "[redacted email]"},
		{name: "punycode top-level domain", text: "user@example.xn--p1ai", want: "[redacted email]"},
		{name: "percent-encoded email", text: "mailto:bob%40example.com", want: "mailto:[redacted email]"},
		{name: "git remote address", text: "git@github.com:owner/repo", want: "[redacted email]:owner/repo"},
		{name: "several emails", text: "a@example.com, b@example.org", want: "[redacted email], [redacted email]"},
		{name: "quoted local part", text: `send failed for "john"@example.com`, want: "send failed for [redacted email]"},
		{name: "quoted local part with a space", text: `"john doe"@example.com`, want: "[redacted email]"},
		{name: "doubled at sign", text: "jane@@gmail.com", want: "[redacted email]"},
		{name: "address without a top-level domain", text: "invalid subscriber jane.doe@gmail", want: "invalid subscriber [redacted email]"},
		{name: "internal address", text: "bob@localhost:8080 refused", want: "[redacted email]:8080 refused"},
		{name: "address with a comma for a dot", text: "jane@gmail,com", want: "[redacted email],com"},
		{name: "address with a one-letter top-level label", text: "bob@example.c", want: "[redacted email].c"},
		{name: "address in a URL query", text: "https://example.com/?q=bob@localhost", want: "https://example.com/[redacted email]"},
		{name: "address in a URL path", text: "https://example.com/users/jane@example.com", want: "https://example.com/users/[redacted email]"},
		{name: "slash-delimited prefix stays outside the address", text: "john/doe@example.com", want: "john/[redacted email]"},
		// Credentials in URLs, headers, and assignments.
		{name: "URL user and password", text: "https://user:hunter2@example.com/x", want: "https://[redacted]@example.com/x"},
		{name: "URL token as user", text: "https://ghp_abcdefghijklmnopqrstuvwxyz0123@github.com/o/r", want: "https://[redacted]@github.com/o/r"},
		{name: "API key query parameter in an error", text: "Post \"https://g.example/v1:gen?key=AIzaSyA1234567890123456789012345678901\": EOF", want: "Post \"https://g.example/v1:gen?key=[redacted]\": EOF"},
		{name: "authorization header", text: "Authorization: Bearer abc.def-ghi", want: "Authorization: [redacted]"},
		{name: "JSON API key field", text: `{"api_key": "s3cr3t"}`, want: `{"api_key": "[redacted]"}`},
		{name: "password field", text: "password: hunter2", want: "password: [redacted]"},
		{name: "token assignment", text: "token=abc123&x=1", want: "token=[redacted]&x=1"},
		{name: "signed URL signature", text: "X-Amz-Signature=deadbeef", want: "X-Amz-Signature=[redacted]"},
		{name: "bare bearer token", text: "bearer abcdefghijkl", want: "bearer [redacted]"},
		// Credential names with prefixes, spacing, and quoting.
		{name: "UPPER_SNAKE password", text: "DB_PASSWORD=hunter2", want: "DB_PASSWORD=[redacted]"},
		{name: "UPPER_SNAKE token", text: "GITHUB_TOKEN=0123456789abcdef0123456789abcdef01234567", want: "GITHUB_TOKEN=[redacted]"},
		{name: "AWS secret access key", text: "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", want: "AWS_SECRET_ACCESS_KEY=[redacted]"},
		{name: "Slack signing secret in a config error", text: "config: SLACK_SIGNING_SECRET=8f742231b10e8888abcd99yyyzzz85a5 invalid", want: "config: SLACK_SIGNING_SECRET=[redacted] invalid"},
		{name: "Google client secret assignment", text: "GOOGLE_CLIENT_SECRET=" + fakeGoogleClientSecret, want: "GOOGLE_CLIENT_SECRET=[redacted]"},
		{name: "Google client secret alone", text: "client secret " + fakeGoogleClientSecret + " rejected", want: "client secret [redacted] rejected"},
		{name: "camelCase token", text: "accessToken=abc123&x=1", want: "accessToken=[redacted]&x=1"},
		{name: "dotted config key", text: "spring.datasource.password=hunter2", want: "spring.datasource.password=[redacted]"},
		{name: "command-line flag", text: "--api-key=abcdef123456", want: "--api-key=[redacted]"},
		{name: "spaced assignment", text: "password = hunter2", want: "password = [redacted]"},
		{name: "spaced API key assignment", text: "api_key = abcdef123456", want: "api_key = [redacted]"},
		{name: "double-quoted assignment with spaces", text: `PASSWORD="correct horse battery" next`, want: `PASSWORD="[redacted]" next`},
		{name: "single-quoted assignment", text: "export SECRET_KEY='s3cr3t'", want: "export SECRET_KEY='[redacted]'"},
		{name: "unterminated quoted assignment", text: `token="abc123`, want: "token=[redacted]"},
		{name: "snake_case YAML field", text: "db_password: hunter2", want: "db_password: [redacted]"},
		{name: "UPPER_SNAKE token field", text: "SLACK_BOT_TOKEN: abcdefgh", want: "SLACK_BOT_TOKEN: [redacted]"},
		{name: "JSON secret field", text: `{"secret": "s3cr3t-value"}`, want: `{"secret": "[redacted]"}`},
		{name: "JSON token field", text: `{"token": "t0ken-value"}`, want: `{"token": "[redacted]"}`},
		{name: "JSON camelCase token field", text: `{"githubToken":"abc"}`, want: `{"githubToken":"[redacted]"}`},
		{name: "JSON passphrase with spaces", text: `{"password": "correct horse battery"}`, want: `{"password": "[redacted]"}`},
		{name: "single-quoted field", text: "password: 'hunter2'", want: "password: '[redacted]'"},
		{name: "cookie header", text: "Cookie: session=abcdef0123456789", want: "Cookie: [redacted]"},
		{name: "set-cookie header with attributes", text: "Set-Cookie: sid=abc; Path=/; HttpOnly next", want: "Set-Cookie: [redacted] next"},
		// Well-known token formats.
		{name: "Slack bot token", text: "xoxb-123456789012-abcdefgh", want: "[redacted]"},
		{name: "Slack app token", text: fakeSlackAppToken, want: "[redacted]"},
		{name: "Slack webhook", text: "https://hooks.slack.com/services/T000/B000/XXXX", want: "https://hooks.slack.com/[redacted]"},
		{name: "GitHub token", text: "ghp_abcdefghijklmnopqrstuvwxyz0123", want: "[redacted]"},
		{name: "GitHub fine-grained token", text: "github_pat_11ABCDEFG0123456789_abcdefghijklmnop", want: "[redacted]"},
		{name: "Google API key", text: "AIzaSyA1234567890123456789012345678901 alone", want: "[redacted] alone"},
		{name: "Google access token", text: fakeGoogleAccessToken, want: "[redacted]"},
		{name: "Google refresh token", text: "refresh 1//0gabcdefghijklmnopqrstuvwxyz", want: "refresh [redacted]"},
		{name: "OpenAI key", text: "sk-proj-abcdefghijklmnop1234", want: "[redacted]"},
		{name: "Anthropic key", text: "sk-ant-api03-abcdefghijklmnop", want: "[redacted]"},
		{name: "AWS access key ID", text: "AKIAIOSFODNN7EXAMPLE", want: "[redacted]"},
		{name: "JSON Web Token", text: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", want: "[redacted]"},
		{name: "private key block", text: "-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----\nafter", want: "[redacted private key]\nafter"},
		{name: "unterminated private key block", text: "x -----BEGIN PRIVATE KEY-----\nMIIE", want: "x [redacted private key]"},
		// Ordinary technical prose stays unchanged.
		{name: "prose about tokens and keys", text: "Improved token refresh and key rotation for connectors.", want: "Improved token refresh and key rotation for connectors."},
		{name: "prose with bearer", text: "The bearer of good news", want: "The bearer of good news"},
		{name: "prose about passwords", text: "password reset flow", want: "password reset flow"},
		{name: "prose key colon", text: "key: the new flow", want: "key: the new flow"},
		{name: "Go module version", text: "Upgrade to github.com/superdurable/dex/sdk-go@v0.12.1", want: "Upgrade to github.com/superdurable/dex/sdk-go@v0.12.1"},
		{name: "npm scoped package version", text: "npm i @angular/core@17.0.0", want: "npm i @angular/core@17.0.0"},
		{name: "GitHub handle", text: "@octocat opened #42", want: "@octocat opened #42"},
		{name: "environment assignment", text: "Set GOPROXY=direct", want: "Set GOPROXY=direct"},
		{name: "identifier ending in key", text: "primary_key=5 monkey=banana public_key=ssh-rsa", want: "primary_key=5 monkey=banana public_key=ssh-rsa"},
		{name: "token counts and limits", text: "max_tokens=500 token_count=5 TOKEN_LIMIT=9", want: "max_tokens=500 token_count=5 TOKEN_LIMIT=9"},
		{name: "comparison is not an assignment", text: `if token == "" || secret != nil`, want: `if token == "" || secret != nil`},
		{name: "Go short variable declaration", text: `token := os.Getenv("GITHUB_TOKEN")`, want: `token := os.Getenv("GITHUB_TOKEN")`},
		{name: "prose token colon", text: "Token: the new refresh flow", want: "Token: the new refresh flow"},
		{name: "prose secret colon", text: "Secret: rotation is now automatic", want: "Secret: rotation is now automatic"},
		{name: "prose cookie colon", text: "cookie: consent banner redesign", want: "cookie: consent banner redesign"},
		{name: "JSON gap before a handle", text: `{"handle": "@octocat.dev"}`, want: `{"handle": "@octocat.dev"}`},
		{name: "GitHub Actions references", text: "uses: actions/checkout@v4 and actions/setup-go@main", want: "uses: actions/checkout@v4 and actions/setup-go@main"},
		{name: "package dist-tags", text: "npm i lodash@latest react@next-15 @scope/pkg@canary", want: "npm i lodash@latest react@next-15 @scope/pkg@canary"},
		{name: "module path at a branch", text: "go get github.com/superdurable/dex/sdk-go@feature-x", want: "go get github.com/superdurable/dex/sdk-go@feature-x"},
		{name: "container digest", text: "alpine@sha256:abcdef0123", want: "alpine@sha256:abcdef0123"},
		{name: "commit reference", text: "fixed in dex@4f2a9c1 and dex@a1b2c3d", want: "fixed in dex@4f2a9c1 and dex@a1b2c3d"},
		{name: "version with a dotted suffix", text: "pinned pkg@x.1 and sdk@v4", want: "pinned pkg@x.1 and sdk@v4"},
		{name: "pull request URL", text: "https://github.com/superdurable/dex/pull/42", want: "https://github.com/superdurable/dex/pull/42"},
		{name: "region name", text: "Asia-Pacific region", want: "Asia-Pacific region"},
		{name: "short sk- word", text: "sk-learn", want: "sk-learn"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := redactSensitiveText(test.text)
			if got != test.want {
				t.Errorf("redactSensitiveText(%q) = %q, want %q", test.text, got, test.want)
			}
			if again := redactSensitiveText(got); again != got {
				t.Errorf("redaction is not idempotent: %q became %q", got, again)
			}
		})
	}
}

package notices

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Invisible and control characters are spelled as byte escapes so the source
// stays plain ASCII.
const (
	testZeroWidthSpace      = "\xe2\x80\x8b"
	testLineSeparator       = "\xe2\x80\xa8"
	testNextLine            = "\xc2\x85"
	testRightToLeftOverride = "\xe2\x80\xae"
	testByteOrderMark       = "\xef\xbb\xbf"
)

// adversarialText combines every Slack control sequence, formatting and
// whitespace trick, email address, and secret the invariant tests look for.
const adversarialText = "<!channel> <!here> <!everyone> <@U123ABC> <#C123|general> <!subteam^S123> @channel @here @everyone " +
	"<https://evil.example|Click me> *bold* `code` & &amp; &lt;!channel&gt; | pipe > arrow\n" +
	"line two\r\nline three" + testLineSeparator + "separator" + testNextLine + "next\x00nul" +
	testRightToLeftOverride + "override" + testZeroWidthSpace + "zero" + testByteOrderMark + "mark \xff invalid\n" +
	"victim@example.com Victim.Name+tag@sub.example.co.uk bob%40example.org vic" + testZeroWidthSpace + "tim2@example.com " +
	"https://generativelanguage.googleapis.com/v1beta/models/m:generateContent?key=AIzaSyA1234567890123456789012345678901 " +
	"xoxb-1234567890-abcdefghij ghp_abcdefghijklmnopqrstuvwxyz0123 sk-proj-abcdefghijklmnop1234 " +
	"Authorization: Bearer Zm9vYmFyYmF6cXV4MTIz https://user:hunter2@example.com/path\n" +
	"DB_PASSWORD=sup3rs3cret GOOGLE_CLIENT_SECRET = \"" + fakeGoogleClientSecret + "\" {\"token\": \"t0kenvalue123\"} " +
	"Cookie: sid=c00kievalue; theme=dark \"john\"@example.com jane.doe@gmail"

// leakedValues must never appear in a notice, even with zero-width spaces
// removed.
var leakedValues = []string{
	"<!channel>", "<!here>", "<!everyone>", "<@U123ABC>", "<#C123", "<!subteam",
	"victim@example.com", "Victim.Name+tag@sub.example.co.uk", "bob%40example.org", "victim2@example.com",
	"AIzaSyA1234567890123456789012345678901", "xoxb-1234567890-abcdefghij", "ghp_abcdefghijklmnopqrstuvwxyz0123",
	"sk-proj-abcdefghijklmnop1234", "Zm9vYmFyYmF6cXV4MTIz", "hunter2",
	"sup3rs3cret", "GOCSPX-", "t0kenvalue123", "c00kievalue", "\"john\"@", "jane.doe@gmail",
}

var (
	constructedSlackLinkPattern = regexp.MustCompile(`<(https?://[^\s<>|]+)\|([^<>|]+)>`)
	anyEmailAddressPattern      = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
)

// assertSlackSafe checks the invariants every notice must satisfy.
func assertSlackSafe(t *testing.T, name string, message string) {
	t.Helper()
	if !utf8.ValidString(message) {
		t.Errorf("%s: message is not valid UTF-8", name)
	}
	if runes := utf8.RuneCountInString(message); runes > maximumMessageRunes {
		t.Errorf("%s: message has %d runes, want at most %d", name, runes, maximumMessageRunes)
	}
	for _, link := range constructedSlackLinkPattern.FindAllStringSubmatch(message, -1) {
		if strings.Contains(link[1], "&") && !strings.Contains(link[1], "&amp;") {
			t.Errorf("%s: link target %q has an unescaped '&'", name, link[1])
		}
	}
	// Replace every constructed link with its label, then nothing may open a
	// Slack control sequence and '>' may only start a quoted line.
	withoutLinks := constructedSlackLinkPattern.ReplaceAllString(message, "$2")
	if strings.Contains(withoutLinks, "<") {
		t.Errorf("%s: raw '<' outside a constructed link in %q", name, message)
	}
	for _, line := range strings.Split(withoutLinks, "\n") {
		content := line
		if line == ">" {
			content = ""
		} else if strings.HasPrefix(line, "> ") {
			content = line[2:]
		}
		if strings.Contains(content, ">") {
			t.Errorf("%s: raw '>' in line %q", name, line)
		}
	}
	for index := strings.IndexByte(withoutLinks, '@'); index >= 0; {
		if !strings.HasPrefix(withoutLinks[index+1:], testZeroWidthSpace) {
			t.Errorf("%s: '@' without a following zero-width space in %q", name, withoutLinks)
			break
		}
		next := strings.IndexByte(withoutLinks[index+1:], '@')
		if next < 0 {
			break
		}
		index += 1 + next
	}
	visible := strings.ReplaceAll(message, testZeroWidthSpace, "")
	for _, leaked := range leakedValues {
		if strings.Contains(visible, leaked) {
			t.Errorf("%s: message leaks %q", name, leaked)
		}
	}
	if email := anyEmailAddressPattern.FindString(visible); email != "" {
		t.Errorf("%s: message contains email address %q", name, email)
	}
	for _, character := range message {
		if character == '\n' {
			continue
		}
		if unicode.Is(unicode.Cc, character) {
			t.Errorf("%s: message contains control character %U", name, character)
		}
		if unicode.Is(unicode.Cf, character) && character != 0x200B {
			t.Errorf("%s: message contains format character %U", name, character)
		}
	}
}

// allNoticesFor renders every notice from the same arguments.
func allNoticesFor(text string, post model.BlogPost, runReference RunReference, repositories []model.RepositoryReference, summary model.DeliverySummary, urlText string, number int) map[string]string {
	coverage := PartialResearchCoverage(repositories)
	return map[string]string{
		"RequestAcknowledged":  RequestAcknowledged(),
		"ClarificationNeeded":  ClarificationNeeded(text),
		"ResearchStarted":      ResearchStarted(text, text, repositories),
		"NoNotableChanges":     NoNotableChanges(text, text),
		"DraftReadyForReview":  DraftReadyForReview(post, runReference, urlText, number),
		"DraftReadyWithPath":   DraftReadyForReview(post, runReference, text, number),
		"ReviewReminder":       ReviewReminder(post, runReference, number),
		"ReviewExpired":        ReviewExpired(post),
		"DraftDiscarded":       DraftDiscarded(post, text),
		"RevisionStarted":      RevisionStarted(number, text),
		"NeedsAttention":       NeedsAttention(text, text, runReference),
		"NoSubscribers":        NoSubscribers(post),
		"DeliveryReport":       DeliveryReport(post, summary, urlText, urlText),
		"DeliveryReportNoLink": DeliveryReport(post, summary, text, text),
		"DeliveryHeld":         DeliveryHeldForAttention(post, summary, text, runReference),
		"RequestFailed":        RequestFailed(text),
		"DeliveryStopped":      DeliveryStopped(post, summary, text, urlText, urlText),
		// The Flow appends the coverage line to these two notices.
		"PartialResearchCoverage":      coverage,
		"NoNotableChangesWithCoverage": NoNotableChanges(text, text) + "\n" + coverage,
		"DraftReadyWithCoverage":       DraftReadyForReview(post, runReference, urlText, number) + "\n" + coverage,
		"DraftReadyWithPathCoverage":   DraftReadyForReview(post, runReference, text, number) + "\n" + coverage,
	}
}

func adversarialPost(text string, count int) model.BlogPost {
	post := model.BlogPost{Title: text, Subtitle: text, Slug: text, Summary: text}
	for index := 0; index < count; index++ {
		post.Tags = append(post.Tags, text)
		post.Sections = append(post.Sections, model.BlogSection{Heading: text})
		post.References = append(post.References, model.SourceReference{Label: text, URL: text})
	}
	return post
}

func adversarialRepositories(text string, count int) []model.RepositoryReference {
	repositories := make([]model.RepositoryReference, 0, count)
	for index := 0; index < count; index++ {
		repositories = append(repositories, model.RepositoryReference{Owner: text, Name: text})
	}
	return repositories
}

func TestNoticesAreSlackSafeForAdversarialInput(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		runReference RunReference
		urlText      string
	}{
		{name: "adversarial text, adversarial run reference", text: adversarialText, runReference: RunReference{FlowID: adversarialText, DexWebURL: adversarialText}, urlText: adversarialText},
		{name: "adversarial text, valid Dex Web URL", text: adversarialText, runReference: RunReference{FlowID: "<!channel>|x>@here&y", DexWebURL: "https://dex.example.com/"}, urlText: "https://blog.example.com/posts/a?x=1&y=2"},
		{name: "mentions only", text: "<!channel> <@U123ABC> @here", runReference: RunReference{FlowID: "<@U123ABC>"}, urlText: "javascript:alert(1)"},
		{name: "entities already escaped", text: "&lt;!channel&gt; &amp;lt;@U123ABC&amp;gt;", runReference: RunReference{FlowID: "&lt;!here&gt;", DexWebURL: "https://dex.example.com"}, urlText: "https://blog.example.com/a|b"},
		{name: "fake quoted lines", text: "fine\n> *Sent:* 999\n>>> <!channel>", runReference: RunReference{FlowID: "f\n> x"}, urlText: "https://blog.example.com/\n<!channel>"},
		{name: "email and secrets only", text: "victim@example.com key=AIzaSyA1234567890123456789012345678901", runReference: RunReference{FlowID: "victim@example.com", DexWebURL: "https://dex.example.com"}, urlText: "https://blog.example.com/?token=hunter2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := adversarialPost(test.text, 3)
			repositories := adversarialRepositories(test.text, 3)
			summary := model.DeliverySummary{Recipients: 3, Sent: 1, Rejected: 1, Uncertain: 1, SkippedOverLimit: 2, InvalidAddresses: 1}
			for name, message := range allNoticesFor(test.text, post, test.runReference, repositories, summary, test.urlText, 2) {
				assertSlackSafe(t, name, message)
			}
		})
	}
}

func TestNoticesStayWithinMessageLimitForMaximumInput(t *testing.T) {
	// Every character of expanding text escapes to several runes.
	expandingText := strings.Repeat("&@ ", 20000)
	manyLines := strings.Repeat("&&&&&&&&&&&&&&&&&&&&\n", 2000)
	longDexWebURL := "https://dex.example.com/" + strings.Repeat("d", 560)
	longArtifactURL := "https://artifacts.example.com/" + strings.Repeat("a", 560)
	tests := []struct {
		name         string
		text         string
		runReference RunReference
		urlText      string
	}{
		{name: "expanding single-line text with longest links", text: expandingText, runReference: RunReference{FlowID: "flow", DexWebURL: longDexWebURL}, urlText: longArtifactURL},
		{name: "many lines with longest links", text: manyLines, runReference: RunReference{FlowID: "flow", DexWebURL: longDexWebURL}, urlText: longArtifactURL},
		{name: "expanding text with Flow ID fallback", text: expandingText, runReference: RunReference{FlowID: expandingText}, urlText: expandingText},
		{name: "input beyond the input cap", text: strings.Repeat("x ", 100000), runReference: RunReference{FlowID: strings.Repeat("x", 200000), DexWebURL: longDexWebURL}, urlText: strings.Repeat("y", 200000)},
	}
	summary := model.DeliverySummary{Recipients: math.MaxInt, Sent: math.MaxInt - 1, Rejected: math.MaxInt, Uncertain: math.MaxInt, Defect: math.MaxInt,
		SkippedOverLimit: math.MaxInt, InvalidAddresses: math.MaxInt}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := adversarialPost(test.text, 50)
			repositories := adversarialRepositories(test.text, 50)
			for name, message := range allNoticesFor(test.text, post, test.runReference, repositories, summary, test.urlText, math.MaxInt) {
				assertSlackSafe(t, name, message)
				if strings.Contains(message, messageTruncationMarker) {
					t.Errorf("%s: dynamic budgets exceeded the message limit and the guard cut the message", name)
				}
			}
		})
	}
	longestLinkMessage := DraftReadyForReview(adversarialPost(expandingText, 50), RunReference{FlowID: "flow", DexWebURL: longDexWebURL}, longArtifactURL, math.MaxInt)
	if !strings.Contains(longestLinkMessage, "<"+longDexWebURL+"/v2/runs/flow|") || !strings.Contains(longestLinkMessage, "<"+longArtifactURL+"|") {
		t.Errorf("maximum-size case did not exercise both links: %q", longestLinkMessage)
	}
}

func TestNoticesAreDeterministic(t *testing.T) {
	post := adversarialPost(adversarialText, 8)
	repositories := adversarialRepositories(adversarialText, 12)
	runReference := RunReference{FlowID: "flow-1", DexWebURL: "https://dex.example.com"}
	summary := model.DeliverySummary{Recipients: 5, Sent: 4, Defect: 1}
	first := allNoticesFor(adversarialText, post, runReference, repositories, summary, "https://blog.example.com/p", 3)
	second := allNoticesFor(adversarialText, post, runReference, repositories, summary, "https://blog.example.com/p", 3)
	for name, message := range first {
		if second[name] != message {
			t.Errorf("%s: output differs between identical calls", name)
		}
	}
}

func TestNoticeText(t *testing.T) {
	post := model.BlogPost{
		Title:   "Connector triggers land in Dex",
		Summary: "Slack and Gmail connectors can now start Flows.",
		Tags:    []string{"connectors", " ", "triggers"},
		Sections: []model.BlogSection{
			{Heading: "What changed"},
			{Heading: ""},
			{Heading: "How it works"},
		},
	}
	linkedRun := RunReference{FlowID: "techblog/Ev01", DexWebURL: "https://dex.example.com/"}
	repositories := []model.RepositoryReference{
		{Owner: "superdurable", Name: "dex"},
		{Owner: " ", Name: " "},
		{Owner: "superdurable", Name: "dex-connectors-library"},
		{Name: "orphan"},
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "RequestAcknowledged",
			got:  RequestAcknowledged(),
			want: ":mag: On it — researching recent changes for this request. I'll post updates in this thread.",
		},
		{
			name: "ClarificationNeeded with question",
			got:  ClarificationNeeded("Which repositories?\r\n\r\n\r\n  And over   what time range?  "),
			want: ":thinking_face: I need a little more detail before I can research this.\n> Which repositories?\n>\n> And over what time range?\nPost a new message in this channel with the details and I'll start again.",
		},
		{
			name: "ClarificationNeeded with blank question",
			got:  ClarificationNeeded(" \n\t" + testZeroWidthSpace),
			want: ":thinking_face: I need a little more detail before I can research this.\n> " + defaultClarificationQuestion + "\nPost a new message in this channel with the details and I'll start again.",
		},
		{
			name: "ClarificationNeeded with padding beyond the input cap",
			got:  ClarificationNeeded(strings.Repeat(" ", 20000) + "Which repo?"),
			want: ":thinking_face: I need a little more detail before I can research this.\n> Which repo?\nPost a new message in this channel with the details and I'll start again.",
		},
		{
			name: "ClarificationNeeded with nothing showable",
			got:  ClarificationNeeded(strings.Repeat("x", 20000)),
			want: ":thinking_face: I need a little more detail before I can research this.\n> " + defaultClarificationQuestion + "\nPost a new message in this channel with the details and I'll start again.",
		},
		{
			name: "ClarificationNeeded with long CJK question",
			got:  ClarificationNeeded(strings.Repeat("请说明要写哪个仓库，", 400)),
			want: ":thinking_face: I need a little more detail before I can research this.\n> " + strings.Repeat("请说明要写哪个仓库，", 119) + "请说明要写哪个仓库…\nPost a new message in this channel with the details and I'll start again.",
		},
		{
			name: "RequestFailed with line-break padding beyond the input cap",
			got:  RequestFailed(strings.Repeat("\n", 20000) + "real reason"),
			want: ":x: Sorry, I couldn't finish this request.\n> real reason\nPost a new message in this channel to try again.",
		},
		{
			name: "DraftReadyForReview with an oversized one-token title and CJK summary",
			got:  DraftReadyForReview(model.BlogPost{Title: strings.Repeat("x", 5000), Summary: strings.Repeat("连接器现在可以启动流程。", 200)}, RunReference{}, "", 0),
			want: ":memo: *Draft ready for review*\n*Title:* (untitled draft)\n*Summary:* " + strings.Repeat("连接器现在可以启动流程。", 41) + "连接器现在可以…\n*Review:* Open this run in Dex Web to approve it, request changes, or discard it.",
		},
		{
			name: "ResearchStarted",
			got:  ResearchStarted("connectors", "Sep 12 – Sep 26, 2026 (past 14 days)", repositories),
			want: ":hourglass_flowing_sand: Researching recent changes.\n*Topic:* connectors\n*Window:* Sep 12 – Sep 26, 2026 (past 14 days)\n*Repositories (3):* `superdurable/dex`, `superdurable/dex-connectors-library`, `orphan`",
		},
		{
			name: "ResearchStarted with nothing to show",
			got:  ResearchStarted(" ", "", nil),
			want: ":hourglass_flowing_sand: Researching recent changes.",
		},
		{
			name: "ResearchStarted with more repositories than listed",
			got:  ResearchStarted("x", "", adversarialRepositories("r", 12)),
			want: ":hourglass_flowing_sand: Researching recent changes.\n*Topic:* x\n*Repositories (12):* " + strings.TrimSuffix(strings.Repeat("`r/r`, ", 10), ", ") + " (+2 more)",
		},
		{
			name: "NoNotableChanges",
			got:  NoNotableChanges("connectors", "Sep 19 – Sep 26, 2026 (past 7 days)"),
			want: ":zzz: I didn't find notable changes to write about, so I didn't draft a blog post.\n*Topic:* connectors\n*Window:* Sep 19 – Sep 26, 2026 (past 7 days)\nPost a new message with a longer time range or a different topic to try again.",
		},
		{
			name: "DraftReadyForReview first draft",
			got:  DraftReadyForReview(post, linkedRun, "artifacts/connector-triggers.html", 0),
			want: ":memo: *Draft ready for review*\n*Title:* Connector triggers land in Dex\n*Summary:* Slack and Gmail connectors can now start Flows.\n*Sections:* What changed · How it works\n*Tags:* connectors, triggers\n*Preview:* `artifacts/connector-triggers.html`\n*Review:* <https://dex.example.com/v2/runs/techblog%2FEv01|Open the review in Dex Web> to approve it, request changes, or discard it.",
		},
		{
			name: "DraftReadyForReview revision with linked preview and Flow ID fallback",
			got:  DraftReadyForReview(post, RunReference{FlowID: "flow-7"}, "https://artifacts.example.com/p.html?v=2&x=1", 2),
			want: ":memo: *Draft ready for review* (revision 2)\n*Title:* Connector triggers land in Dex\n*Summary:* Slack and Gmail connectors can now start Flows.\n*Sections:* What changed · How it works\n*Tags:* connectors, triggers\n*Preview:* <https://artifacts.example.com/p.html?v=2&amp;x=1|Open the HTML preview>\n*Review:* Open Flow `flow-7` in Dex Web to approve it, request changes, or discard it.",
		},
		{
			name: "DraftReadyForReview empty",
			got:  DraftReadyForReview(model.BlogPost{}, RunReference{}, "", -3),
			want: ":memo: *Draft ready for review*\n*Title:* (untitled draft)\n*Review:* Open this run in Dex Web to approve it, request changes, or discard it.",
		},
		{
			name: "DraftReadyForReview with more sections and tags than listed",
			got:  DraftReadyForReview(adversarialPost("s", 9), RunReference{}, "", 0),
			want: ":memo: *Draft ready for review*\n*Title:* s\n*Summary:* s\n*Sections:* s · s · s · s · s · s (+3 more)\n*Tags:* s, s, s, s, s, s (+3 more)\n*Review:* Open this run in Dex Web to approve it, request changes, or discard it.",
		},
		{
			name: "ReviewReminder numbered",
			got:  ReviewReminder(post, linkedRun, 2),
			want: ":bell: Reminder 2: this draft is still waiting for editor review.\n*Title:* Connector triggers land in Dex\n*Review:* <https://dex.example.com/v2/runs/techblog%2FEv01|Open the review in Dex Web> to approve it, request changes, or discard it.",
		},
		{
			name: "ReviewReminder unnumbered",
			got:  ReviewReminder(model.BlogPost{}, RunReference{FlowID: "flow-1"}, 0),
			want: ":bell: Reminder: this draft is still waiting for editor review.\n*Title:* (untitled draft)\n*Review:* Open Flow `flow-1` in Dex Web to approve it, request changes, or discard it.",
		},
		{
			name: "ReviewExpired",
			got:  ReviewExpired(post),
			want: ":hourglass: The review window closed without a decision, so the newsletter was not sent.\n*Title:* Connector triggers land in Dex\nPost a new request in this channel if you still want to publish this post.",
		},
		{
			name: "DraftDiscarded with reason",
			got:  DraftDiscarded(post, "Not ready.\nToo long."),
			want: ":wastebasket: The draft was discarded, so the newsletter was not sent.\n*Title:* Connector triggers land in Dex\n*Reason:*\n> Not ready.\n> Too long.",
		},
		{
			name: "DraftDiscarded without reason",
			got:  DraftDiscarded(post, "   "),
			want: ":wastebasket: The draft was discarded, so the newsletter was not sent.\n*Title:* Connector triggers land in Dex",
		},
		{
			name: "RevisionStarted with feedback",
			got:  RevisionStarted(1, "Shorten the intro.\n- Add a code sample"),
			want: ":pencil2: Revising the draft (revision 1) based on editor feedback.\n*Feedback:*\n> Shorten the intro.\n> - Add a code sample\nI'll post the revised draft in this thread when it's ready.",
		},
		{
			name: "RevisionStarted without feedback or number",
			got:  RevisionStarted(0, ""),
			want: ":pencil2: Revising the draft based on editor feedback.\nI'll post the revised draft in this thread when it's ready.",
		},
		{
			name: "NeedsAttention",
			got:  NeedsAttention("draft blog post", "Gemini returned status 503.", linkedRun),
			want: ":warning: *This request needs attention.*\n*Stage:* draft blog post\n*Details:*\n> Gemini returned status 503.\n*Run:* <https://dex.example.com/v2/runs/techblog%2FEv01|Open the run in Dex Web>",
		},
		{
			name: "NeedsAttention empty",
			got:  NeedsAttention("", "", RunReference{DexWebURL: "https://dex.example.com"}),
			want: ":warning: *This request needs attention.*",
		},
		{
			name: "NoSubscribers",
			got:  NoSubscribers(post),
			want: ":mailbox_with_no_mail: The newsletter was not sent because the subscriber list has no deliverable addresses.\n*Title:* Connector triggers land in Dex\nReaders can subscribe on the application's home page. Once someone has subscribed, post a new request to try again.",
		},
		{
			name: "RequestFailed with reason",
			got:  RequestFailed("GitHub returned 404 for superdurable/missing."),
			want: ":x: Sorry, I couldn't finish this request.\n> GitHub returned 404 for superdurable/missing.\nPost a new message in this channel to try again.",
		},
		{
			name: "RequestFailed without reason",
			got:  RequestFailed(""),
			want: ":x: Sorry, I couldn't finish this request.\nPost a new message in this channel to try again.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Errorf("got\n%s\nwant\n%s", test.got, test.want)
			}
		})
	}
}

func TestDeliveryReport(t *testing.T) {
	post := model.BlogPost{Title: "Connector triggers | <v2>"}
	tests := []struct {
		name         string
		summary      model.DeliverySummary
		publishedURL string
		artifactPath string
		want         string
	}{
		{
			name:         "all sent with published link and artifact",
			summary:      model.DeliverySummary{Recipients: 42, Sent: 42},
			publishedURL: "https://blog.example.com/posts/connector-triggers",
			artifactPath: "artifacts/connector-triggers.html",
			want:         ":white_check_mark: Newsletter sent to 42 subscribers.\n*Published post:* <https://blog.example.com/posts/connector-triggers|Connector triggers ¦ &lt;v2&gt;>\n*Blog artifact:* `artifacts/connector-triggers.html`",
		},
		{
			name:    "one subscriber without published link",
			summary: model.DeliverySummary{Recipients: 1, Sent: 1},
			want:    ":white_check_mark: Newsletter sent to 1 subscriber.\n*Title:* Connector triggers | &lt;v2&gt;",
		},
		{
			name:         "partial delivery with every failure kind",
			summary:      model.DeliverySummary{Recipients: 45, Sent: 40, Rejected: 2, Uncertain: 1, Defect: 1},
			publishedURL: "ftp://blog.example.com/p",
			want:         ":warning: Newsletter sent to 40 of 45 subscribers.\n*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered:* 2 rejected · 1 unconfirmed · 1 failed · 1 not attempted\n_Unconfirmed emails may still have been delivered; check before resending._",
		},
		{
			name:    "nothing sent",
			summary: model.DeliverySummary{Recipients: 3, Rejected: 3},
			want:    ":x: The newsletter could not be sent to any subscriber (3 subscribers in the list).\n*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered:* 3 rejected",
		},
		{
			name:    "nothing confirmed, every send unconfirmed",
			summary: model.DeliverySummary{Recipients: 3, Uncertain: 3},
			want:    ":warning: Delivery could not be confirmed for any subscriber (3 subscribers in the list).\n*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered:* 3 unconfirmed\n_Unconfirmed emails may still have been delivered; check before resending._",
		},
		{
			name:    "nothing confirmed, unconfirmed and rejected",
			summary: model.DeliverySummary{Recipients: 1, Rejected: 1, Uncertain: 1},
			want:    ":warning: Delivery could not be confirmed for any subscriber (2 subscribers in the list).\n*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered:* 1 rejected · 1 unconfirmed\n_Unconfirmed emails may still have been delivered; check before resending._",
		},
		{
			name:    "nothing sent, failed and not attempted",
			summary: model.DeliverySummary{Recipients: 4, Rejected: 1, Defect: 2},
			want:    ":x: The newsletter could not be sent to any subscriber (4 subscribers in the list).\n*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered:* 1 rejected · 2 failed · 1 not attempted",
		},
		{
			name:    "nothing attempted",
			summary: model.DeliverySummary{},
			want:    ":x: No newsletter emails were sent.\n*Title:* Connector triggers | &lt;v2&gt;",
		},
		{
			name:    "negative counts treated as zero",
			summary: model.DeliverySummary{Recipients: -5, Sent: 2, Rejected: -1, Uncertain: -1, Defect: -1},
			want:    ":white_check_mark: Newsletter sent to 2 subscribers.\n*Title:* Connector triggers | &lt;v2&gt;",
		},
		{
			name:    "counts larger than recipients",
			summary: model.DeliverySummary{Recipients: 1, Sent: 2, Defect: 1},
			want:    ":warning: Newsletter sent to 2 of 3 subscribers.\n*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered:* 1 failed",
		},
		{
			name:         "published URL with credentials is not linked",
			summary:      model.DeliverySummary{Recipients: 2, Sent: 2},
			publishedURL: "https://user:secret@blog.example.com/p",
			artifactPath: "https://artifacts.example.com/p.html",
			want:         ":white_check_mark: Newsletter sent to 2 subscribers.\n*Title:* Connector triggers | &lt;v2&gt;\n*Blog artifact:* <https://artifacts.example.com/p.html|Open the blog HTML>",
		},
		{
			name:         "all sent with subscribers over the recipient limit and invalid entries",
			summary:      model.DeliverySummary{Recipients: 500, Sent: 500, SkippedOverLimit: 12, InvalidAddresses: 3},
			artifactPath: "artifacts/p.html",
			want: ":white_check_mark: Newsletter sent to 500 subscribers.\n*Title:* Connector triggers | &lt;v2&gt;\n" +
				"12 subscribers were not sent because of the recipient limit (`newsletter.maxRecipients`).\n" +
				"3 stored subscriber addresses were skipped because they are not valid email addresses.\n*Blog artifact:* `artifacts/p.html`",
		},
		{
			name:    "one subscriber over the limit and one invalid entry",
			summary: model.DeliverySummary{Recipients: 2, Sent: 2, SkippedOverLimit: 1, InvalidAddresses: 1},
			want: ":white_check_mark: Newsletter sent to 2 subscribers.\n*Title:* Connector triggers | &lt;v2&gt;\n" +
				"1 subscriber was not sent because of the recipient limit (`newsletter.maxRecipients`).\n" +
				"1 stored subscriber address was skipped because it is not a valid email address.",
		},
		{
			name:    "partial delivery keeps its header and adds the recipient limit",
			summary: model.DeliverySummary{Recipients: 3, Sent: 2, Uncertain: 1, SkippedOverLimit: 4},
			want: ":warning: Newsletter sent to 2 of 3 subscribers.\n*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered:* 1 unconfirmed\n" +
				"_Unconfirmed emails may still have been delivered; check before resending._\n" +
				"4 subscribers were not sent because of the recipient limit (`newsletter.maxRecipients`).",
		},
		{
			name:    "invalid entries only, nothing attempted",
			summary: model.DeliverySummary{InvalidAddresses: 7},
			want: ":x: No newsletter emails were sent.\n*Title:* Connector triggers | &lt;v2&gt;\n" +
				"7 stored subscriber addresses were skipped because they are not valid email addresses.",
		},
		{
			name:    "negative skipped and invalid counts are omitted",
			summary: model.DeliverySummary{Recipients: 1, Sent: 1, SkippedOverLimit: -2, InvalidAddresses: -1},
			want:    ":white_check_mark: Newsletter sent to 1 subscriber.\n*Title:* Connector triggers | &lt;v2&gt;",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DeliveryReport(post, test.summary, test.publishedURL, test.artifactPath)
			if got != test.want {
				t.Errorf("got\n%s\nwant\n%s", got, test.want)
			}
		})
	}
	untitled := DeliveryReport(model.BlogPost{}, model.DeliverySummary{Recipients: 1, Sent: 1}, "https://blog.example.com/p", "")
	if want := ":white_check_mark: Newsletter sent to 1 subscriber.\n*Published post:* <https://blog.example.com/p|" + publishedPostDefaultLabel + ">"; untitled != want {
		t.Errorf("untitled published post: got %q, want %q", untitled, want)
	}
	oversizedTitle := DeliveryReport(model.BlogPost{Title: strings.Repeat("x", 5000)}, model.DeliverySummary{Recipients: 1, Sent: 1}, "https://blog.example.com/p", "")
	if want := ":white_check_mark: Newsletter sent to 1 subscriber.\n*Published post:* <https://blog.example.com/p|" + publishedPostDefaultLabel + ">"; oversizedTitle != want {
		t.Errorf("oversized one-token title: got %q, want %q", oversizedTitle, want)
	}
	saturated := DeliveryReport(post, model.DeliverySummary{Sent: math.MaxInt, Defect: math.MaxInt}, "", "")
	if !strings.HasPrefix(saturated, ":warning: Newsletter sent to ") {
		t.Errorf("saturated counts: got %q", saturated)
	}
}

func TestDeliveryStopped(t *testing.T) {
	post := model.BlogPost{Title: "Connector triggers | <v2>"}
	reason := "Abandoned by an operator."
	doNotRepost := "Do not post this request again: a new request would email every subscriber again, including those already sent to."
	tests := []struct {
		name    string
		summary model.DeliverySummary
		want    string
	}{
		{
			name:    "stopped part-way",
			summary: model.DeliverySummary{Recipients: 10, Sent: 4, Uncertain: 1},
			want: ":octagonal_sign: *Newsletter delivery stopped* after sending to 4 of 10 subscribers.\n" +
				"*Title:* Connector triggers | &lt;v2&gt;\n*Details:*\n> " + reason + "\n*Not delivered:* 1 unconfirmed · 5 not attempted\n" +
				"_Unconfirmed emails may still have been delivered; check before resending._\n" + doNotRepost,
		},
		{
			name:    "stopped after only a rejected send",
			summary: model.DeliverySummary{Recipients: 2, Rejected: 1},
			want: ":octagonal_sign: *Newsletter delivery stopped* after sending to 0 of 2 subscribers.\n" +
				"*Title:* Connector triggers | &lt;v2&gt;\n*Details:*\n> " + reason + "\n*Not delivered:* 1 rejected · 1 not attempted\n" + doNotRepost,
		},
		{
			name:    "stopped before the first send",
			summary: model.DeliverySummary{Recipients: 3},
			want: ":octagonal_sign: *Newsletter delivery stopped* before any email was sent.\n" +
				"*Title:* Connector triggers | &lt;v2&gt;\n*Details:*\n> " + reason + "\n*Not delivered:* 3 not attempted\n" +
				"Post a new message in this channel to try again.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DeliveryStopped(post, test.summary, reason, "", "")
			if got != test.want {
				t.Errorf("got\n%s\nwant\n%s", got, test.want)
			}
			assertSlackSafe(t, test.name, got)
		})
	}
}

func TestDeliveryHeldForAttention(t *testing.T) {
	post := model.BlogPost{Title: "Connector triggers | <v2>"}
	linkedRun := RunReference{FlowID: "techblog/Ev01", DexWebURL: "https://dex.example.com/"}
	nextStep := "*Next step:* Fix the cause in the details, then retry the run in Dex Web. Sending resumes with the first subscriber not yet attempted."
	reason := "Gmail could not send with the newsletter-sender connection (authentication)."
	tests := []struct {
		name         string
		summary      model.DeliverySummary
		reason       string
		runReference RunReference
		want         string
	}{
		{
			name:         "paused part-way with a linked run",
			summary:      model.DeliverySummary{Recipients: 500, Sent: 120, SkippedOverLimit: 9, InvalidAddresses: 2},
			reason:       reason,
			runReference: linkedRun,
			want: ":warning: *Newsletter sending paused* after sending to 120 of 500 subscribers.\n" +
				"*Title:* Connector triggers | &lt;v2&gt;\n*Details:*\n> " + reason + "\n*Not delivered so far:* 380 not yet attempted\n" +
				nextStep + "\n*Run:* <https://dex.example.com/v2/runs/techblog%2FEv01|Open the run in Dex Web>",
		},
		{
			name:         "paused before the first send",
			summary:      model.DeliverySummary{Recipients: 1},
			runReference: RunReference{FlowID: "flow-1"},
			want: ":warning: *Newsletter sending paused* after sending to 0 of 1 subscriber.\n" +
				"*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered so far:* 1 not yet attempted\n" + nextStep + "\n*Run:* Flow `flow-1`",
		},
		{
			name:    "paused after rejected and unconfirmed sends",
			summary: model.DeliverySummary{Recipients: 10, Sent: 5, Rejected: 1, Uncertain: 2, Defect: 1},
			reason:  " \n ",
			want: ":warning: *Newsletter sending paused* after sending to 5 of 10 subscribers.\n" +
				"*Title:* Connector triggers | &lt;v2&gt;\n*Not delivered so far:* 1 rejected · 2 unconfirmed · 1 failed · 1 not yet attempted\n" +
				"_Unconfirmed emails may still have been delivered; check before resending._\n" + nextStep,
		},
		{
			name:    "no counts recorded",
			summary: model.DeliverySummary{Recipients: -3, Sent: -1},
			want:    ":warning: *Newsletter sending paused*.\n*Title:* Connector triggers | &lt;v2&gt;\n" + nextStep,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DeliveryHeldForAttention(post, test.summary, test.reason, test.runReference)
			if got != test.want {
				t.Errorf("got\n%s\nwant\n%s", got, test.want)
			}
			assertSlackSafe(t, test.name, got)
		})
	}
	saturated := DeliveryHeldForAttention(post, model.DeliverySummary{Sent: math.MaxInt, Defect: math.MaxInt}, "", RunReference{})
	if !strings.HasPrefix(saturated, ":warning: *Newsletter sending paused* after sending to ") {
		t.Errorf("saturated counts: got %q", saturated)
	}
}

func TestPartialResearchCoverage(t *testing.T) {
	tests := []struct {
		name         string
		repositories []model.RepositoryReference
		want         string
	}{
		{name: "nil", repositories: nil, want: ""},
		{name: "empty", repositories: []model.RepositoryReference{}, want: ""},
		{
			name:         "one repository",
			repositories: []model.RepositoryReference{{Owner: "superdurable", Name: "dex"}},
			want:         ":warning: Research did not complete for 1 repository: `superdurable/dex`. Changes there may be missing.",
		},
		{
			name: "duplicates named once, blanks skipped",
			repositories: []model.RepositoryReference{
				{Owner: "superdurable", Name: "dex"}, {Owner: " ", Name: ""}, {Owner: "SuperDurable", Name: "DEX"},
				{Owner: "superdurable", Name: "dex-skills"},
			},
			want: ":warning: Research did not complete for 2 repositories: `superdurable/dex`, `superdurable/dex-skills`. Changes there may be missing.",
		},
		{
			name:         "more repositories than named",
			repositories: adversarialRepositoriesNumbered(8),
			want:         ":warning: Research did not complete for 8 repositories: `o/r0`, `o/r1`, `o/r2`, `o/r3`, `o/r4` (+3 more). Changes there may be missing.",
		},
		{
			name:         "only blank repositories",
			repositories: []model.RepositoryReference{{}, {Owner: " "}},
			want:         ":warning: Research did not complete for every repository, so changes may be missing.",
		},
		{
			name:         "Slack syntax in names is escaped",
			repositories: []model.RepositoryReference{{Owner: "<!channel>", Name: "a`b @here"}},
			want:         ":warning: Research did not complete for 1 repository: `&lt;!channel&gt;/a'b @" + testZeroWidthSpace + "here`. Changes there may be missing.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := PartialResearchCoverage(test.repositories)
			if got != test.want {
				t.Errorf("got\n%s\nwant\n%s", got, test.want)
			}
			if strings.Contains(got, "\n") {
				t.Errorf("coverage note spans several lines: %q", got)
			}
			assertSlackSafe(t, test.name, got)
		})
	}
}

func TestPartialResearchCoverageFitsAfterTheLongestNotices(t *testing.T) {
	// Distinct repositories whose names escape to the longest possible code
	// spans, so dedupe cannot shorten the line.
	var repositories []model.RepositoryReference
	for index := 0; index < 50; index++ {
		prefix := strconv.Itoa(index)
		repositories = append(repositories, model.RepositoryReference{Owner: prefix + strings.Repeat("&", 300), Name: prefix + strings.Repeat("&", 300)})
	}
	coverage := PartialResearchCoverage(repositories)
	if !strings.Contains(coverage, "(+45 more)") {
		t.Fatalf("worst case did not name the maximum number of repositories: %q", coverage)
	}
	expandingText := strings.Repeat("&@ ", 20000)
	longDexWebURL := "https://dex.example.com/" + strings.Repeat("d", 560)
	longArtifactURL := "https://artifacts.example.com/" + strings.Repeat("a", 560)
	longestDraft := DraftReadyForReview(adversarialPost(expandingText, 50), RunReference{FlowID: "flow", DexWebURL: longDexWebURL}, longArtifactURL, math.MaxInt)
	for name, message := range map[string]string{
		"DraftReadyForReview": longestDraft,
		"NoNotableChanges":    NoNotableChanges(expandingText, expandingText),
	} {
		combined := message + "\n" + coverage
		assertSlackSafe(t, name+" with coverage", combined)
		if runes := utf8.RuneCountInString(combined); runes > maximumMessageRunes {
			t.Errorf("%s with coverage has %d runes, want at most %d", name, runes, maximumMessageRunes)
		} else {
			t.Logf("%s with coverage: %d of %d runes", name, runes, maximumMessageRunes)
		}
	}
}

func adversarialRepositoriesNumbered(count int) []model.RepositoryReference {
	repositories := make([]model.RepositoryReference, 0, count)
	for index := 0; index < count; index++ {
		repositories = append(repositories, model.RepositoryReference{Owner: "o", Name: "r" + strconv.Itoa(index)})
	}
	return repositories
}

func TestRunReferenceMarkup(t *testing.T) {
	longFlowID := strings.Repeat("f", maximumLinkedFlowIDBytes+1)
	tests := []struct {
		name         string
		runReference RunReference
		want         string
		wantLink     bool
	}{
		{name: "base URL with trailing slashes", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://dex.example.com///"}, want: "<https://dex.example.com/v2/runs/flow-1|L>", wantLink: true},
		{name: "base URL with path and surrounding space", runReference: RunReference{FlowID: "flow-1", DexWebURL: " http://127.0.0.1:8802/dex/ "}, want: "<http://127.0.0.1:8802/dex/v2/runs/flow-1|L>", wantLink: true},
		{name: "Flow ID path-escaped", runReference: RunReference{FlowID: "a/b c|d>e<f?g#h", DexWebURL: "https://dex.example.com"}, want: "<https://dex.example.com/v2/runs/a%2Fb%20c%7Cd%3Ee%3Cf%3Fg%23h|L>", wantLink: true},
		{name: "ampersand in Flow ID escaped for Slack", runReference: RunReference{FlowID: "a&b", DexWebURL: "https://dex.example.com"}, want: "<https://dex.example.com/v2/runs/a&amp;b|L>", wantLink: true},
		{name: "mention in Flow ID path-escaped", runReference: RunReference{FlowID: "<!channel>", DexWebURL: "https://dex.example.com"}, want: "<https://dex.example.com/v2/runs/%3C%21channel%3E|L>", wantLink: true},
		{name: "empty Dex Web URL", runReference: RunReference{FlowID: "flow-1"}, want: "Flow `flow-1`"},
		{name: "blank Flow ID", runReference: RunReference{FlowID: "  ", DexWebURL: "https://dex.example.com"}, want: ""},
		{name: "non-http scheme", runReference: RunReference{FlowID: "flow-1", DexWebURL: "javascript:alert(1)"}, want: "Flow `flow-1`"},
		{name: "ftp scheme", runReference: RunReference{FlowID: "flow-1", DexWebURL: "ftp://dex.example.com"}, want: "Flow `flow-1`"},
		{name: "relative URL", runReference: RunReference{FlowID: "flow-1", DexWebURL: "/dex"}, want: "Flow `flow-1`"},
		{name: "credentials in base URL", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://admin:hunter2@dex.example.com"}, want: "Flow `flow-1`"},
		{name: "query in base URL", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://dex.example.com/?tab=1"}, want: "Flow `flow-1`"},
		{name: "fragment in base URL", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://dex.example.com/#x"}, want: "Flow `flow-1`"},
		{name: "Slack syntax in base URL", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://dex.example.com/|<!channel>"}, want: "Flow `flow-1`"},
		{name: "space in base URL", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://dex example.com"}, want: "Flow `flow-1`"},
		{name: "secret in base URL", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://dex.example.com/xoxb-1234567890-abcdefghij"}, want: "Flow `flow-1`"},
		{name: "base URL too long to link", runReference: RunReference{FlowID: "flow-1", DexWebURL: "https://dex.example.com/" + strings.Repeat("d", maximumLinkTargetRunes)}, want: "Flow `flow-1`"},
		{name: "Flow ID too long to link", runReference: RunReference{FlowID: longFlowID, DexWebURL: "https://dex.example.com"}, want: "Flow `" + strings.Repeat("f", maximumFlowIDRunes-3) + ellipsis + "`"},
		{name: "backticks and mentions in fallback Flow ID", runReference: RunReference{FlowID: "a`b <@U1>"}, want: "Flow `a'b &lt;@" + testZeroWidthSpace + "U1&gt;`"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, isLink := runReferenceMarkup(test.runReference, "L")
			if got != test.want || isLink != test.wantLink {
				t.Errorf("got (%q, %t), want (%q, %t)", got, isLink, test.want, test.wantLink)
			}
		})
	}
}

func TestSingleLineFieldsCannotAddLines(t *testing.T) {
	injected := "Real title\n*Not delivered:* none\n> <!channel>" + testLineSeparator + "more" + testNextLine + "more\rmore"
	post := model.BlogPost{Title: injected, Summary: injected, Tags: []string{injected}, Sections: []model.BlogSection{{Heading: injected}}}
	plainPost := model.BlogPost{Title: "t", Summary: "s", Tags: []string{"g"}, Sections: []model.BlogSection{{Heading: "h"}}}
	runReference := RunReference{FlowID: injected}
	tests := []struct {
		name     string
		injected string
		plain    string
	}{
		{name: "ResearchStarted", injected: ResearchStarted(injected, injected, []model.RepositoryReference{{Owner: injected, Name: injected}}), plain: ResearchStarted("t", "w", []model.RepositoryReference{{Owner: "o", Name: "n"}})},
		{name: "NoNotableChanges", injected: NoNotableChanges(injected, injected), plain: NoNotableChanges("t", "w")},
		{name: "DraftReadyForReview", injected: DraftReadyForReview(post, runReference, injected, 1), plain: DraftReadyForReview(plainPost, RunReference{FlowID: "f"}, "p", 1)},
		{name: "ReviewReminder", injected: ReviewReminder(post, runReference, 1), plain: ReviewReminder(plainPost, RunReference{FlowID: "f"}, 1)},
		{name: "NeedsAttention stage", injected: NeedsAttention(injected, "r", runReference), plain: NeedsAttention("s", "r", RunReference{FlowID: "f"})},
		{name: "DeliveryReport", injected: DeliveryReport(post, model.DeliverySummary{Recipients: 1, Sent: 1}, injected, injected), plain: DeliveryReport(plainPost, model.DeliverySummary{Recipients: 1, Sent: 1}, "", "p")},
		{name: "DeliveryHeldForAttention", injected: DeliveryHeldForAttention(post, model.DeliverySummary{Recipients: 2, Sent: 1}, "r", runReference), plain: DeliveryHeldForAttention(plainPost, model.DeliverySummary{Recipients: 2, Sent: 1}, "r", RunReference{FlowID: "f"})},
		{name: "PartialResearchCoverage", injected: PartialResearchCoverage([]model.RepositoryReference{{Owner: injected, Name: injected}, {Owner: "o", Name: injected}}), plain: PartialResearchCoverage([]model.RepositoryReference{{Owner: "o", Name: "n"}, {Owner: "o", Name: "m"}})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, want := strings.Count(test.injected, "\n"), strings.Count(test.plain, "\n"); got != want {
				t.Errorf("injected text changed the line count to %d, want %d:\n%s", got, want, test.injected)
			}
		})
	}
}

func TestQuotedFieldsKeepEveryLineQuoted(t *testing.T) {
	injected := "first\n*Sent:* 999\n:white_check_mark: done\n\n\n\nlast"
	tests := []struct {
		name    string
		message string
		header  string
		footer  string
	}{
		{name: "ClarificationNeeded", message: ClarificationNeeded(injected), header: ":thinking_face: I need a little more detail before I can research this.", footer: "Post a new message in this channel with the details and I'll start again."},
		{name: "DraftDiscarded", message: DraftDiscarded(model.BlogPost{Title: "t"}, injected), header: "*Reason:*"},
		{name: "RevisionStarted", message: RevisionStarted(1, injected), header: "*Feedback:*", footer: "I'll post the revised draft in this thread when it's ready."},
		{name: "NeedsAttention", message: NeedsAttention("s", injected, RunReference{}), header: "*Details:*"},
		{name: "DeliveryHeldForAttention", message: DeliveryHeldForAttention(model.BlogPost{Title: "t"}, model.DeliverySummary{}, injected, RunReference{}), header: "*Details:*"},
		{name: "RequestFailed", message: RequestFailed(injected), header: ":x: Sorry, I couldn't finish this request.", footer: "Post a new message in this channel to try again."},
	}
	wantQuote := "> first\n> *Sent:* 999\n> :white_check_mark: done\n>\n> last"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := test.header + "\n" + wantQuote
			if test.footer != "" {
				want += "\n" + test.footer
			}
			if !strings.Contains(test.message, want) {
				t.Errorf("message does not contain the fully quoted block:\n%s", test.message)
			}
		})
	}
}

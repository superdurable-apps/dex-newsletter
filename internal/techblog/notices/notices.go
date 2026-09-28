// Package notices builds the Slack thread replies, in Slack mrkdwn, that the
// tech blog newsletter Process posts while it handles one request.
//
// Every function is pure and deterministic: identical arguments always produce
// a byte-identical message, so a retried Dex Step posts the same text.
//
// Every dynamic value (Slack text, language-model output, GitHub content,
// error reasons, paths, and IDs) is treated as untrusted. Before it reaches a
// message it is cut to a bounded size, stripped of control and format
// characters (including zero-width characters and bidirectional overrides),
// scrubbed of email addresses and secret-like tokens, collapsed onto one line
// unless it is shown as a quoted block, truncated with an ellipsis, and
// escaped for Slack (& as &amp;, < as &lt;, > as &gt;). Every '@' is followed
// by a zero-width space, so no value can mention a user, a user group, or
// @channel, @here, or @everyone, and because '<' is always escaped no value
// can form <!channel>, <@U123>, <#C123>, or a disguised link. Slack links
// (<url|label>) are emitted only for Dex Web run URLs this package builds and
// for URLs validated as absolute http(s) URLs without credentials. Every
// message is at most 3,500 characters (Unicode code points).
package notices

import (
	"math"
	"strconv"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Bounds, in runes after escaping, for each kind of dynamic value.
const (
	maximumTitleRunes             = 200
	maximumSummaryRunes           = 500
	maximumTopicRunes             = 200
	maximumWindowDescriptionRunes = 120
	maximumQuestionRunes          = 1200
	maximumFeedbackRunes          = 1500
	maximumReasonRunes            = 1200
	maximumStageRunes             = 80
	maximumFlowIDRunes            = 200
	maximumArtifactPathRunes      = 300
	maximumRepositoryNameRunes    = 120
	maximumListedRepositories     = 10
	maximumSectionHeadingRunes    = 60
	maximumListedSections         = 6
	maximumTagRunes               = 30
	maximumListedTags             = 6

	// maximumCoverageRepositories bounds the repositories named by
	// PartialResearchCoverage, whose line is appended to other notices.
	maximumCoverageRepositories = 5
)

// Static wording shared by several notices.
const (
	untitledDraftText            = "(untitled draft)"
	defaultClarificationQuestion = "Which topic or part of the codebase should the post cover, and over what time range?"
	reviewActionsText            = "to approve it, request changes, or discard it."
	reviewLinkLabel              = "Open the review in Dex Web"
	runLinkLabel                 = "Open the run in Dex Web"
	previewLinkLabel             = "Open the HTML preview"
	blogArtifactLinkLabel        = "Open the blog HTML"
	publishedPostDefaultLabel    = "Read the published post"
	listItemSeparator            = ", "
	middleDotSeparator           = " · "
)

// RunReference identifies the Dex Flow run a notice points its reader to.
type RunReference struct {
	// FlowID is the Dex Flow ID of the run.
	FlowID string
	// DexWebURL is the Dex Web base URL, such as "https://dex.example.com".
	// When it is set and is an absolute http(s) URL without credentials,
	// query, or fragment, notices link to
	// strings.TrimRight(DexWebURL, "/") + "/v2/runs/" + url.PathEscape(FlowID).
	// Otherwise notices show the Flow ID as code.
	DexWebURL string
}

// RequestAcknowledged returns the first reply to a new request.
func RequestAcknowledged() string {
	return ":mag: On it — researching recent changes for this request. I'll post updates in this thread."
}

// ClarificationNeeded returns the reply asking the requester for the detail
// the request is missing. question is the language model's clarification
// question; a generic question is used when it is blank or nothing of it can
// be shown (a single token too long to cut safely).
func ClarificationNeeded(question string) string {
	var message slackMessage
	message.appendLine(":thinking_face: I need a little more detail before I can research this.")
	quotedQuestion := quotedText(question, maximumQuestionRunes)
	if showsNoContent(quotedQuestion) {
		quotedQuestion = "> " + defaultClarificationQuestion
	}
	message.appendLine(quotedQuestion)
	message.appendLine("Post a new message in this channel with the details and I'll start again.")
	return message.render()
}

// ResearchStarted returns the reply announcing research on topic over the
// change window described by windowDescription in the given repositories.
// At most ten repositories are listed; the rest are counted.
func ResearchStarted(topic string, windowDescription string, repositories []model.RepositoryReference) string {
	var message slackMessage
	message.appendLine(":hourglass_flowing_sand: Researching recent changes.")
	message.appendField("Topic", singleLineText(topic, maximumTopicRunes))
	message.appendField("Window", singleLineText(windowDescription, maximumWindowDescriptionRunes))
	repositoryList, repositoryCount := repositoryListText(repositories, maximumListedRepositories)
	message.appendField("Repositories ("+strconv.Itoa(repositoryCount)+")", repositoryList)
	return message.render()
}

// PartialResearchCoverage returns one line naming the repositories whose
// research failed or was incomplete, or "" when repositories is empty. The
// Flow appends it, after a line break, to NoNotableChanges and
// DraftReadyForReview, and the combined message stays within the Slack bound
// for any input. Duplicates are named once, at most five repositories are
// named, and the rest are counted.
func PartialResearchCoverage(repositories []model.RepositoryReference) string {
	if len(repositories) == 0 {
		return ""
	}
	repositoryList, repositoryCount := repositoryListText(uniqueRepositories(repositories), maximumCoverageRepositories)
	if repositoryCount == 0 {
		return ":warning: Research did not complete for every repository, so changes may be missing."
	}
	return ":warning: Research did not complete for " + countedNoun(repositoryCount, "repository", "repositories") + ": " +
		repositoryList + ". Changes there may be missing."
}

// NoNotableChanges returns the reply explaining that research found nothing
// worth a post, so no draft was written.
func NoNotableChanges(topic string, windowDescription string) string {
	var message slackMessage
	message.appendLine(":zzz: I didn't find notable changes to write about, so I didn't draft a blog post.")
	message.appendField("Topic", singleLineText(topic, maximumTopicRunes))
	message.appendField("Window", singleLineText(windowDescription, maximumWindowDescriptionRunes))
	message.appendLine("Post a new message with a longer time range or a different topic to try again.")
	return message.render()
}

// DraftReadyForReview returns the reply announcing a draft that waits for the
// editor in Dex Web. revisionNumber is zero for the first draft and counts the
// editor-requested revisions after it. artifactPath is the HTML artifact
// location: a validated http(s) URL is linked, anything else is shown as code.
func DraftReadyForReview(post model.BlogPost, runReference RunReference, artifactPath string, revisionNumber int) string {
	var message slackMessage
	heading := ":memo: *Draft ready for review*"
	if revisionNumber > 0 {
		heading += " (revision " + strconv.Itoa(revisionNumber) + ")"
	}
	message.appendLine(heading)
	message.appendField("Title", postTitleText(post))
	message.appendField("Summary", singleLineText(post.Summary, maximumSummaryRunes))
	message.appendField("Sections", sectionHeadingListText(post.Sections))
	message.appendField("Tags", boundedListText(post.Tags, maximumListedTags, maximumTagRunes, listItemSeparator))
	message.appendField("Preview", artifactReferenceText(artifactPath, previewLinkLabel))
	message.appendField("Review", reviewInstructionText(runReference))
	return message.render()
}

// ReviewReminder returns the reminder that a draft still waits for review.
// reminderNumber counts reminders from one; it is omitted when not positive.
func ReviewReminder(post model.BlogPost, runReference RunReference, reminderNumber int) string {
	var message slackMessage
	heading := ":bell: Reminder"
	if reminderNumber > 0 {
		heading += " " + strconv.Itoa(reminderNumber)
	}
	message.appendLine(heading + ": this draft is still waiting for editor review.")
	message.appendField("Title", postTitleText(post))
	message.appendField("Review", reviewInstructionText(runReference))
	return message.render()
}

// ReviewExpired returns the reply explaining that the review window closed
// without a decision, so nothing was sent.
func ReviewExpired(post model.BlogPost) string {
	var message slackMessage
	message.appendLine(":hourglass: The review window closed without a decision, so the newsletter was not sent.")
	message.appendField("Title", postTitleText(post))
	message.appendLine("Post a new request in this channel if you still want to publish this post.")
	return message.render()
}

// DraftDiscarded returns the reply explaining that the editor discarded the
// draft. reason is the editor's explanation and is omitted when blank.
func DraftDiscarded(post model.BlogPost, reason string) string {
	var message slackMessage
	message.appendLine(":wastebasket: The draft was discarded, so the newsletter was not sent.")
	message.appendField("Title", postTitleText(post))
	message.appendQuotedField("Reason", quotedText(reason, maximumReasonRunes))
	return message.render()
}

// RevisionStarted returns the reply announcing that the draft is being revised
// from editor feedback. revisionNumber counts revisions from one and is omitted
// when not positive; feedback is omitted when blank.
func RevisionStarted(revisionNumber int, feedback string) string {
	var message slackMessage
	heading := ":pencil2: Revising the draft"
	if revisionNumber > 0 {
		heading += " (revision " + strconv.Itoa(revisionNumber) + ")"
	}
	message.appendLine(heading + " based on editor feedback.")
	message.appendQuotedField("Feedback", quotedText(feedback, maximumFeedbackRunes))
	message.appendLine("I'll post the revised draft in this thread when it's ready.")
	return message.render()
}

// NeedsAttention returns the reply reporting that the run stopped at stage and
// needs an operator. reason is shown as a quoted block and omitted when blank.
func NeedsAttention(stage string, reason string, runReference RunReference) string {
	var message slackMessage
	message.appendLine(":warning: *This request needs attention.*")
	message.appendField("Stage", singleLineText(stage, maximumStageRunes))
	message.appendQuotedField("Details", quotedText(reason, maximumReasonRunes))
	runMarkup, _ := runReferenceMarkup(runReference, runLinkLabel)
	message.appendField("Run", runMarkup)
	return message.render()
}

// NoSubscribers returns the reply explaining that the approved newsletter was
// not sent because the subscriber list had no deliverable address.
func NoSubscribers(post model.BlogPost) string {
	var message slackMessage
	message.appendLine(":mailbox_with_no_mail: The newsletter was not sent because the subscriber list has no deliverable addresses.")
	message.appendField("Title", postTitleText(post))
	message.appendLine("Readers can subscribe on the application's home page. Once someone has subscribed, post a new request to try again.")
	return message.render()
}

// DeliveryReport returns the final reply summarizing newsletter delivery. It
// reports only counts, never recipient addresses. Negative counts are treated
// as zero. When nothing was confirmed sent but some sends are unconfirmed, the
// header says delivery could not be confirmed rather than that it failed.
// Subscribers left out by the recipient limit (SkippedOverLimit) and unusable
// stored addresses (InvalidAddresses) are reported on their own lines when
// non-zero; they never change the header, which counts only the subscribers
// delivery was meant to reach. publishedURL is linked, with the post title as
// its label, when it is a validated http(s) URL and omitted otherwise;
// artifactPath is shown as in DraftReadyForReview.
func DeliveryReport(post model.BlogPost, summary model.DeliverySummary, publishedURL string, artifactPath string) string {
	counts := deliveryCountsOf(summary)
	sent, attempted := counts.sent, counts.total

	var message slackMessage
	switch {
	case attempted == 0:
		message.appendLine(":x: No newsletter emails were sent.")
	case sent == 0 && counts.uncertain > 0:
		// Unconfirmed sends may have been delivered, so a definite failure
		// header would invite a resend that duplicates them.
		message.appendLine(":warning: Delivery could not be confirmed for any subscriber (" + countedNoun(attempted, "subscriber", "subscribers") + " in the list).")
	case sent == 0:
		message.appendLine(":x: The newsletter could not be sent to any subscriber (" + countedNoun(attempted, "subscriber", "subscribers") + " in the list).")
	case counts.undelivered == 0 && counts.notAttempted == 0:
		message.appendLine(":white_check_mark: Newsletter sent to " + countedNoun(sent, "subscriber", "subscribers") + ".")
	default:
		message.appendLine(":warning: Newsletter sent to " + strconv.Itoa(sent) + " of " + countedNoun(attempted, "subscriber", "subscribers") + ".")
	}

	if publishedLink := slackLink(publishedURL, publishedPostLabel(post)); publishedLink != "" {
		message.appendField("Published post", publishedLink)
	} else {
		message.appendField("Title", postTitleText(post))
	}

	message.appendField("Not delivered", counts.undeliveredText("not attempted"))
	if counts.uncertain > 0 {
		message.appendLine(unconfirmedDeliveryText)
	}
	if skipped := nonNegative(summary.SkippedOverLimit); skipped > 0 {
		message.appendLine(countedNoun(skipped, "subscriber was", "subscribers were") + " not sent because of the recipient limit (`newsletter.maxRecipients`).")
	}
	if invalid := nonNegative(summary.InvalidAddresses); invalid > 0 {
		message.appendLine(countedNoun(invalid, "stored subscriber address was", "stored subscriber addresses were") +
			" skipped because " + pluralChoice(invalid, "it is not a valid email address.", "they are not valid email addresses."))
	}
	message.appendField("Blog artifact", artifactReferenceText(artifactPath, blogArtifactLinkLabel))
	return message.render()
}

// DeliveryHeldForAttention returns the reply reporting that newsletter
// delivery paused part-way, either because the Gmail connection itself failed
// (for example, revoked or expired credentials) or because Gmail kept failing
// after every retry (for example, at the daily sending limit). reason names
// the cause, so the header and next step state none. No further subscriber is
// sent to until an operator retries the run. summary
// holds the counts recorded before the pause; the subscriber whose send
// failed is not among them, and a retry resumes with it. It reports only
// counts, never recipient addresses, and treats negative counts as zero.
// reason is shown as a quoted block and omitted when blank.
func DeliveryHeldForAttention(post model.BlogPost, summary model.DeliverySummary, reason string, runReference RunReference) string {
	counts := deliveryCountsOf(summary)
	var message slackMessage
	if counts.total == 0 {
		message.appendLine(":warning: *Newsletter sending paused*.")
	} else {
		message.appendLine(":warning: *Newsletter sending paused* after sending to " + strconv.Itoa(counts.sent) + " of " +
			countedNoun(counts.total, "subscriber", "subscribers") + ".")
	}
	message.appendField("Title", postTitleText(post))
	message.appendQuotedField("Details", quotedText(reason, maximumReasonRunes))
	message.appendField("Not delivered so far", counts.undeliveredText("not yet attempted"))
	if counts.uncertain > 0 {
		message.appendLine(unconfirmedDeliveryText)
	}
	message.appendField("Next step", "Fix the cause in the details, then retry the run in Dex Web. Sending resumes with the first subscriber not yet attempted.")
	runMarkup, _ := runReferenceMarkup(runReference, runLinkLabel)
	message.appendField("Run", runMarkup)
	return message.render()
}

// unconfirmedDeliveryText warns against resending while sends are unconfirmed.
const unconfirmedDeliveryText = "_Unconfirmed emails may still have been delivered; check before resending._"

// deliveryCounts are the non-negative, overflow-safe counts of one
// model.DeliverySummary.
type deliveryCounts struct {
	sent, rejected, uncertain, defect int
	// undelivered is rejected + uncertain + defect.
	undelivered int
	// total is the subscriber count: Recipients, or every recorded outcome
	// when that is larger.
	total int
	// notAttempted is total minus every recorded outcome.
	notAttempted int
}

func deliveryCountsOf(summary model.DeliverySummary) deliveryCounts {
	counts := deliveryCounts{
		sent:      nonNegative(summary.Sent),
		rejected:  nonNegative(summary.Rejected),
		uncertain: nonNegative(summary.Uncertain),
		defect:    nonNegative(summary.Defect),
	}
	counts.undelivered = saturatingSum(counts.rejected, counts.uncertain, counts.defect)
	accounted := saturatingSum(counts.sent, counts.undelivered)
	counts.total = max(nonNegative(summary.Recipients), accounted)
	counts.notAttempted = counts.total - accounted
	return counts
}

// undeliveredText lists the non-zero undelivered counts, naming the
// subscribers not yet reached notAttemptedLabel, or "" when there are none.
func (counts deliveryCounts) undeliveredText(notAttemptedLabel string) string {
	var undelivered []string
	if counts.rejected > 0 {
		undelivered = append(undelivered, strconv.Itoa(counts.rejected)+" rejected")
	}
	if counts.uncertain > 0 {
		undelivered = append(undelivered, strconv.Itoa(counts.uncertain)+" unconfirmed")
	}
	if counts.defect > 0 {
		undelivered = append(undelivered, strconv.Itoa(counts.defect)+" failed")
	}
	if counts.notAttempted > 0 {
		undelivered = append(undelivered, strconv.Itoa(counts.notAttempted)+" "+notAttemptedLabel)
	}
	return strings.Join(undelivered, middleDotSeparator)
}

// DeliveryStopped returns the final reply when an operator abandons, or the
// attention wait expires on, a newsletter whose delivery had paused. Unlike
// RequestFailed it never invites a new request once any send was attempted:
// a new request would email every subscriber again, including those already
// reached. It reports only counts, never recipient addresses. reason is shown
// as a quoted block and omitted when blank; publishedURL and artifactPath are
// shown as in DeliveryReport.
func DeliveryStopped(post model.BlogPost, summary model.DeliverySummary, reason string, publishedURL string, artifactPath string) string {
	counts := deliveryCountsOf(summary)
	attempted := saturatingSum(counts.sent, counts.undelivered)
	var message slackMessage
	if attempted == 0 {
		message.appendLine(":octagonal_sign: *Newsletter delivery stopped* before any email was sent.")
	} else {
		message.appendLine(":octagonal_sign: *Newsletter delivery stopped* after sending to " + strconv.Itoa(counts.sent) + " of " +
			countedNoun(counts.total, "subscriber", "subscribers") + ".")
	}
	if publishedLink := slackLink(publishedURL, publishedPostLabel(post)); publishedLink != "" {
		message.appendField("Published post", publishedLink)
	} else {
		message.appendField("Title", postTitleText(post))
	}
	message.appendQuotedField("Details", quotedText(reason, maximumReasonRunes))
	message.appendField("Not delivered", counts.undeliveredText("not attempted"))
	if counts.uncertain > 0 {
		message.appendLine(unconfirmedDeliveryText)
	}
	if attempted == 0 {
		message.appendLine("Post a new message in this channel to try again.")
	} else {
		message.appendLine("Do not post this request again: a new request would email every subscriber again, including those already sent to.")
	}
	message.appendField("Blog artifact", artifactReferenceText(artifactPath, blogArtifactLinkLabel))
	return message.render()
}

// RequestFailed returns the reply reporting that the request ended without a
// post. reason is shown as a quoted block and omitted when blank.
func RequestFailed(reason string) string {
	var message slackMessage
	message.appendLine(":x: Sorry, I couldn't finish this request.")
	message.appendLine(quotedText(reason, maximumReasonRunes))
	message.appendLine("Post a new message in this channel to try again.")
	return message.render()
}

// postTitleText returns the escaped post title, or a placeholder when the
// title is blank or nothing of it can be shown.
func postTitleText(post model.BlogPost) string {
	if title := singleLineText(post.Title, maximumTitleRunes); !showsNoContent(title) {
		return title
	}
	return untitledDraftText
}

// publishedPostLabel returns the link label for the published post: its
// escaped title, or a generic label when the title is blank or nothing of it
// can be shown.
func publishedPostLabel(post model.BlogPost) string {
	if label := linkLabelText(post.Title, maximumTitleRunes); !showsNoContent(label) {
		return label
	}
	return publishedPostDefaultLabel
}

// reviewInstructionText tells the editor where to review the draft.
func reviewInstructionText(runReference RunReference) string {
	markup, isLink := runReferenceMarkup(runReference, reviewLinkLabel)
	switch {
	case isLink:
		return markup + " " + reviewActionsText
	case markup != "":
		return "Open " + markup + " in Dex Web " + reviewActionsText
	default:
		return "Open this run in Dex Web " + reviewActionsText
	}
}

// artifactReferenceText links artifactPath when it is a validated http(s) URL
// and otherwise shows it as code; it returns "" when artifactPath is blank.
func artifactReferenceText(artifactPath string, linkLabel string) string {
	if link := slackLink(artifactPath, linkLabel); link != "" {
		return link
	}
	return codeSpan(artifactPath, maximumArtifactPathRunes)
}

// repositoryListText lists up to maximumListed repositories as code spans and
// returns the number of non-blank repositories.
func repositoryListText(repositories []model.RepositoryReference, maximumListed int) (string, int) {
	var names []string
	count := 0
	for _, repository := range repositories {
		name := repositoryReferenceText(repository)
		if name == "" {
			continue
		}
		count++
		if len(names) < maximumListed {
			names = append(names, name)
		}
	}
	return withRemainderText(strings.Join(names, listItemSeparator), count-len(names)), count
}

// uniqueRepositories drops repositories that repeat an earlier owner and
// name, compared case-insensitively as GitHub does.
func uniqueRepositories(repositories []model.RepositoryReference) []model.RepositoryReference {
	unique := make([]model.RepositoryReference, 0, len(repositories))
	seen := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		key := strings.ToLower(strings.TrimSpace(repository.Owner) + "/" + strings.TrimSpace(repository.Name))
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, repository)
	}
	return unique
}

// repositoryReferenceText returns "owner/name" as a code span, or "" when both
// parts are blank.
func repositoryReferenceText(repository model.RepositoryReference) string {
	owner, ownerTruncated := normalizedSingleLine(repository.Owner, maximumRepositoryNameRunes)
	name, nameTruncated := normalizedSingleLine(repository.Name, maximumRepositoryNameRunes)
	fullName := owner
	switch {
	case owner != "" && name != "":
		fullName = owner + "/" + name
	case name != "":
		fullName = name
	}
	return codeSpanFromNormalizedText(fullName, ownerTruncated || nameTruncated, maximumRepositoryNameRunes)
}

// sectionHeadingListText lists up to maximumListedSections section headings.
func sectionHeadingListText(sections []model.BlogSection) string {
	headings := make([]string, 0, len(sections))
	for _, section := range sections {
		headings = append(headings, section.Heading)
	}
	return boundedListText(headings, maximumListedSections, maximumSectionHeadingRunes, middleDotSeparator)
}

// boundedListText escapes and joins up to maximumItems non-blank values of at
// most itemLimit runes each and counts the rest.
func boundedListText(values []string, maximumItems int, itemLimit int, separator string) string {
	var items []string
	count := 0
	for _, value := range values {
		item := singleLineText(value, itemLimit)
		if item == "" {
			continue
		}
		count++
		if len(items) < maximumItems {
			items = append(items, item)
		}
	}
	return withRemainderText(strings.Join(items, separator), count-len(items))
}

// withRemainderText appends " (+N more)" to a non-empty list when N items
// were left out.
func withRemainderText(list string, remainder int) string {
	if list == "" || remainder <= 0 {
		return list
	}
	return list + " (+" + strconv.Itoa(remainder) + " more)"
}

// countedNoun returns "1 singular" or "N plural".
func countedNoun(count int, singular string, plural string) string {
	return strconv.Itoa(count) + " " + pluralChoice(count, singular, plural)
}

// pluralChoice returns singular when count is 1 and plural otherwise.
func pluralChoice(count int, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

// nonNegative returns value, or zero when value is negative.
func nonNegative(value int) int {
	return max(value, 0)
}

// saturatingSum adds non-negative values, stopping at math.MaxInt instead of
// overflowing.
func saturatingSum(values ...int) int {
	sum := 0
	for _, value := range values {
		if value > math.MaxInt-sum {
			return math.MaxInt
		}
		sum += value
	}
	return sum
}

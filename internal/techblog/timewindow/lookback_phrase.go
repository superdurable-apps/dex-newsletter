package timewindow

import (
	"strings"
	"unicode"
)

// maximumLookbackQuantity saturates numerals in request text. It is large
// enough that an hour count still saturates to maximumRepresentableLookbackDays
// and small enough that no multiplication by a unit length can overflow an int.
const maximumLookbackQuantity = maximumRepresentableLookbackDays * 24

// lookbackPhraseToken is one lowercase word of request text.
type lookbackPhraseToken struct {
	word string
	// adjoinsPreviousToken reports whether only whitespace and ASCII hyphens
	// separate this word from the previous word, so the two can form one
	// phrase.
	adjoinsPreviousToken bool
	// compoundsWithPreviousToken reports whether this word is glued to the
	// previous word into one compound word, as "end" is in "week-end": the
	// separator has no whitespace, at most one hyphen, and otherwise only
	// invisible format characters such as a soft hyphen or zero-width space.
	compoundsWithPreviousToken bool
}

// lookbackTokenSeparator summarizes the characters between two words.
type lookbackTokenSeparator struct {
	// hasWhitespace reports at least one whitespace character.
	hasWhitespace bool
	// hyphenCount counts ASCII hyphens, U+2010 HYPHEN, and U+2011 NON-BREAKING
	// HYPHEN.
	hyphenCount int
	// hasNonTightCharacter reports a character other than whitespace and the
	// ASCII hyphen; such a character breaks a phrase.
	hasNonTightCharacter bool
	// hasVisiblePunctuation reports a character other than whitespace,
	// hyphens, and invisible format characters, such as an apostrophe, comma,
	// or period; such a character ends a word rather than joining a compound.
	hasVisiblePunctuation bool
}

// lookbackUnit is one recognized time unit word.
type lookbackUnit struct {
	// daysPerUnit is the whole-day length of one unit; zero for hours.
	daysPerUnit int
	// countsHours reports that the unit is hours, rounded up to whole days.
	countsHours bool
	// plural reports that the unit word is plural ("weeks" rather than "week").
	plural bool
}

// ParseLookbackPhrase is the deterministic fallback used when the language
// model does not report a time range. It finds the first English lookback
// phrase in text and returns its length in whole days.
//
// Recognized shapes, case-insensitively, are:
//
//   - "past", "last", or "previous" followed by a quantity and a unit:
//     "past 7 days", "last 3 weeks", "previous two months".
//   - "past", "last", or "previous" followed directly by a singular unit,
//     meaning one unit: "last week", "past month", "past fortnight". This also
//     covers "since last week". A quantity-less "last day" is not a lookback
//     because it names a final day ("Priya's last day"); "past day" is.
//   - "since" followed by a quantity, a unit, and "ago": "since two weeks ago".
//     A bare "two weeks ago" dates an event rather than naming the range to
//     cover, so it never matches and cannot override a later requested range.
//
// Quantities are ASCII numerals, the words one through twelve, "a" or "an"
// (1), and "couple", "a couple", or "a couple of" (2). Units are day (1 day),
// week (7), fortnight (14), month (30), year (365), and hour (rounded up to
// whole days), each singular or plural.
//
// Words must be whole words, and the words of a phrase may be separated only
// by whitespace or ASCII hyphens ("past-7-days", "the past 7-day window"). A
// phrase must not start or end inside a hyphenated compound word, so
// "outlast 3 days", "last weekend", "last week-end", "past day-to-day",
// "last year-end", "second-last week", and "last, 3 days" do not match. A
// spaced or doubled hyphen used as a dash ("last week - or so", "last
// week--mostly") is punctuation, not a compound. A zero quantity is not a
// lookback and scanning continues past it. Results saturate at the largest
// whole-day time.Duration (106751 days); callers clamp further with
// ResolveChangeWindow.
//
// No match returns (0, false); a match always returns days >= 1.
func ParseLookbackPhrase(text string) (days int, found bool) {
	tokens := tokenizeLookbackText(text)
	for index := range tokens {
		if matchedDays, matched := matchLookbackPhraseAt(tokens, index); matched {
			return matchedDays, true
		}
	}
	return 0, false
}

// tokenizeLookbackText splits lowercase text into words made of letters,
// digits, and combining marks, recording how each word is joined to the
// previous one.
func tokenizeLookbackText(text string) []lookbackPhraseToken {
	lowered := strings.ToLower(text)
	var tokens []lookbackPhraseToken
	wordStart := -1
	var separator lookbackTokenSeparator
	for index, character := range lowered {
		if isLookbackWordCharacter(character) {
			if wordStart < 0 {
				wordStart = index
			}
			continue
		}
		if wordStart >= 0 {
			tokens = appendLookbackPhraseToken(tokens, lowered[wordStart:index], separator)
			wordStart = -1
			separator = lookbackTokenSeparator{}
		}
		separator.include(character)
	}
	if wordStart >= 0 {
		tokens = appendLookbackPhraseToken(tokens, lowered[wordStart:], separator)
	}
	return tokens
}

// appendLookbackPhraseToken appends word, which follows separator, to tokens.
func appendLookbackPhraseToken(tokens []lookbackPhraseToken, word string, separator lookbackTokenSeparator) []lookbackPhraseToken {
	hasPreviousToken := len(tokens) > 0
	return append(tokens, lookbackPhraseToken{
		word:                 word,
		adjoinsPreviousToken: !separator.hasNonTightCharacter,
		compoundsWithPreviousToken: hasPreviousToken &&
			!separator.hasWhitespace &&
			!separator.hasVisiblePunctuation &&
			separator.hyphenCount <= 1,
	})
}

// include records one separator character.
func (separator *lookbackTokenSeparator) include(character rune) {
	switch {
	case unicode.IsSpace(character):
		separator.hasWhitespace = true
	case character == '-':
		separator.hyphenCount++
	case character == '\u2010' || character == '\u2011':
		separator.hyphenCount++
		separator.hasNonTightCharacter = true
	case unicode.Is(unicode.Cf, character):
		separator.hasNonTightCharacter = true
	default:
		separator.hasNonTightCharacter = true
		separator.hasVisiblePunctuation = true
	}
}

func isLookbackWordCharacter(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsDigit(character) || unicode.IsMark(character)
}

// matchLookbackPhraseAt matches a lookback phrase that starts at tokens[index].
// The phrase must not start or end inside a compound word.
func matchLookbackPhraseAt(tokens []lookbackPhraseToken, index int) (int, bool) {
	if tokens[index].compoundsWithPreviousToken {
		return 0, false
	}
	var days, endIndex int
	var matched bool
	switch keyword := tokens[index].word; keyword {
	case "past", "last", "previous":
		days, endIndex, matched = matchRelativeLookback(tokens, keyword, index+1)
	case "since":
		days, endIndex, matched = matchSinceAgoLookback(tokens, index+1)
	}
	if !matched || continuesAsCompound(tokens, endIndex) {
		return 0, false
	}
	return days, true
}

// matchRelativeLookback matches "[quantity] unit" starting at tokens[index],
// immediately after keyword ("past", "last", or "previous"). It returns the
// day count and the index just past the unit.
func matchRelativeLookback(tokens []lookbackPhraseToken, keyword string, index int) (days int, endIndex int, matched bool) {
	if !isAdjoiningToken(tokens, index) {
		return 0, 0, false
	}
	quantity, unitIndex, hasQuantity := parseLookbackQuantity(tokens, index)
	if !hasQuantity {
		quantity, unitIndex = 1, index
	}
	unit, isUnit := lookbackUnitAt(tokens, unitIndex)
	if !isUnit {
		return 0, 0, false
	}
	if !hasQuantity && (unit.plural || (keyword == "last" && isLookbackDayUnit(unit))) {
		return 0, 0, false
	}
	days, matched = lookbackDaysFor(quantity, unit)
	return days, unitIndex + 1, matched
}

// matchSinceAgoLookback matches "quantity unit ago" starting at tokens[index],
// immediately after "since". It returns the day count and the index just past
// "ago".
func matchSinceAgoLookback(tokens []lookbackPhraseToken, index int) (days int, endIndex int, matched bool) {
	if !isAdjoiningToken(tokens, index) {
		return 0, 0, false
	}
	quantity, unitIndex, hasQuantity := parseLookbackQuantity(tokens, index)
	if !hasQuantity {
		return 0, 0, false
	}
	unit, isUnit := lookbackUnitAt(tokens, unitIndex)
	agoIndex := unitIndex + 1
	if !isUnit || !isAdjoiningToken(tokens, agoIndex) || tokens[agoIndex].word != "ago" {
		return 0, 0, false
	}
	days, matched = lookbackDaysFor(quantity, unit)
	return days, agoIndex + 1, matched
}

// continuesAsCompound reports whether the word at tokens[endIndex], the first
// word after a candidate phrase, is glued to the phrase's last word as one
// compound word ("week-end", "day-to-day").
func continuesAsCompound(tokens []lookbackPhraseToken, endIndex int) bool {
	return endIndex < len(tokens) && tokens[endIndex].compoundsWithPreviousToken
}

// parseLookbackQuantity reads a quantity starting at tokens[index] and returns
// it with the index of the following token. The caller decides whether
// tokens[index] may start a phrase; later words must adjoin.
func parseLookbackQuantity(tokens []lookbackPhraseToken, index int) (quantity int, nextIndex int, found bool) {
	if index >= len(tokens) {
		return 0, index, false
	}
	word := tokens[index].word
	if isASCIIDigits(word) {
		return parseSaturatingLookbackNumeral(word), index + 1, true
	}
	switch word {
	case "a", "an":
		if isAdjoiningToken(tokens, index+1) && tokens[index+1].word == "couple" {
			return 2, skipAdjoiningOf(tokens, index+2), true
		}
		return 1, index + 1, true
	case "couple":
		return 2, skipAdjoiningOf(tokens, index+1), true
	}
	if numberWordValue, isNumberWord := lookbackNumberWordValue(word); isNumberWord {
		return numberWordValue, index + 1, true
	}
	return 0, index, false
}

// skipAdjoiningOf skips an optional "of" at tokens[index], as in "couple of".
func skipAdjoiningOf(tokens []lookbackPhraseToken, index int) int {
	if isAdjoiningToken(tokens, index) && tokens[index].word == "of" {
		return index + 1
	}
	return index
}

// parseSaturatingLookbackNumeral converts ASCII digits to an int, saturating at
// maximumLookbackQuantity instead of overflowing.
func parseSaturatingLookbackNumeral(digits string) int {
	value := 0
	for index := 0; index < len(digits); index++ {
		value = value*10 + int(digits[index]-'0')
		if value >= maximumLookbackQuantity {
			return maximumLookbackQuantity
		}
	}
	return value
}

func lookbackNumberWordValue(word string) (int, bool) {
	switch word {
	case "one":
		return 1, true
	case "two":
		return 2, true
	case "three":
		return 3, true
	case "four":
		return 4, true
	case "five":
		return 5, true
	case "six":
		return 6, true
	case "seven":
		return 7, true
	case "eight":
		return 8, true
	case "nine":
		return 9, true
	case "ten":
		return 10, true
	case "eleven":
		return 11, true
	case "twelve":
		return 12, true
	}
	return 0, false
}

// lookbackUnitAt reads a unit word at tokens[index], which must adjoin the
// previous word.
func lookbackUnitAt(tokens []lookbackPhraseToken, index int) (lookbackUnit, bool) {
	if !isAdjoiningToken(tokens, index) {
		return lookbackUnit{}, false
	}
	switch tokens[index].word {
	case "hour":
		return lookbackUnit{countsHours: true}, true
	case "hours":
		return lookbackUnit{countsHours: true, plural: true}, true
	case "day":
		return lookbackUnit{daysPerUnit: 1}, true
	case "days":
		return lookbackUnit{daysPerUnit: 1, plural: true}, true
	case "week":
		return lookbackUnit{daysPerUnit: 7}, true
	case "weeks":
		return lookbackUnit{daysPerUnit: 7, plural: true}, true
	case "fortnight":
		return lookbackUnit{daysPerUnit: 14}, true
	case "fortnights":
		return lookbackUnit{daysPerUnit: 14, plural: true}, true
	case "month":
		return lookbackUnit{daysPerUnit: 30}, true
	case "months":
		return lookbackUnit{daysPerUnit: 30, plural: true}, true
	case "year":
		return lookbackUnit{daysPerUnit: 365}, true
	case "years":
		return lookbackUnit{daysPerUnit: 365, plural: true}, true
	}
	return lookbackUnit{}, false
}

// isLookbackDayUnit reports whether unit is "day" or "days".
func isLookbackDayUnit(unit lookbackUnit) bool {
	return !unit.countsHours && unit.daysPerUnit == 1
}

func isAdjoiningToken(tokens []lookbackPhraseToken, index int) bool {
	return index < len(tokens) && tokens[index].adjoinsPreviousToken
}

// lookbackDaysFor converts a quantity of units to whole days, saturating at
// maximumRepresentableLookbackDays. A quantity below 1 is not a lookback.
func lookbackDaysFor(quantity int, unit lookbackUnit) (int, bool) {
	if quantity < 1 {
		return 0, false
	}
	days := 0
	switch {
	case unit.countsHours:
		days = (quantity + 23) / 24
	case quantity > maximumRepresentableLookbackDays/unit.daysPerUnit:
		days = maximumRepresentableLookbackDays
	default:
		days = quantity * unit.daysPerUnit
	}
	if days > maximumRepresentableLookbackDays {
		days = maximumRepresentableLookbackDays
	}
	return days, true
}

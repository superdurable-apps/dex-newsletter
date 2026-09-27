// Package subscribers turns the raw cell values of the newsletter subscriber
// sheet into a validated, deduplicated, bounded recipient list.
//
// Every cell is untrusted input. The package performs no I/O and reads no
// clock or randomness, so identical rows and layout always produce an
// identical model.SubscriberList. A retried Dex Step therefore recomputes the
// same audience snapshot.
//
// # Sheet shape
//
// Rows are Google Sheets values: rows may be ragged, trailing empty cells are
// omitted, and a missing cell reads as blank. A row is fully blank when every
// cell is blank after trimming; fully blank rows are ignored everywhere.
//
// The first non-blank row is the header row when one of its cells equals
// SheetLayout.EmailColumnHeader after trimming, compared case-insensitively.
// When several header cells match, the leftmost one wins.
//
// A non-blank SheetLayout.StatusColumnHeader makes the status column
// required: the header row must have a cell equal to it, found the same way
// (leftmost wins), or BuildSubscriberList returns an error. The package fails
// closed here on purpose. Otherwise a renamed, misspelled, or deleted status
// column, or a deleted header row, would silently turn unsubscribe filtering
// off, model.SubscriberList would show nothing unusual, and the newsletter
// would reach people who opted out. Setting StatusColumnHeader to blank is
// the explicit way to send without status filtering.
//
// When the first non-blank row has no matching header cell but its first cell
// is a valid email address, the sheet has no header: every row is data and the
// email column is column 0. Such a sheet has no status column, so it is
// accepted only when StatusColumnHeader is blank. Any other first row is an
// error, because no email column can be identified. A sheet with no non-blank
// row has neither a header nor data and yields an empty list, not an error.
//
// # Address policy
//
// A recipient must be a single bare RFC 5322 addr-spec in printable ASCII:
// a dot-atom local part of at most 64 characters, exactly one '@', and a
// hostname domain with at least two labels, for a total of at most 254
// characters. Display names, groups, lists, comments, quoted local parts,
// domain literals, and any whitespace, control, or non-ASCII character are
// rejected rather than repaired, so a cell such as
// "a@example.com\r\nBcc: b@example.com" or "Name <a@example.com>" can never
// reach a message header. Internationalized addresses are rejected on purpose:
// they need SMTPUTF8 end to end, admit invisible and look-alike characters,
// and have no single case mapping for deduplication.
//
// Accepted addresses are lowercased in full, local part included, both for
// deduplication and in the output. Local parts are case-sensitive in RFC 5321
// section 2.4, but mainstream mailbox providers treat them case-insensitively,
// and lowercasing makes "Alice@Example.com" and "alice@example.com" one
// subscriber with one canonical spelling. Provider-specific aliases such as
// dots or "+tag" suffixes are not merged.
package subscribers

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// SheetLayout describes where subscriber data lives in the sheet and bounds
// the size of the resulting recipient list.
type SheetLayout struct {
	// EmailColumnHeader is the header cell text that names the email column.
	// It is compared trimmed and case-insensitively and must not be blank.
	EmailColumnHeader string
	// StatusColumnHeader is the header cell text that names the subscription
	// status column. It is compared trimmed and case-insensitively and must
	// differ from EmailColumnHeader. When it is not blank the sheet must have
	// a header row with this column, or BuildSubscriberList returns an error,
	// so a missing status column never disables unsubscribe filtering
	// silently. A blank value is the explicit choice to send without status
	// filtering, and is required for a sheet without a header row.
	StatusColumnHeader string
	// UnsubscribedStatusValues lists the status cell values, compared trimmed
	// and case-insensitively, that exclude an address from the list. A blank
	// entry matches a blank or missing status cell.
	UnsubscribedStatusValues []string
	// MaxRecipients is the largest number of recipients the list may hold. It
	// must be positive.
	MaxRecipients int
}

// SheetLayoutFromConfiguration returns the sheet layout named by the
// newsletter configuration. The unsubscribed status values are copied, so
// later changes to the configuration slice do not alter the layout.
func SheetLayoutFromConfiguration(configuration config.NewsletterConfiguration) SheetLayout {
	var unsubscribedStatusValues []string
	if configuration.UnsubscribedStatusValues != nil {
		unsubscribedStatusValues = make([]string, len(configuration.UnsubscribedStatusValues))
		copy(unsubscribedStatusValues, configuration.UnsubscribedStatusValues)
	}
	return SheetLayout{
		EmailColumnHeader:        configuration.EmailColumnHeader,
		StatusColumnHeader:       configuration.StatusColumnHeader,
		UnsubscribedStatusValues: unsubscribedStatusValues,
		MaxRecipients:            configuration.MaxRecipients,
	}
}

// BuildSubscriberList validates the sheet rows against the layout and returns
// the recipients in first-seen order together with the counts of every row it
// set aside. It returns an error only for an unusable layout (a blank email
// column header, a status column header equal to the email column header, or
// a MaxRecipients that is not positive) or when the sheet does not match the
// layout: it has no identifiable email column, or StatusColumnHeader is not
// blank and the sheet has no header row with that column. Error messages
// name only row numbers and layout values, never cell contents. A sheet with
// no non-blank rows, or one whose rows yield no recipients, is not an error;
// the caller decides what zero recipients means.
//
// RowsRead counts every non-blank data row; the header row is not a data row.
// Each such row lands in exactly one of these outcomes, checked in order:
//
//   - its email cell is blank: the row is skipped and counted nowhere else;
//   - its address is not valid (see the package documentation):
//     InvalidAddresses;
//   - its address is unsubscribed: UnsubscribedCount;
//   - its address already appeared on an earlier row, compared
//     case-insensitively: DuplicateCount;
//   - the list already holds MaxRecipients addresses: TruncatedCount;
//   - otherwise the lowercased address is appended to Recipients.
//
// An address is unsubscribed when the status cell of any row that carries it
// holds one of the UnsubscribedStatusValues. Every row with that address is
// then counted in UnsubscribedCount, including rows whose own status is
// blank or subscribed, so an unsubscribe can never be overridden by a
// duplicate row placed before or after it.
//
// The rows and the layout are not modified. Recipients is never nil.
func BuildSubscriberList(rows [][]string, layout SheetLayout) (model.SubscriberList, error) {
	if layout.MaxRecipients <= 0 {
		return model.SubscriberList{}, fmt.Errorf("subscriber sheet layout: max recipients must be positive, got %d", layout.MaxRecipients)
	}
	emailColumnHeader := trimCell(layout.EmailColumnHeader)
	if emailColumnHeader == "" {
		return model.SubscriberList{}, errors.New("subscriber sheet layout: email column header is blank")
	}
	statusColumnHeader := trimCell(layout.StatusColumnHeader)
	if statusColumnHeader != "" && strings.EqualFold(statusColumnHeader, emailColumnHeader) {
		return model.SubscriberList{}, fmt.Errorf("subscriber sheet layout: status column header %q must differ from email column header %q", layout.StatusColumnHeader, layout.EmailColumnHeader)
	}

	subscriberList := model.SubscriberList{Recipients: []string{}}
	columns, hasData, err := locateSheetColumns(rows, emailColumnHeader, statusColumnHeader)
	if err != nil {
		return model.SubscriberList{}, err
	}
	if !hasData {
		return subscriberList, nil
	}
	unsubscribedStatusValues := make([]string, len(layout.UnsubscribedStatusValues))
	for index, statusValue := range layout.UnsubscribedStatusValues {
		unsubscribedStatusValues[index] = trimCell(statusValue)
	}

	// The first pass validates every data row and records which addresses are
	// unsubscribed anywhere in the sheet, so that the second pass can suppress
	// every row of such an address regardless of row order.
	var validAddresses []string
	unsubscribedAddresses := map[string]bool{}
	for _, row := range rows[columns.firstDataRowIndex:] {
		if isBlankRow(row) {
			continue
		}
		subscriberList.RowsRead++
		emailCell := trimCell(cellAt(row, columns.emailColumnIndex))
		if emailCell == "" {
			continue
		}
		address, valid := canonicalEmailAddress(emailCell)
		if !valid {
			subscriberList.InvalidAddresses++
			continue
		}
		if columns.statusColumnIndex >= 0 && isUnsubscribedStatus(cellAt(row, columns.statusColumnIndex), unsubscribedStatusValues) {
			unsubscribedAddresses[address] = true
		}
		validAddresses = append(validAddresses, address)
	}

	seenAddresses := map[string]bool{}
	for _, address := range validAddresses {
		switch {
		case unsubscribedAddresses[address]:
			subscriberList.UnsubscribedCount++
		case seenAddresses[address]:
			subscriberList.DuplicateCount++
		case len(subscriberList.Recipients) >= layout.MaxRecipients:
			seenAddresses[address] = true
			subscriberList.TruncatedCount++
		default:
			seenAddresses[address] = true
			subscriberList.Recipients = append(subscriberList.Recipients, address)
		}
	}
	return subscriberList, nil
}

// sheetColumns records where the email and status columns are and where the
// data rows begin.
type sheetColumns struct {
	emailColumnIndex int
	// statusColumnIndex is -1 when the sheet has no status column.
	statusColumnIndex int
	firstDataRowIndex int
}

// locateSheetColumns finds the header row, if any, and the email and status
// columns. The headers must already be trimmed. A non-blank status column
// header must name a column of the header row. hasData is false when every
// row is blank.
func locateSheetColumns(rows [][]string, emailColumnHeader string, statusColumnHeader string) (columns sheetColumns, hasData bool, err error) {
	for rowIndex, row := range rows {
		if isBlankRow(row) {
			continue
		}
		emailColumnIndex := indexOfHeaderCell(row, emailColumnHeader)
		if emailColumnIndex >= 0 {
			statusColumnIndex := -1
			if statusColumnHeader != "" {
				statusColumnIndex = indexOfHeaderCell(row, statusColumnHeader)
				if statusColumnIndex < 0 {
					return sheetColumns{}, false, fmt.Errorf(
						"subscriber sheet has no status column: header row %d has no cell equal to %q, so unsubscribed addresses cannot be excluded; name the status column %q, or set the status column header to blank to send without status filtering",
						rowIndex+1, statusColumnHeader, statusColumnHeader)
				}
			}
			return sheetColumns{
				emailColumnIndex:  emailColumnIndex,
				statusColumnIndex: statusColumnIndex,
				firstDataRowIndex: rowIndex + 1,
			}, true, nil
		}
		if _, valid := canonicalEmailAddress(trimCell(cellAt(row, 0))); valid {
			if statusColumnHeader != "" {
				return sheetColumns{}, false, fmt.Errorf(
					"subscriber sheet has no status column: row %d, the first non-blank row, holds an address instead of a header row, so the status column %q cannot be located and unsubscribed addresses cannot be excluded; add a header row naming the email and status columns, or set the status column header to blank to send without status filtering",
					rowIndex+1, statusColumnHeader)
			}
			return sheetColumns{
				emailColumnIndex:  0,
				statusColumnIndex: -1,
				firstDataRowIndex: rowIndex,
			}, true, nil
		}
		return sheetColumns{}, false, fmt.Errorf(
			"subscriber sheet has no email column: row %d, the first non-blank row, has no header cell equal to %q and its first cell is not a valid email address",
			rowIndex+1, emailColumnHeader)
	}
	return sheetColumns{}, false, nil
}

// indexOfHeaderCell returns the index of the leftmost cell equal to the
// trimmed header, compared trimmed and case-insensitively, or -1.
func indexOfHeaderCell(row []string, header string) int {
	for cellIndex, cell := range row {
		if strings.EqualFold(trimCell(cell), header) {
			return cellIndex
		}
	}
	return -1
}

// isUnsubscribedStatus reports whether the status cell equals one of the
// trimmed unsubscribed status values, compared trimmed and case-insensitively.
func isUnsubscribedStatus(statusCell string, unsubscribedStatusValues []string) bool {
	status := trimCell(statusCell)
	for _, unsubscribedStatusValue := range unsubscribedStatusValues {
		if strings.EqualFold(status, unsubscribedStatusValue) {
			return true
		}
	}
	return false
}

// cellAt returns the cell at the column index, or "" for a column past the end
// of a ragged row.
func cellAt(row []string, columnIndex int) string {
	if columnIndex < 0 || columnIndex >= len(row) {
		return ""
	}
	return row[columnIndex]
}

// isBlankRow reports whether every cell of the row is blank after trimming.
func isBlankRow(row []string) bool {
	for _, cell := range row {
		if trimCell(cell) != "" {
			return false
		}
	}
	return true
}

// trimCell removes leading and trailing Unicode white space and the invisible
// characters that spreadsheet imports commonly leave at cell edges: the byte
// order mark, zero-width space and joiners, and the word joiner. Characters
// inside the cell are never removed.
func trimCell(cell string) string {
	return strings.TrimFunc(cell, isIgnorableCellEdgeRune)
}

// isIgnorableCellEdgeRune reports whether trimCell removes the rune.
func isIgnorableCellEdgeRune(character rune) bool {
	switch character {
	case '\uFEFF', '\u200B', '\u200C', '\u200D', '\u2060':
		return true
	}
	return unicode.IsSpace(character)
}

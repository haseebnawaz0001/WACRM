package handlers

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/dedupe"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/phoneutil"
	"gorm.io/gorm"
)

// ContactImportRow is one parsed CSV row.
type ContactImportRow struct {
	Line            int
	PhoneNumber     string
	ProfileName     string
	WhatsAppAccount string
	Tags            []string
	AssignedUserID  string
	Fields          map[string]any
}

// ContactImportError explains why one row was skipped.
type ContactImportError struct {
	Line    int    `json:"line"`
	Phone   string `json:"phone,omitempty"`
	Message string `json:"message"`
}

// ContactImportResult summarises an import.
type ContactImportResult struct {
	Created int                  `json:"created"`
	Updated int                  `json:"updated"`
	Skipped int                  `json:"skipped"`
	Errors  []ContactImportError `json:"errors"`

	// MergedInFile counts rows collapsed because another row in the same file
	// named the same person. Reported rather than silent: a spreadsheet with
	// the same customer on four lines is a fact about the file its owner
	// should hear, and "created 96 of 100" with no explanation reads as data
	// loss (plan 06).
	MergedInFile int `json:"merged_in_file"`

	// Flagged counts contacts created by create_anyway that were also raised
	// as duplicate candidates for review.
	Flagged int `json:"flagged"`
}

// What an import does when a row names a contact that already exists (plan 06).
const (
	// OnMatchSkip leaves the existing record alone. The default, because an
	// import is usually a list of people rather than a correction of them,
	// and overwriting a record an agent curated with a stale spreadsheet is
	// the expensive mistake of the three.
	OnMatchSkip = "skip"
	// OnMatchUpdate applies the row's fields, tags and name to the match.
	OnMatchUpdate = "update"
	// OnMatchCreateAnyway inserts a second record and flags the pair for
	// review, for the case where one number genuinely serves two people —
	// a shared family phone, a reception desk.
	OnMatchCreateAnyway = "create_anyway"
)

// ContactImportOpts controls how a file is applied.
type ContactImportOpts struct {
	// OnMatch is one of the OnMatch* values. An empty value means skip.
	OnMatch string
}

// onMatch reads the option, defaulting rather than failing: an unrecognised
// value from an old client should not lose somebody's import.
func (o ContactImportOpts) onMatch() string {
	switch o.OnMatch {
	case OnMatchUpdate, OnMatchCreateAnyway:
		return o.OnMatch
	default:
		return OnMatchSkip
	}
}

// maxImportErrors bounds the error list returned to the caller. A file with
// thousands of bad rows is a mistake in the file, and the first few explain it.
const maxImportErrors = 50

// reservedImportColumns are the built-in contact columns; every other header is
// matched against a custom field key.
var reservedImportColumns = map[string]bool{
	"phone_number": true, "phone": true,
	"profile_name": true, "name": true,
	"whatsapp_account": true, "whats_app_account": true, "account": true,
	"tags": true, "assigned_user_id": true,
}

// ImportContactsCSV imports contacts from CSV (plan 01).
//
// This replaces the reflection importer for contacts, which matched on the
// exact phone string — so "+923…" and "923…" produced two contacts — and failed
// the whole import with a unique-constraint error when a row collided with a
// soft-deleted contact. Resolution goes through the contact lifecycle service,
// which knows about both.
//
// A bad row is skipped and reported rather than failing the file: an import of
// a thousand contacts should not be lost to one malformed phone number.
func (a *App) ImportContactsCSV(orgID, userID uuid.UUID, source io.Reader, opts ContactImportOpts) (ContactImportResult, error) {
	var result ContactImportResult

	reader := csv.NewReader(source)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return result, fmt.Errorf("could not read the CSV header: %w", err)
	}

	defs, err := customfields.New(a.DB).DefinitionsByKey(context.Background(), orgID, models.FieldEntityContact)
	if err != nil {
		return result, err
	}

	columns := normaliseImportHeader(header)
	if !hasPhoneColumn(columns) {
		return result, fmt.Errorf("the CSV needs a phone number column")
	}

	lifecycle := contacts.New(a.DB)
	fieldSvc := customfields.New(a.DB)
	countryCode := a.orgDefaultCountryCode(orgID)

	// Rows are collected before anything is written so duplicates inside the
	// file can be collapsed (plan 06). Importing them one at a time makes the
	// same person arrive twice: the first row creates the contact and the
	// second is reported as a match against an import that has not finished,
	// which is a confusing way to describe a problem with the spreadsheet.
	line := 1
	seen := map[string]int{} // normalised phone -> index in rows
	var rows []ContactImportRow

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			result.Skipped++
			a.appendImportError(&result, ContactImportError{Line: line, Message: err.Error()})
			continue
		}

		row := parseContactImportRow(line, columns, record, defs)
		if row.PhoneNumber == "" {
			result.Skipped++
			a.appendImportError(&result, ContactImportError{Line: line, Message: "missing phone number"})
			continue
		}

		key := phoneutil.NormalizeWithCountry(row.PhoneNumber, countryCode)
		if key == "" {
			key = row.PhoneNumber
		}
		if at, duplicate := seen[key]; duplicate {
			// The last row wins: a file is usually appended to, so the later
			// line is the more recent thing somebody knew.
			rows[at] = row
			result.MergedInFile++
			continue
		}
		seen[key] = len(rows)
		rows = append(rows, row)
	}

	for _, row := range rows {
		outcome, err := a.importOneContact(orgID, userID, lifecycle, fieldSvc, row, countryCode, opts.onMatch())
		if err != nil {
			result.Skipped++
			a.appendImportError(&result, ContactImportError{
				Line: row.Line, Phone: row.PhoneNumber, Message: err.Error(),
			})
			continue
		}
		switch outcome {
		case importCreated:
			result.Created++
		case importFlagged:
			result.Created++
			result.Flagged++
		case importUpdated:
			result.Updated++
		case importSkipped:
			result.Skipped++
		}
	}

	return result, nil
}

// What happened to one row.
type importOutcome string

const (
	importCreated importOutcome = "created"
	importUpdated importOutcome = "updated"
	importSkipped importOutcome = "skipped"
	importFlagged importOutcome = "flagged"
)

// importOneContact applies one row under the chosen match policy.
func (a *App) importOneContact(orgID, userID uuid.UUID, lifecycle *contacts.Service,
	fieldSvc *customfields.Service, row ContactImportRow, countryCode, onMatch string) (importOutcome, error) {

	// Find first, decide second. Resolving with CreateIfMissing would settle
	// the question before the policy is consulted, and "skip" has to be able
	// to leave without writing anything.
	existing, _, err := lifecycle.Resolve(context.Background(), orgID,
		contacts.Identity{Phone: row.PhoneNumber}, contacts.ResolveOpts{
			CreateIfMissing:    false,
			AllowRestore:       false,
			DefaultCountryCode: countryCode,
		})
	switch {
	case err != nil && !errors.Is(err, contacts.ErrNotFound):
		return "", err
	case existing != nil:
		switch onMatch {
		case OnMatchSkip:
			return importSkipped, nil
		case OnMatchUpdate:
			if err := a.applyImportRow(orgID, userID, fieldSvc, existing, row); err != nil {
				return "", err
			}
			if row.ProfileName != "" && existing.ProfileName == "" {
				// Only fills a gap: a name from a spreadsheet must not replace
				// the one WhatsApp reported for that person.
				if err := a.DB.Model(existing).Update("profile_name", row.ProfileName).Error; err != nil {
					return "", err
				}
			}
			return importUpdated, nil
		}
		// create_anyway falls through to the insert below.
	}

	contact, outcome, err := lifecycle.Resolve(context.Background(), orgID,
		contacts.Identity{Phone: row.PhoneNumber}, contacts.ResolveOpts{
			CreateIfMissing: true,
			// create_anyway means a second record for a number that already
			// has one, so the lookup is skipped rather than consulted.
			ForceCreate:        existing != nil,
			AllowRestore:       false,
			UpdateName:         row.ProfileName != "",
			ProfileName:        row.ProfileName,
			Source:             contacts.SourceImport,
			DefaultCountryCode: countryCode,
			Actor:              crmevents.UserActor(userID, ""),
		})
	if err != nil {
		return "", err
	}

	if existing != nil && contact.ID == existing.ID {
		// One WhatsApp number is one chat thread, and the database enforces
		// that: two live contacts on the same number would make every inbound
		// message ambiguous. create_anyway can separate a shared phone written
		// differently — a reception desk stored as +1 415 555 0104 and a
		// person as 14155550104 — but not one written identically. Saying so
		// beats quietly updating the record the option asked not to touch.
		return "", fmt.Errorf("%s already belongs to another contact; "+
			"merge them or import this person under their own number", row.PhoneNumber)
	}

	if err := a.applyImportRow(orgID, userID, fieldSvc, contact, row); err != nil {
		return "", err
	}

	if existing != nil {
		// Two records for one person is what the importer was told to do, not
		// something to hide: the pair is raised for review so somebody can
		// merge them if the file was simply wrong.
		a.flagImportDuplicate(orgID, existing.ID, contact.ID)
		return importFlagged, nil
	}
	if outcome == contacts.OutcomeCreated {
		return importCreated, nil
	}
	return importUpdated, nil
}

// applyImportRow writes a row's columns and custom fields onto a contact.
func (a *App) applyImportRow(orgID, userID uuid.UUID, fieldSvc *customfields.Service,
	contact *models.Contact, row ContactImportRow) error {

	updates := map[string]any{}
	if row.WhatsAppAccount != "" {
		var account models.WhatsAppAccount
		if err := a.DB.Where("organization_id = ? AND name = ?", orgID, row.WhatsAppAccount).
			First(&account).Error; err != nil {
			return fmt.Errorf("unknown WhatsApp account %q", row.WhatsAppAccount)
		}
		updates["whatsapp_account"] = account.Name
	}
	if row.AssignedUserID != "" {
		assignee, err := uuid.Parse(row.AssignedUserID)
		if err != nil {
			return fmt.Errorf("assigned_user_id %q is not a valid id", row.AssignedUserID)
		}
		var user models.User
		if err := a.DB.Where("id = ? AND organization_id = ?", assignee, orgID).First(&user).Error; err != nil {
			return fmt.Errorf("assigned user %s is not a member of this organization", assignee)
		}
		updates["assigned_user_id"] = assignee
	}
	if len(row.Tags) > 0 {
		// Union rather than replace: an import that adds "webinar-2026"
		// should not strip the tags an agent applied by hand.
		tags := mergeImportTags(contact.Tags, row.Tags)
		updates["tags"] = tags
	}

	return a.DB.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&models.Contact{}).Where("id = ?", contact.ID).
				Updates(updates).Error; err != nil {
				return err
			}
		}
		if len(row.Fields) > 0 {
			if _, err := fieldSvc.SetValues(tx, orgID, contact.ID,
				models.FieldEntityContact, row.Fields, &userID); err != nil {
				return err
			}
		}
		return nil
	})
}

// mergeImportTags adds the file's tags to the ones the contact already has.
func mergeImportTags(existing models.JSONBArray, incoming []string) models.JSONBArray {
	have := make(map[string]bool, len(existing))
	out := make(models.JSONBArray, 0, len(existing)+len(incoming))
	for _, raw := range existing {
		out = append(out, raw)
		if tag, ok := raw.(string); ok {
			have[tag] = true
		}
	}
	for _, tag := range incoming {
		if !have[tag] {
			out = append(out, tag)
			have[tag] = true
		}
	}
	return out
}

// flagImportDuplicate raises the pair create_anyway produced for review.
//
// Best-effort: the contact was created and the import must not fail because
// the review queue could not be written. The hourly scan would find the pair
// anyway; this only makes it visible now, while somebody remembers importing.
func (a *App) flagImportDuplicate(orgID, primaryID, duplicateID uuid.UUID) {
	if err := dedupe.New(a.DB).FlagPair(context.Background(), orgID, primaryID, duplicateID, dedupe.ReasonImport); err != nil {
		a.Log.Error("Failed to flag imported duplicate", "error", err,
			"primary", primaryID, "duplicate", duplicateID)
	}
}

// appendImportError records an error, up to the reporting cap.
func (a *App) appendImportError(result *ContactImportResult, e ContactImportError) {
	if len(result.Errors) < maxImportErrors {
		result.Errors = append(result.Errors, e)
	}
}

// normaliseImportHeader lower-cases and trims the header row so "Phone Number"
// and "phone_number" name the same column.
func normaliseImportHeader(header []string) []string {
	out := make([]string, len(header))
	for i, name := range header {
		normalised := strings.ToLower(strings.TrimSpace(name))
		normalised = strings.ReplaceAll(normalised, " ", "_")
		out[i] = strings.TrimPrefix(normalised, "\ufeff") // strip a UTF-8 BOM
	}
	return out
}

func hasPhoneColumn(columns []string) bool {
	for _, c := range columns {
		if c == "phone_number" || c == "phone" {
			return true
		}
	}
	return false
}

// parseContactImportRow maps one CSV record onto a row, routing unrecognised
// headers to custom fields when they match a field key.
func parseContactImportRow(line int, columns, record []string, defs map[string]models.CustomFieldDefinition) ContactImportRow {
	row := ContactImportRow{Line: line, Fields: map[string]any{}}

	for i, column := range columns {
		if i >= len(record) {
			break
		}
		value := strings.TrimSpace(record[i])
		if value == "" {
			continue
		}

		switch column {
		case "phone_number", "phone":
			row.PhoneNumber = value
		case "profile_name", "name":
			row.ProfileName = value
		case "whatsapp_account", "whats_app_account", "account":
			row.WhatsAppAccount = value
		case "assigned_user_id":
			row.AssignedUserID = value
		case "tags":
			for _, tag := range strings.Split(value, ",") {
				if trimmed := strings.TrimSpace(tag); trimmed != "" {
					row.Tags = append(row.Tags, trimmed)
				}
			}
		default:
			if reservedImportColumns[column] {
				continue
			}
			// A header that names a custom field imports into it; anything
			// else is ignored rather than rejected, so an export from another
			// system can be imported without editing the file first.
			key := strings.TrimPrefix(column, customfields.FieldKeyPrefix)
			if _, ok := defs[key]; ok {
				row.Fields[key] = value
			}
		}
	}

	return row
}

// orgDefaultCountryCode reads the organization's default calling code, used to
// expand local "0"-prefixed numbers during import.
func (a *App) orgDefaultCountryCode(orgID uuid.UUID) string {
	var org models.Organization
	if err := a.DB.Select("settings").Where("id = ?", orgID).First(&org).Error; err != nil {
		return ""
	}
	code, _ := org.Settings["default_country_code"].(string)
	return code
}

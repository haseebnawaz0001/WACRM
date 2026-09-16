package handlers

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
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
func (a *App) ImportContactsCSV(orgID, userID uuid.UUID, source io.Reader) (ContactImportResult, error) {
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

	line := 1
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

		created, err := a.importOneContact(orgID, userID, lifecycle, fieldSvc, row, countryCode)
		if err != nil {
			result.Skipped++
			a.appendImportError(&result, ContactImportError{
				Line: line, Phone: row.PhoneNumber, Message: err.Error(),
			})
			continue
		}
		if created {
			result.Created++
		} else {
			result.Updated++
		}
	}

	return result, nil
}

// importOneContact resolves and updates a single row, reporting whether the
// contact was newly created.
func (a *App) importOneContact(orgID, userID uuid.UUID, lifecycle *contacts.Service, fieldSvc *customfields.Service, row ContactImportRow, countryCode string) (bool, error) {
	// An import must not resurrect a contact somebody deleted, but it should
	// find one that already exists under a differently formatted number.
	contact, outcome, err := lifecycle.Resolve(context.Background(), orgID,
		contacts.Identity{Phone: row.PhoneNumber}, contacts.ResolveOpts{
			CreateIfMissing:    true,
			AllowRestore:       false,
			UpdateName:         row.ProfileName != "",
			ProfileName:        row.ProfileName,
			Source:             contacts.SourceImport,
			DefaultCountryCode: countryCode,
			Actor:              crmevents.UserActor(userID, ""),
		})
	if err != nil {
		return false, err
	}

	updates := map[string]any{}
	if row.WhatsAppAccount != "" {
		var account models.WhatsAppAccount
		if err := a.DB.Where("organization_id = ? AND name = ?", orgID, row.WhatsAppAccount).
			First(&account).Error; err != nil {
			return false, fmt.Errorf("unknown WhatsApp account %q", row.WhatsAppAccount)
		}
		updates["whatsapp_account"] = account.Name
	}
	if row.AssignedUserID != "" {
		assignee, err := uuid.Parse(row.AssignedUserID)
		if err != nil {
			return false, fmt.Errorf("assigned_user_id %q is not a valid id", row.AssignedUserID)
		}
		var user models.User
		if err := a.DB.Where("id = ? AND organization_id = ?", assignee, orgID).First(&user).Error; err != nil {
			return false, fmt.Errorf("assigned user %s is not a member of this organization", assignee)
		}
		updates["assigned_user_id"] = assignee
	}
	if len(row.Tags) > 0 {
		tags := make(models.JSONBArray, 0, len(row.Tags))
		for _, tag := range row.Tags {
			tags = append(tags, tag)
		}
		updates["tags"] = tags
	}

	if err := a.DB.Transaction(func(tx *gorm.DB) error {
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
	}); err != nil {
		return false, err
	}

	return outcome == contacts.OutcomeCreated, nil
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

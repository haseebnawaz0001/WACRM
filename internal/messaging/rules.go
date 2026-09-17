// Package messaging holds the rules that decide whether an outbound message is
// allowed, and why not when it is not (plan 00, F10 / plan 10, S4).
//
// Four code paths sent WhatsApp messages — the chat handler, the template API,
// the campaign worker and the automation action library — and each re-derived
// the rules for itself. They disagreed in ways nobody could see from any one of
// them:
//
//   - The 24-hour service window was enforced for automation sends and not for
//     the chat handler, which let Meta reject the message instead and surfaced
//     an opaque API error to the agent.
//   - The marketing opt-out check was written out three times, against three
//     slightly different spellings of the category comparison.
//   - The worker checked approval at start, the handler at send, and automation
//     not at all, so a template unapproved between the two moments produced a
//     different failure in each caller.
//
// These are pure functions over the records, not a transport: the transport
// legitimately differs between an HTTP request that can stream an upload and a
// worker that cannot. What must not differ is the answer to "may we send this?"
package messaging

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/templateutil"
)

// ServiceWindow is how long after a customer's last message WhatsApp allows a
// free-form reply. Outside it only an approved template may be sent.
const ServiceWindow = 24 * time.Hour

// Send refusals. Typed so a caller can turn each into the right thing: a 400
// with a readable message for an agent, a permanent recipient failure for the
// worker, a recorded skip reason for an automation run.
var (
	// ErrTemplateMissing: the template was deleted, or never existed.
	ErrTemplateMissing = errors.New("messaging: template not found")

	// ErrTemplateNotApproved: Meta has not approved it, or moved it out of
	// APPROVED after a quality review.
	ErrTemplateNotApproved = errors.New("messaging: template is not approved")

	// ErrMarketingOptOut: this contact asked not to receive marketing.
	ErrMarketingOptOut = errors.New("messaging: contact has opted out of marketing messages")

	// ErrOutsideServiceWindow: more than 24 hours since the customer wrote.
	ErrOutsideServiceWindow = errors.New("messaging: the 24-hour service window has closed for this contact; use a template instead")

	// ErrMissingParams: the template has variables the caller did not fill.
	ErrMissingParams = errors.New("messaging: template parameters are missing")

	// ErrNoRecipient: no phone number and no BSUID to send to.
	ErrNoRecipient = errors.New("messaging: contact has no address to send to")
)

// MissingParamsError names which parameters were not supplied.
//
// "Template parameters are missing" sends an author back to compare the
// template against their payload by hand; naming them does not.
type MissingParamsError struct {
	Names []string
}

func (e *MissingParamsError) Error() string {
	return fmt.Sprintf("messaging: missing template parameters: %s", strings.Join(e.Names, ", "))
}

func (e *MissingParamsError) Unwrap() error { return ErrMissingParams }

// WithinServiceWindow reports whether a free-form reply is still allowed.
//
// A contact who has never written is outside it: the window is opened by the
// customer, and a contact created by an import or a campaign has not opened one.
func WithinServiceWindow(contact *models.Contact, now time.Time) bool {
	if contact == nil || contact.LastMessageAt == nil {
		return false
	}
	return now.Sub(*contact.LastMessageAt) < ServiceWindow
}

// IsMarketing reports whether a template counts as marketing.
//
// Case-insensitive because Meta's category arrives capitalised and the value
// has been stored both ways over the product's life; a case-sensitive
// comparison here silently stopped honouring opt-outs for the other spelling.
func IsMarketing(template *models.Template) bool {
	return template != nil && strings.EqualFold(template.Category, "MARKETING")
}

// CheckFreeText reports whether a free-form message may be sent.
func CheckFreeText(contact *models.Contact, now time.Time) error {
	if err := checkRecipient(contact); err != nil {
		return err
	}
	if !WithinServiceWindow(contact, now) {
		return ErrOutsideServiceWindow
	}
	return nil
}

// CheckTemplate reports whether a template may be sent to a contact.
//
// params are the body parameter values the caller has, keyed the way the
// template names them. Nil skips the parameter check, for callers that fill
// them later from a per-recipient row.
func CheckTemplate(template *models.Template, contact *models.Contact, params map[string]string) error {
	if template == nil {
		return ErrTemplateMissing
	}
	if err := checkRecipient(contact); err != nil {
		return err
	}
	if !strings.EqualFold(template.Status, "APPROVED") {
		return ErrTemplateNotApproved
	}
	// Opt-out applies to marketing only: a delivery notification or a
	// one-time password is not what somebody opted out of.
	if IsMarketing(template) && contact.MarketingOptOut {
		return ErrMarketingOptOut
	}
	if params != nil {
		if missing := MissingParams(template, params); len(missing) > 0 {
			return &MissingParamsError{Names: missing}
		}
	}
	return nil
}

// MissingParams lists the template's body parameters the caller did not fill.
//
// Meta rejects the whole send when a parameter is absent, so finding out here
// turns a failed delivery into a message the author can act on.
func MissingParams(template *models.Template, params map[string]string) []string {
	if template == nil {
		return nil
	}

	missing := make([]string, 0)
	for _, name := range BodyParamNames(template) {
		if strings.TrimSpace(params[name]) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

// BodyParamNames returns the parameter names a template's body uses.
//
// It delegates to templateutil, which is the extractor the campaign worker and
// the template editor already share — a second implementation here would
// disagree with them about some template nobody has written yet, and that
// disagreement would show up as a send Meta rejects.
//
// Positional templates name them "1", "2", …; named templates use words. Both
// come back as written, because that is how the recipient rows key them.
func BodyParamNames(template *models.Template) []string {
	if template == nil {
		return nil
	}
	return templateutil.ExtParamNames(template.BodyContent)
}

func checkRecipient(contact *models.Contact) error {
	if contact == nil || (strings.TrimSpace(contact.PhoneNumber) == "" && strings.TrimSpace(contact.BSUID) == "") {
		return ErrNoRecipient
	}
	return nil
}

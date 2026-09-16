// Package phoneutil normalises WhatsApp phone numbers (plan 00, F7).
//
// The same person's number reaches us in several shapes: "+92 321 1234567"
// from a CSV, "923211234567" from Meta, "03211234567" typed by a user. Storing
// whatever arrived means the same contact is created twice, imports collide on
// the unique index, and duplicate detection has nothing to compare.
//
// Normalize reduces a number to comparable digits. It is deliberately not a
// full E.164 parser: the product accepts numbers from every country, and
// guessing at a national format we cannot verify would corrupt more numbers
// than it fixes. What it does is remove formatting, and — only when the caller
// supplies a default country code — expand the common local "0" prefix.
package phoneutil

import "strings"

// Normalize strips formatting and any leading "+" from a phone number.
//
// Group JIDs (120363422675615917@g.us) are returned unchanged: they are
// WhatsApp identifiers rather than phone numbers, and stripping characters
// from them would break the lookup entirely.
func Normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "@") {
		return raw
	}

	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizeWithCountry normalises a number and expands a single leading zero
// using the supplied country calling code.
//
// "03211234567" with country code "92" becomes "923211234567". The expansion is
// applied only when a country code is given, because a leading zero is not a
// national prefix everywhere, and rewriting it blindly would corrupt numbers
// from countries that do not use one.
func NormalizeWithCountry(raw, defaultCountryCode string) string {
	normalized := Normalize(raw)
	if normalized == "" || strings.Contains(normalized, "@") {
		return normalized
	}

	cc := Normalize(defaultCountryCode)
	if cc == "" {
		return normalized
	}

	// Already international: leave it alone.
	if strings.HasPrefix(normalized, cc) && len(normalized) > len(cc) {
		return normalized
	}
	if strings.HasPrefix(normalized, "0") {
		return cc + strings.TrimLeft(normalized, "0")
	}
	return normalized
}

// Variants returns the stored forms a number might already appear as, most
// canonical first.
//
// Existing rows were written before normalisation existed, so the same contact
// may be stored with or without a "+". A lookup has to try both before
// concluding the contact is new — that omission is what makes imports create
// duplicates today.
func Variants(raw string) []string {
	normalized := Normalize(raw)
	if normalized == "" {
		return nil
	}
	if strings.Contains(normalized, "@") {
		return []string{normalized}
	}

	out := []string{normalized, "+" + normalized}

	// The caller may have passed a number that is already stored verbatim in
	// some other shape; include it when it differs from both forms.
	trimmed := strings.TrimSpace(raw)
	if trimmed != normalized && trimmed != "+"+normalized {
		out = append(out, trimmed)
	}
	return out
}

// SameNumber reports whether two raw numbers refer to the same subscriber.
func SameNumber(a, b string) bool {
	na, nb := Normalize(a), Normalize(b)
	return na != "" && na == nb
}

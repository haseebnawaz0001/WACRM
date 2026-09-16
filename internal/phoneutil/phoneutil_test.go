package phoneutil_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/phoneutil"
	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "923211234567", "923211234567"},
		{"leading plus", "+923211234567", "923211234567"},
		{"spaces and dashes", "+92 321-123 4567", "923211234567"},
		{"parentheses", "+1 (555) 123-4567", "15551234567"},
		{"dots", "1.555.123.4567", "15551234567"},
		{"surrounding whitespace", "  +923211234567  ", "923211234567"},
		{"empty", "", ""},
		{"only formatting", "()- ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, phoneutil.Normalize(tc.in))
		})
	}
}

// Group JIDs are WhatsApp identifiers, not phone numbers. Stripping their
// non-digits would produce a value that matches nothing.
func TestNormalize_LeavesGroupJIDsIntact(t *testing.T) {
	jid := "120363422675615917@g.us"
	assert.Equal(t, jid, phoneutil.Normalize(jid))
	assert.Equal(t, []string{jid}, phoneutil.Variants(jid))
}

func TestNormalizeWithCountry(t *testing.T) {
	cases := []struct {
		name, in, cc, want string
	}{
		{"local zero prefix expanded", "03211234567", "92", "923211234567"},
		{"already international", "923211234567", "92", "923211234567"},
		{"international with plus", "+923211234567", "92", "923211234567"},
		{"no country code given leaves zero", "03211234567", "", "03211234567"},
		{"country code with plus", "03211234567", "+92", "923211234567"},
		{"multiple leading zeros collapse", "0003211234567", "92", "923211234567"},
		{"empty input", "", "92", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, phoneutil.NormalizeWithCountry(tc.in, tc.cc))
		})
	}
}

// A number without a leading zero must not be prefixed: doing so would turn a
// valid foreign number into a different one.
func TestNormalizeWithCountry_DoesNotPrefixArbitraryNumbers(t *testing.T) {
	assert.Equal(t, "15551234567", phoneutil.NormalizeWithCountry("+1 555 123 4567", "92"))
	assert.Equal(t, "441234567890", phoneutil.NormalizeWithCountry("441234567890", "92"))
}

// Rows written before normalisation existed may be stored with or without a
// "+", so a lookup has to try both or it will create a duplicate.
func TestVariants_CoversBothStoredForms(t *testing.T) {
	variants := phoneutil.Variants("+92 321 1234567")
	assert.Contains(t, variants, "923211234567")
	assert.Contains(t, variants, "+923211234567")
	assert.Equal(t, "923211234567", variants[0], "the canonical form comes first")
}

func TestVariants_EmptyForBlankInput(t *testing.T) {
	assert.Nil(t, phoneutil.Variants(""))
	assert.Nil(t, phoneutil.Variants("  "))
}

func TestSameNumber(t *testing.T) {
	assert.True(t, phoneutil.SameNumber("+92 321 1234567", "923211234567"))
	assert.True(t, phoneutil.SameNumber("(555) 123-4567", "5551234567"))
	assert.False(t, phoneutil.SameNumber("923211234567", "923211234568"))
	assert.False(t, phoneutil.SameNumber("", ""), "two blanks are not the same number")
}

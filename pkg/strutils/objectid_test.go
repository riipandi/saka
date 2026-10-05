package strutils_test

import (
	"regexp"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/pkg/strutils"
)

// testPrefix is the stand-in for the prefix type every module defines: a
// struct carrying one prefix string, bound to the identifier at compile time.
type testPrefix struct{}

// Prefix reports the TypeID prefix.
func (testPrefix) Prefix() string { return "tst" }

// testID is the typed identifier the helpers are generic over.
type testID = typeid.TypeID[testPrefix]

func TestEncodeIDCarriesTheTypePrefix(t *testing.T) {
	// The prefix the wire form carries is the one the type names, not a
	// value the caller passes: two helpers over two prefixes cannot produce
	// each other's wire form.
	raw := uuid.MustParse("01900000-0000-7000-8000-000000000000")

	id, err := strutils.EncodeID[testID](raw)
	require.NoError(t, err)

	assert.Equal(t, "tst", id.Prefix())
	assert.True(t, regexp.MustCompile(`^tst_[0-9a-z]{26}$`).MatchString(id.String()))
}

func TestEncodeIDIsDeterministicAndRoundTrips(t *testing.T) {
	// The same UUID encodes to the same wire form, and the wire form parses
	// back to the bytes the column stores.
	raw := uuid.MustParse("01900000-0000-7000-8000-000000000000")

	first, err := strutils.EncodeID[testID](raw)
	require.NoError(t, err)
	second, err := strutils.EncodeID[testID](raw)
	require.NoError(t, err)
	assert.Equal(t, first.String(), second.String())

	parsed, err := strutils.ParseID[testID](first.String())
	require.NoError(t, err)
	assert.Equal(t, raw, strutils.ToUUID(parsed))
}

func TestParseIDRefusesAForeignPrefix(t *testing.T) {
	// A wire form that names another kind of object parses as that kind, not
	// as this one: the prefix is the type, so the boundary refuses it.
	_, err := strutils.ParseID[testID]("usr_00000000000000000000000000")
	assert.Error(t, err)

	_, err = strutils.ParseID[testID]("01900000-0000-7000-8000-000000000000")
	assert.Error(t, err, "a bare UUID carries no prefix at all")
}

func TestUUIDFromWireAnswersNilBesideTheError(t *testing.T) {
	// A malformed identifier answers the zero UUID beside the error, so a
	// caller that only checks the error never stores a half-parsed key.
	parsed, err := strutils.UUIDFromWire[testID]("usr_00000000000000000000000000")
	assert.Error(t, err)
	assert.Equal(t, uuid.Nil(), parsed)
}

func TestFormatIDMatchesTheEncodedString(t *testing.T) {
	// FormatID is the render that cannot fail: rows read from the database
	// always carry a valid UUID, so it answers the same string EncodeID does.
	raw := uuid.MustParse("01900000-0000-7000-8000-000000000000")

	id, err := strutils.EncodeID[testID](raw)
	require.NoError(t, err)

	assert.Equal(t, id.String(), strutils.FormatID[testID](raw))
}

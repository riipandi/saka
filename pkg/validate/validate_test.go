package validate_test

import (
	"io"
	"strings"
	"testing"

	"github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/pkg/validate"
)

type createReq struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

func (r createReq) Validate() error {
	return validation.ValidateStruct(&r,
		validation.Field(&r.Username, validation.Required, validation.Length(3, 32)),
		validation.Field(&r.Email, is.Email),
	)
}

func TestRequestDecodesAndValidates(t *testing.T) {
	var req createReq
	require.NoError(t, validate.Request(strings.NewReader(
		`{"username":"john","email":"john@example.com"}`), &req))
	assert.Equal(t, "john", req.Username)
}

func TestRequestRejectsMalformedJSON(t *testing.T) {
	var req createReq
	err := validate.Request(strings.NewReader(`{invalid`), &req)

	require.Error(t, err)
	assert.False(t, validate.IsValidationError(err))
	assert.Equal(t, []validate.FieldError{{Field: "body", Message: "malformed JSON"}},
		validate.FieldErrors(err))
}

func TestRequestCollectsFieldErrors(t *testing.T) {
	var req createReq
	err := validate.Request(strings.NewReader(
		`{"username":"ab","email":"nope"}`), &req)

	require.Error(t, err)
	require.True(t, validate.IsValidationError(err))

	got := validate.FieldErrors(err)
	require.Len(t, got, 2)
	fields := map[string]string{}
	for _, fe := range got {
		fields[fe.Field] = fe.Message
	}
	assert.Contains(t, fields["username"], "the length must be between 3 and 32")
	assert.Contains(t, fields["email"], "must be a valid email address")
}

func TestRequestWithoutValidateContract(t *testing.T) {
	var raw map[string]string
	require.NoError(t, validate.Request(strings.NewReader(`{"a":"b"}`), &raw))
	assert.Equal(t, "b", raw["a"])
}

func TestFieldErrorsNil(t *testing.T) {
	assert.Nil(t, validate.FieldErrors(nil))
	var _ io.Reader // keep io import if assertions change
}

// TestFieldErrorsReportsDomainSentinels pins that a non-ozzo validation
// error keeps its own message: a body-rule failure such as an invalid
// username must not be misreported as a malformed JSON body, which
// sends the caller looking for a syntax problem that does not exist.
func TestFieldErrorsReportsDomainSentinels(t *testing.T) {
	err := errInvalidUsername{}
	got := validate.FieldErrors(err)
	require.Len(t, got, 1)
	assert.Equal(t, "body", got[0].Field)
	assert.Contains(t, got[0].Message, "username")
	assert.NotContains(t, got[0].Message, "malformed JSON")
}

type errInvalidUsername struct{}

func (errInvalidUsername) Error() string {
	return "username must be 3-32 characters: letters, digits, underscores"
}

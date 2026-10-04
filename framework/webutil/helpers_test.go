package webutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/webutil"
)

// The kit is exercised over hand-built requests and a scratch router, the way
// any binary would: no container, no configuration, no middleware chain
// (decision 16).

// kitInput is the body the decode tests post. It carries its rules the way
// the kit reads them: code-first, through Validate (decision: ozzo).
type kitInput struct {
	Name string `json:"name"`
}

// Validate refuses an empty name, the rule an invalid-body test exercises.
func (in kitInput) Validate() error {
	return validation.ValidateStruct(&in,
		validation.Field(&in.Name, validation.Required))
}

func TestPathParamAndQueryReadTheRequest(t *testing.T) {
	router := chi.NewRouter()
	var path, query string
	router.Get("/api/widgets/{id}", func(w http.ResponseWriter, r *http.Request) {
		path = webutil.PathParam(r, "id")
		query = webutil.Query(r, "expand")
		w.WriteHeader(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/api/widgets/wdg_1?expand=roles", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)

	assert.Equal(t, "wdg_1", path)
	assert.Equal(t, "roles", query)
}

func TestDecodeAcceptsAValidBodyAndAnswersNothing(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/widgets", strings.NewReader(`{"name": "gryffindor"}`))

	var in kitInput
	ok := webutil.Decode(recorder, request, &in)

	assert.True(t, ok, "a valid body decodes and validates")
	assert.Equal(t, "gryffindor", in.Name)
	assert.Equal(t, http.StatusOK, recorder.Code, "the happy path writes no envelope")
}

func TestDecodeWritesTheEnvelopeForAnInvalidBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/widgets", strings.NewReader(`{}`))

	var in kitInput
	ok := webutil.Decode(recorder, request, &in)

	assert.False(t, ok, "an invalid body is answered by the kit itself")
	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)

	var envelope struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Error   []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&envelope))
	assert.Equal(t, "error", envelope.Status)
	assert.Equal(t, "name", envelope.Error[0].Field)
}

func TestDecodeWritesTheEnvelopeForAnUnparsableBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/widgets", strings.NewReader(`{not json`))

	var in kitInput
	ok := webutil.Decode(recorder, request, &in)

	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "error")
}

func TestPrincipalReadsTheRequestContext(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/widgets", nil)
	_, ok := webutil.Principal(request)
	assert.False(t, ok, "a request with no credential answers no principal")
}

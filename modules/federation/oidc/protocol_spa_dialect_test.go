package oidc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dialectRequest(header string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/oidc/authorize/some-session", nil)
	if header != "" {
		r.Header.Set(spaDialectHeader, header)
	}
	return r
}

func redirectingHandler(status int, location, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, dialectRequest(""), location, status)
		if body != "" {
			_, _ = io.WriteString(w, body)
		}
	})
}

func TestTheDialectAnswersARedirectAsItsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	spaDialect(redirectingHandler(http.StatusFound, "https://rp.example/cb?code=abc", "<html></html>")).ServeHTTP(rec, dialectRequest(spaDialectValue))

	require.Equal(t, http.StatusOK, rec.Code, "the redirect is answered, not passed through")
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Empty(t, rec.Header().Get("Location"), "the raw redirect must not leak beside the JSON")
	var answer map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	assert.Equal(t, "https://rp.example/cb?code=abc", answer["redirect"])
}

func TestTheDialectLeavesAHeaderlessRedirectAlone(t *testing.T) {
	rec := httptest.NewRecorder()
	spaDialect(redirectingHandler(http.StatusFound, "https://rp.example/cb?code=abc", "")).ServeHTTP(rec, dialectRequest(""))

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "https://rp.example/cb?code=abc", rec.Header().Get("Location"))
}

func TestTheDialectLeavesAWrongHeaderValueAlone(t *testing.T) {
	rec := httptest.NewRecorder()
	spaDialect(redirectingHandler(http.StatusFound, "https://rp.example/cb", "")).ServeHTTP(rec, dialectRequest("browser"))

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "https://rp.example/cb", rec.Header().Get("Location"))
}

func TestTheDialectPassesANonRedirectAnswerThrough(t *testing.T) {
	document := []byte(`{"interaction_id":"sess_1","consent_required":true}`)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document)
	})

	withHeader := httptest.NewRecorder()
	spaDialect(handler).ServeHTTP(withHeader, dialectRequest(spaDialectValue))
	assert.Equal(t, http.StatusOK, withHeader.Code)
	assert.JSONEq(t, string(document), withHeader.Body.String())
}

func TestTheDialectPassesARefusalThrough(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid request", http.StatusBadRequest)
	})

	rec := httptest.NewRecorder()
	spaDialect(handler).ServeHTTP(rec, dialectRequest(spaDialectValue))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARedirectWithoutALocationIsNoDialectAnswer(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusFound)
	})

	rec := httptest.NewRecorder()
	spaDialect(handler).ServeHTTP(rec, dialectRequest(spaDialectValue))
	assert.Equal(t, http.StatusFound, rec.Code, "a redirect that names no target is not the dialect's to answer")
}

package transport_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRPCRefusesTheEngineProceduresWithoutAToken proves the guard on the
// queue and scheduler surface: an anonymous caller never reaches the
// handler, the same 401 every administrative procedure answers.
func TestRPCRefusesTheEngineProceduresWithoutAToken(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	for _, procedure := range []string{
		"/tango.system.v1.QueueService/ListQueues",
		"/tango.system.v1.QueueService/ListTasks",
		"/tango.system.v1.SchedulerService/ListJobs",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, rpcRequest(t, procedure, "{}"))
		require.Equal(t, http.StatusUnauthorized, rec.Code, procedure)
	}
}

// TestRPCQueueProceduresAnswerWithoutTheEngine proves the wiring answer: an
// administrator reaches the handler, and a router composed without the
// queue client says the engine is unavailable rather than pretending the
// queue is empty — the state a bare test router is in.
func TestRPCQueueProceduresAnswerWithoutTheEngine(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	req := rpcRequest(t, "/tango.system.v1.QueueService/ListQueues", "{}")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body.Code)
	assert.Contains(t, body.Message, "queue engine")
}

// TestRPCSchedulerProceduresAnswerWithoutTheEngine is the scheduler's half
// of the wiring answer.
func TestRPCSchedulerProceduresAnswerWithoutTheEngine(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	req := rpcRequest(t, "/tango.system.v1.SchedulerService/ListJobs", "{}")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())

	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body.Code)
}

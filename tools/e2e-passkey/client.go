package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// rpcClient speaks the ConnectRPC surface: one JSON object per procedure
// call, the bearer token and the extra headers a guarded call carries.
type rpcClient struct {
	base string
	http *http.Client
}

func newClient(baseURL string, insecure bool) *rpcClient {
	client := &rpcClient{base: strings.TrimSuffix(baseURL, "/"), http: &http.Client{}}
	if insecure {
		client.http.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // the self-signed development proxy
	}
	return client
}

const rpcPath = "/rpc/"

func (c *rpcClient) call(procedure, body, accessToken string, headers ...string) (int, map[string]any) {
	request, err := http.NewRequest(http.MethodPost, c.base+rpcPath+procedure, bytes.NewBufferString(body))
	if err != nil {
		fail("the request does not build: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}

	response, err := c.http.Do(request)
	if err != nil {
		fail("%s is unreachable: %v", procedure, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)

	var document map[string]any
	_ = json.Unmarshal(raw, &document)
	return response.StatusCode, document
}

func (c *rpcClient) mustRPC(procedure string, body map[string]any, accessToken string, headers ...string) map[string]any {
	raw, _ := json.Marshal(body)
	code, document := c.call(procedure, string(raw), accessToken, headers...)
	if code >= 300 {
		fail("%s answered %d: %s", procedure, code, mustJSON(document))
	}
	return document
}

func (c *rpcClient) tryRPC(procedure string, body map[string]any, accessToken string, headers ...string) int {
	raw, _ := json.Marshal(body)
	code, _ := c.call(procedure, string(raw), accessToken, headers...)
	return code
}

func mustJSON(document map[string]any) string {
	raw, err := json.Marshal(document)
	if err != nil {
		return fmt.Sprintf("%v", document)
	}
	return string(raw)
}

package backends

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/makibytes/xmc/log"
)

// mgmtHTTPClient is a shared HTTP client for broker management APIs
// with a 10-second timeout. All management HTTP interactions should go
// through MgmtGet/MgmtPost/MgmtPut/MgmtDelete rather than creating
// separate clients.
var mgmtHTTPClient = &http.Client{Timeout: 10 * time.Second}

// MgmtGet performs a GET request against a management API endpoint.
func MgmtGet(url, user, password string) ([]byte, error) {
	return mgmtRequest("GET", url, nil, user, password, 200, 200)
}

// MgmtPost performs a POST request with a JSON body against a management
// API endpoint. StatusOK is the expected success status code (usually 200,
// but some APIs return 201 or 204).
func MgmtPost(url string, body []byte, user, password string) ([]byte, error) {
	return mgmtRequest("POST", url, body, user, password, 200, 201, 204)
}

// MgmtPut performs a PUT request with a JSON body against a management API
// endpoint.
func MgmtPut(url string, body []byte, user, password string) error {
	_, err := mgmtRequest("PUT", url, body, user, password, 200, 201, 204)
	return err
}

// MgmtDelete performs a DELETE request against a management API endpoint.
// StatusOK is the expected success status code.
func MgmtDelete(url, user, password string) ([]byte, error) {
	return mgmtRequest("DELETE", url, nil, user, password, 200, 204)
}

// mgmtRequest performs an HTTP request with Basic Auth and returns the
// response body. The okStatuses variadic specifies which HTTP status codes
// are considered success; if none are provided, only 200 is accepted.
func mgmtRequest(method, url string, body []byte, user, password string, okStatuses ...int) ([]byte, error) {
	log.Verbose("%s %s", method, url)

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := mgmtHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if !isOKStatus(resp.StatusCode, okStatuses) {
		return nil, fmt.Errorf("management API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// isOKStatus reports whether status matches one of the acceptable status
// codes. If okStatuses is empty 200 is the only acceptable code.
func isOKStatus(status int, okStatuses []int) bool {
	if len(okStatuses) == 0 {
		return status == 200
	}
	for _, s := range okStatuses {
		if status == s {
			return true
		}
	}
	return false
}

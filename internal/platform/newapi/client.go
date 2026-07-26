package newapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

// ErrInsufficientPrivilege reports that the configured credential exists but
// lacks the role the endpoint requires. Discovery mode probing relies on this
// being distinguishable from a transport failure: a common-user token hitting an
// Admin endpoint must fall back to token mode, not report the upstream as down.
var ErrInsufficientPrivilege = errors.New("New API credential lacks the required privilege")

// transport centralizes New API management calls so channel discovery, token
// discovery and group discovery share one timeout, header and error policy.
// Credentials never appear in returned errors.
type transport struct {
	baseURL        string
	accessToken    string
	userID         int
	requestTimeout time.Duration
	client         *http.Client
}

type request struct {
	method string
	path   string
	query  string
	// proof is New API's Root security proof. It is only set for channel key
	// reads and is never logged or persisted.
	proof string
	body  any
}

func (t transport) get(ctx context.Context, path, query string, destination any) error {
	return t.do(ctx, request{method: http.MethodGet, path: path, query: query}, destination)
}

func (t transport) do(ctx context.Context, spec request, destination any) error {
	var encoded []byte
	var err error
	if spec.body != nil {
		encoded, err = json.Marshal(spec.body)
		if err != nil {
			return errors.New("failed to encode New API request")
		}
		defer wipeTargetBytes(encoded)
	}

	requestCtx, cancel := context.WithTimeout(ctx, t.requestTimeout)
	defer cancel()

	requestURL := t.baseURL + spec.path
	if spec.query != "" {
		requestURL += "?" + spec.query
	}
	httpRequest, err := http.NewRequestWithContext(requestCtx, spec.method, requestURL, bytes.NewReader(encoded))
	if err != nil {
		return errors.New("failed to create New API request")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+t.accessToken)
	httpRequest.Header.Set("Accept", "application/json")
	if spec.body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if t.userID > 0 {
		httpRequest.Header.Set("New-Api-User", strconv.Itoa(t.userID))
	}
	if spec.proof != "" {
		httpRequest.Header.Set("X-Security-Proof", spec.proof)
	}

	response, err := t.client.Do(httpRequest)
	if err != nil {
		if requestCtx.Err() != nil {
			return requestCtx.Err()
		}
		return errors.New("New API request failed")
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		return statusError(response.StatusCode)
	}

	encodedResponse, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if requestCtx.Err() != nil {
			return requestCtx.Err()
		}
		return errors.New("New API returned an invalid response")
	}
	defer wipeTargetBytes(encodedResponse)
	if len(encodedResponse) > maxResponseBytes {
		return errors.New("New API returned an invalid response")
	}
	if err := json.Unmarshal(encodedResponse, destination); err != nil {
		if requestCtx.Err() != nil {
			return requestCtx.Err()
		}
		return errors.New("New API returned an invalid response")
	}
	return nil
}

// statusError maps the states an operator must act on differently: rate
// limiting is retryable, missing privilege is not.
func statusError(status int) error {
	switch status {
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: New API rate limit reached", platform.ErrRateLimited)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: status %d", ErrInsufficientPrivilege, status)
	default:
		return fmt.Errorf("New API request returned status %d", status)
	}
}

func wipeTargetBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

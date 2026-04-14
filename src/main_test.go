package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"connection refused", errors.New("connection refused"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"timeout", errors.New("dial tcp: i/o timeout"), true},
		{"no such host", errors.New("dial tcp: lookup example.com: no such host"), true},
		{"context deadline", context.DeadlineExceeded, true},
		{"context cancelled", context.Canceled, true},
		{"server error 500", errors.New("server returned 500: internal server error"), false},
		{"not found 404", errors.New("API error 404: not found"), false},
		{"auth failed", errors.New("API error 401: unauthorized"), false},
		{"parse error", errors.New("parsing user: unexpected end of json"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestIsRetryableErrorCaseInsensitive(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		expected bool
	}{
		{"uppercase timeout", "DIAL TCP: I/O TIMEOUT", true},
		{"mixed case connection refused", "Connection Refused", true},
		{"uppercase context", "CONTEXT DEADLINE EXCEEDED", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(errors.New(tt.err))
			if result != tt.expected {
				t.Errorf("isRetryableError(%q) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestGetUserWithRetrySuccess(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		if path == "/systemusers/user123" {
			return []byte(`{"_id":"user123","username":"testuser","email":"test@example.com","firstname":"Test","lastname":"User","activated":true,"suspended":false}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	user, err := jc.GetUserWithRetry(ctx, "user123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if user.Username != "testuser" {
		t.Errorf("expected username 'testuser', got %q", user.Username)
	}
	if user.Email != "test@example.com" {
		t.Errorf("expected email 'test@example.com', got %q", user.Email)
	}
}

func TestGetUserWithRetryRetriesOnTimeout(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	callCount := 0
	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		callCount++
		if path == "/systemusers/user123" {
			if callCount < 3 {
				return nil, errors.New("dial tcp: i/o timeout")
			}
			return []byte(`{"_id":"user123","username":"testuser","email":"test@example.com","firstname":"Test","lastname":"User","activated":true,"suspended":false}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	user, err := jc.GetUserWithRetry(ctx, "user123")

	if err != nil {
		t.Fatalf("expected no error after retries, got %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
	if user.Username != "testuser" {
		t.Errorf("expected username 'testuser', got %q", user.Username)
	}
}

func TestGetUserWithRetryFailsAfterMaxRetries(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	callCount := 0
	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		callCount++
		if path == "/systemusers/user123" {
			return nil, errors.New("dial tcp: i/o timeout")
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	_, err := jc.GetUserWithRetry(ctx, "user123")

	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (1 initial + 2 retries), got %d", callCount)
	}
	if !strings.Contains(err.Error(), "failed after 3 retries") {
		t.Errorf("expected error message to mention retries, got %v", err)
	}
}

func TestGetUserWithRetryNonRetryableError(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	callCount := 0
	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		callCount++
		if path == "/systemusers/user123" {
			return nil, errors.New("API error 404: not found")
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	_, err := jc.GetUserWithRetry(ctx, "user123")

	if err == nil {
		t.Fatal("expected error for non-retryable error")
	}
	if callCount != 1 {
		t.Errorf("expected only 1 call (no retries for non-retryable), got %d", callCount)
	}
	if !strings.Contains(err.Error(), "non-retryable") {
		t.Errorf("expected error message to mention non-retryable, got %v", err)
	}
}

func TestGetUserWithRetryContextCancelled(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	callCount := 0
	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		callCount++
		if path == "/systemusers/user123" {
			return nil, errors.New("dial tcp: i/o timeout")
		}
		return nil, errors.New("unexpected path")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := jc.GetUserWithRetry(ctx, "user123")

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("expected error message to mention context cancelled, got %v", err)
	}
}

func TestGetUserWithRetryBackoffTiming(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	startTimes := make([]time.Time, 0)
	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		startTimes = append(startTimes, time.Now())
		if path == "/systemusers/user123" {
			return nil, errors.New("dial tcp: i/o timeout")
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	_, _ = jc.GetUserWithRetry(ctx, "user123")

	if len(startTimes) < 3 {
		t.Fatalf("expected at least 3 calls, got %d", len(startTimes))
	}

	delay1 := startTimes[1].Sub(startTimes[0])
	delay2 := startTimes[2].Sub(startTimes[1])

	if delay1 < 400*time.Millisecond || delay1 > 700*time.Millisecond {
		t.Errorf("first delay expected ~500ms, got %v", delay1)
	}
	if delay2 < 800*time.Millisecond || delay2 > 1200*time.Millisecond {
		t.Errorf("second delay expected ~1000ms, got %v", delay2)
	}
}

func TestGetGroupMembers_UtenteRimossoDalGruppo(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		if path == "/v2/usergroups/group123/members" {
			return []byte(`[
				{"to": {"id": "user1", "type": "user"}},
				{"to": {"id": "user2", "type": "user"}}
			]`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	memberIDs, err := jc.GetGroupMembers(ctx, "group123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(memberIDs) != 2 {
		t.Errorf("expected 2 members, got %d", len(memberIDs))
	}
	if memberIDs[0] != "user1" || memberIDs[1] != "user2" {
		t.Errorf("expected [user1 user2], got %v", memberIDs)
	}
}

func TestGetGroupMembers_GruppoVuoto(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		if path == "/v2/usergroups/group123/members" {
			return []byte(`[]`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	memberIDs, err := jc.GetGroupMembers(ctx, "group123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(memberIDs) != 0 {
		t.Errorf("expected 0 members, got %d", len(memberIDs))
	}
}

func TestGetGroupMembers_IgnoraNonUser(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		if path == "/v2/usergroups/group123/members" {
			return []byte(`[
				{"to": {"id": "user1", "type": "user"}},
				{"to": {"id": "device1", "type": "device"}},
				{"to": {"id": "user2", "type": "user"}}
			]`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	memberIDs, err := jc.GetGroupMembers(ctx, "group123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(memberIDs) != 2 {
		t.Errorf("expected 2 user members (ignoring device), got %d", len(memberIDs))
	}
}

func TestGetUser_UtenteCancellato(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		if path == "/systemusers/user123" {
			return nil, errors.New("API error 404: not found")
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	_, err := jc.GetUser(ctx, "user123")

	if err == nil {
		t.Fatal("expected error for 404 not found")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to contain 404, got %v", err)
	}
}

func TestGetUser_UtenteSospeso(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		if path == "/systemusers/user123" {
			return []byte(`{"_id":"user123","username":"testuser","email":"test@example.com","firstname":"Test","lastname":"User","activated":true,"suspended":true}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	user, err := jc.GetUser(ctx, "user123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if user.Suspended != true {
		t.Errorf("expected suspended=true, got %v", user.Suspended)
	}
}

func TestGetUser_UtenteDisattivato(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		if path == "/systemusers/user123" {
			return []byte(`{"_id":"user123","username":"testuser","email":"test@example.com","firstname":"Test","lastname":"User","activated":false,"suspended":false}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	user, err := jc.GetUser(ctx, "user123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if user.Activated != false {
		t.Errorf("expected activated=false, got %v", user.Activated)
	}
}

func TestGetUserWithRetry_404NonRetryable(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "http://localhost:9999",
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "valid-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	callCount := 0
	jc.execute = func(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
		callCount++
		if path == "/systemusers/user123" {
			return nil, errors.New("API error 404: not found")
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	_, err := jc.GetUserWithRetry(ctx, "user123")

	if err == nil {
		t.Fatal("expected error for non-retryable 404")
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 call (no retries for 404), got %d", callCount)
	}
	if !strings.Contains(err.Error(), "non-retryable") {
		t.Errorf("expected non-retryable error, got %v", err)
	}
}

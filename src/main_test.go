package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestStringSliceEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected bool
	}{
		{"both nil", nil, nil, true},
		{"both empty", []string{}, []string{}, true},
		{"both equal", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, false},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different content", []string{"a", "b"}, []string{"a", "c"}, false},
		{"single element equal", []string{"a"}, []string{"a"}, true},
		{"single element different", []string{"a"}, []string{"b"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringSliceEqual(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("stringSliceEqual(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestMergeRoles(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		defaults []string
		expected []string
	}{
		{"both empty", []string{}, []string{}, []string{}},
		{"only existing", []string{"role1", "role2"}, []string{}, []string{"role1", "role2"}},
		{"only defaults", []string{}, []string{"role1", "role2"}, []string{"role1", "role2"}},
		{"existing and defaults no overlap", []string{"role1"}, []string{"role2"}, []string{"role1", "role2"}},
		{"existing and defaults with overlap", []string{"role1", "role2"}, []string{"role2", "role3"}, []string{"role1", "role2", "role3"}},
		{"all defaults already in existing", []string{"role1", "role2"}, []string{"role1", "role2"}, []string{"role1", "role2"}},
		{"nil existing", nil, []string{"role1"}, []string{"role1"}},
		{"nil defaults", []string{"role1"}, nil, []string{"role1"}},
		{"duplicate in existing", []string{"role1", "role1"}, []string{"role2"}, []string{"role1", "role1", "role2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeRoles(tt.existing, tt.defaults)
			if !sliceEqualUnordered(result, tt.expected) {
				t.Errorf("mergeRoles(%v, %v) = %v, want %v", tt.existing, tt.defaults, result, tt.expected)
			}
		})
	}
}

func sliceEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int)
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

func TestFindGroupByName_GruppoTrovato(t *testing.T) {
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
		if path == "/v2/usergroups?filter=name:eq:TestGroup&limit=1" {
			return []byte(`[{"id":"group123","name":"TestGroup"}]`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	groupID, err := jc.FindGroupByName(ctx, "TestGroup")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if groupID != "group123" {
		t.Errorf("expected group123, got %q", groupID)
	}
}

func TestFindGroupByName_GruppoNonTrovato(t *testing.T) {
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
		if path == "/v2/usergroups?filter=name:eq:NonExistent&limit=1" {
			return []byte(`[]`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	_, err := jc.FindGroupByName(ctx, "NonExistent")

	if err == nil {
		t.Fatal("expected error for not found group")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %v", err)
	}
}

func TestFindGroupByName_CaseInsensitive(t *testing.T) {
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
		if strings.Contains(path, "filter=name:eq:") {
			return []byte(`[{"id":"group123","name":"testgroup"}]`), nil
		}
		return nil, errors.New("unexpected path")
	}

	ctx := context.Background()
	groupID, err := jc.FindGroupByName(ctx, "TESTGROUP")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if groupID != "group123" {
		t.Errorf("expected group123, got %q", groupID)
	}
}

func TestAuthenticate_TokenValido(t *testing.T) {
	jc := &JumpCloudClient{
		clientID:     "test",
		clientSecret: "test",
		orgID:        "test",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		authURL:      "http://localhost:9999/token",
	}

	jc.accessToken = "existing-token"
	jc.tokenExpiry = time.Now().Add(1 * time.Hour)

	ctx := context.Background()
	err := jc.authenticate(ctx)

	if err != nil {
		t.Errorf("expected no error when token is valid, got %v", err)
	}
	if jc.accessToken != "existing-token" {
		t.Errorf("expected token to not be refreshed")
	}
}

func TestFindGroupByName_WithRealHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" && r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
			return
		}
		if r.URL.Path == "/api/v2/usergroups" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"id":"group123","name":"TestGroup"}]`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	jc := NewJumpCloudClient("test-id", "test-secret", "test-org")
	jc.baseURL = server.URL + "/api"
	jc.authURL = server.URL + "/oauth2/token"

	ctx := context.Background()
	groupID, err := jc.FindGroupByName(ctx, "TestGroup")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if groupID != "group123" {
		t.Errorf("expected group123, got %q", groupID)
	}
}

func TestGetGroupMembers_WithRealHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" && r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
			return
		}
		if r.URL.Path == "/api/v2/usergroups/group123/members" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"to":{"id":"user1","type":"user"}},{"to":{"id":"user2","type":"user"}}]`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	jc := NewJumpCloudClient("test-id", "test-secret", "test-org")
	jc.baseURL = server.URL + "/api"
	jc.authURL = server.URL + "/oauth2/token"

	ctx := context.Background()
	members, err := jc.GetGroupMembers(ctx, "group123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(members) != 2 {
		t.Errorf("expected 2 members, got %d", len(members))
	}
	if members[0] != "user1" || members[1] != "user2" {
		t.Errorf("expected [user1 user2], got %v", members)
	}
}

func TestGetUser_WithRealHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" && r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
			return
		}
		if r.URL.Path == "/api/systemusers/user123" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"_id":"user123","username":"testuser","email":"test@example.com","firstname":"Test","lastname":"User","activated":true,"suspended":false}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	jc := NewJumpCloudClient("test-id", "test-secret", "test-org")
	jc.baseURL = server.URL + "/api"
	jc.authURL = server.URL + "/oauth2/token"

	ctx := context.Background()
	user, err := jc.GetUser(ctx, "user123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if user.Username != "testuser" {
		t.Errorf("expected testuser, got %q", user.Username)
	}
	if user.Email != "test@example.com" {
		t.Errorf("expected test@example.com, got %q", user.Email)
	}
}

func TestFindGroupByName_GroupNotFound_WithRealHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" && r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
			return
		}
		if r.URL.Path == "/api/v2/usergroups" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	jc := NewJumpCloudClient("test-id", "test-secret", "test-org")
	jc.baseURL = server.URL + "/api"
	jc.authURL = server.URL + "/oauth2/token"

	ctx := context.Background()
	_, err := jc.FindGroupByName(ctx, "NonExistent")

	if err == nil {
		t.Fatal("expected error for not found group")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %v", err)
	}
}

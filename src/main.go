package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/types"
)

// --- JumpCloud API types ---

type JCMember struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type JCGroupMembership struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Members []JCMember `json:"members,omitempty"`
}

type JCUser struct {
	ID        string `json:"_id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstname"`
	LastName  string `json:"lastname"`
	Activated bool   `json:"activated"`
	Suspended bool   `json:"suspended"`
}

type JCListResponse struct {
	Results    []json.RawMessage `json:"results"`
	TotalCount int               `json:"totalCount"`
}

// --- Config ---

type Config struct {
	JumpCloudClientID     string
	JumpCloudClientSecret string
	JumpCloudOrgID        string
	JumpCloudGroupName    string
	TeleportAddr          string
	TeleportIdentity      string
	TeleportRoles         []string
	DryRun                bool
}

func loadConfig() (*Config, error) {
	clientID := os.Getenv("JUMPCLOUD_CLIENT_ID")
	if clientID == "" {
		return nil, fmt.Errorf("JUMPCLOUD_CLIENT_ID is required")
	}
	clientSecret := os.Getenv("JUMPCLOUD_CLIENT_SECRET")
	if clientSecret == "" {
		return nil, fmt.Errorf("JUMPCLOUD_CLIENT_SECRET is required")
	}
	orgID := os.Getenv("JUMPCLOUD_ORG_ID")
	if orgID == "" {
		return nil, fmt.Errorf("JUMPCLOUD_ORG_ID is required")
	}
	groupName := os.Getenv("TELEPORT_SYNC_GROUP")
	if groupName == "" {
		return nil, fmt.Errorf("TELEPORT_SYNC_GROUP is required")
	}
	addr := os.Getenv("TELEPORT_ADDR")
	if addr == "" {
		addr = "teleport-auth.teleport.svc.cluster.local:3025"
	}
	identity := os.Getenv("TELEPORT_IDENTITY_FILE")
	if identity == "" {
		identity = "/var/run/teleport/identity"
	}
	roles := os.Getenv("TELEPORT_DEFAULT_ROLES")
	if roles == "" {
		roles = "access"
	}
	dryRun := os.Getenv("DRY_RUN") == "true"

	return &Config{
		JumpCloudClientID:     clientID,
		JumpCloudClientSecret: clientSecret,
		JumpCloudOrgID:        orgID,
		JumpCloudGroupName:    groupName,
		TeleportAddr:          addr,
		TeleportIdentity:      identity,
		TeleportRoles:         strings.Split(roles, ","),
		DryRun:                dryRun,
	}, nil
}

// --- JumpCloud client ---

type JumpCloudClient struct {
	clientID     string
	clientSecret string
	orgID        string
	httpClient   *http.Client
	baseURL      string
	authURL      string
	accessToken  string
	tokenExpiry  time.Time
}

func NewJumpCloudClient(clientID, clientSecret, orgID string) *JumpCloudClient {
	return &JumpCloudClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		orgID:        orgID,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      "https://console.jumpcloud.com/api",
		authURL:      "https://auth.jumpcloud.com/oauth2/token",
	}
}

func (jc *JumpCloudClient) authenticate(ctx context.Context) error {
	if jc.accessToken != "" && time.Now().Before(jc.tokenExpiry) {
		return nil
	}

	form := strings.NewReader("grant_type=client_credentials&client_id=" + jc.clientID + "&client_secret=" + jc.clientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, jc.authURL, form)
	if err != nil {
		return fmt.Errorf("creating auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := jc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing auth request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading auth response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth failed %d: %s", resp.StatusCode, string(data))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &tokenResp); err != nil {
		return fmt.Errorf("parsing auth response: %w", err)
	}

	jc.accessToken = tokenResp.AccessToken
	jc.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	return nil
}

func (jc *JumpCloudClient) do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	if err := jc.authenticate(ctx); err != nil {
		return nil, fmt.Errorf("authenticating: %w", err)
	}

	url := jc.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jc.accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-org-id", jc.orgID)

	resp, err := jc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (jc *JumpCloudClient) FindGroupByName(ctx context.Context, name string) (string, error) {
	path := fmt.Sprintf("/v2/usergroups?filter=name:eq:%s&limit=1", name)
	data, err := jc.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("listing user groups: %w", err)
	}

	var groups []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &groups); err != nil {
		return "", fmt.Errorf("parsing groups: %w", err)
	}
	for _, g := range groups {
		if strings.EqualFold(g.Name, name) {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("group %q not found", name)
}

func (jc *JumpCloudClient) GetGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	path := fmt.Sprintf("/v2/usergroups/%s/members", groupID)
	data, err := jc.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("getting group members: %w", err)
	}

	var members []struct {
		To struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"to"`
	}
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, fmt.Errorf("parsing members: %w", err)
	}

	var userIDs []string
	for _, m := range members {
		if m.To.Type == "user" {
			userIDs = append(userIDs, m.To.ID)
		}
	}
	return userIDs, nil
}

func (jc *JumpCloudClient) GetUser(ctx context.Context, userID string) (*JCUser, error) {
	path := fmt.Sprintf("/systemusers/%s", userID)
	data, err := jc.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("getting user %s: %w", userID, err)
	}

	var user JCUser
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("parsing user: %w", err)
	}
	return &user, nil
}

// --- Sync logic ---

const managedLabel = "jumpcloud-scim-sync"

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := slog.Default()
	logger.Info("starting sync",
		"group", cfg.JumpCloudGroupName,
		"teleport_addr", cfg.TeleportAddr,
		"roles", cfg.TeleportRoles,
		"dry_run", cfg.DryRun,
	)

	jcClient := NewJumpCloudClient(cfg.JumpCloudClientID, cfg.JumpCloudClientSecret, cfg.JumpCloudOrgID)

	groupID, err := jcClient.FindGroupByName(ctx, cfg.JumpCloudGroupName)
	if err != nil {
		return fmt.Errorf("finding JumpCloud group: %w", err)
	}
	logger.Info("found JumpCloud group", "group_id", groupID, "name", cfg.JumpCloudGroupName)

	memberIDs, err := jcClient.GetGroupMembers(ctx, groupID)
	if err != nil {
		return fmt.Errorf("getting group members: %w", err)
	}
	logger.Info("found group members", "count", len(memberIDs))

	jcUsers := make(map[string]*JCUser)
	for _, uid := range memberIDs {
		user, err := jcClient.GetUser(ctx, uid)
		if err != nil {
			logger.Warn("failed to fetch user, skipping", "user_id", uid, "error", err)
			continue
		}
		if user.Suspended || !user.Activated {
			logger.Info("skipping deactivated/suspended user", "username", user.Username)
			continue
		}
		jcUsers[user.Username] = user
	}
	logger.Info("active JumpCloud users to sync", "count", len(jcUsers))

	tlsCfg := &tls.Config{}
	creds := client.LoadIdentityFile(cfg.TeleportIdentity)

	tc, err := client.New(ctx, client.Config{
		Addrs:       []string{cfg.TeleportAddr},
		Credentials: []client.Credentials{creds},
	})
	if err != nil {
		return fmt.Errorf("connecting to teleport: %w", err)
	}
	defer tc.Close()
	_ = tlsCfg

	existingUsers, err := tc.GetUsers(ctx, false)
	if err != nil {
		return fmt.Errorf("listing teleport users: %w", err)
	}

	managedUsers := make(map[string]types.User)
	allTeleportUsers := make(map[string]types.User)
	for _, u := range existingUsers {
		allTeleportUsers[u.GetName()] = u
		labels := u.GetMetadata().Labels
		if labels != nil && labels["managed-by"] == managedLabel {
			managedUsers[u.GetName()] = u
		}
	}

	synced := make(map[string]bool)
	var created, updated, deleted, skipped int

	for username, jcUser := range jcUsers {
		synced[username] = true

		existing, exists := allTeleportUsers[username]
		if exists {
			labels := existing.GetMetadata().Labels
			if labels == nil || labels["managed-by"] != managedLabel {
				logger.Info("user exists but not managed by us, skipping", "username", username)
				skipped++
				continue
			}
			needsUpdate := false
			traits := existing.GetTraits()

			desiredEmail := []string{jcUser.Email}
			if !stringSliceEqual(traits["email"], desiredEmail) {
				needsUpdate = true
			}
			desiredFullName := []string{fmt.Sprintf("%s %s", jcUser.FirstName, jcUser.LastName)}
			if !stringSliceEqual(traits["full_name"], desiredFullName) {
				needsUpdate = true
			}
			if !stringSliceEqual(existing.GetRoles(), cfg.TeleportRoles) {
				needsUpdate = true
			}

			if needsUpdate {
				if cfg.DryRun {
					logger.Info("[DRY RUN] would update user", "username", username)
				} else {
					traits["email"] = desiredEmail
					traits["full_name"] = desiredFullName
					existing.SetTraits(traits)
					existing.SetRoles(cfg.TeleportRoles)
					if _, err := tc.UpdateUser(ctx, existing); err != nil {
						logger.Error("failed to update user", "username", username, "error", err)
						continue
					}
					logger.Info("updated user", "username", username)
				}
				updated++
			}
		} else {
			if cfg.DryRun {
				logger.Info("[DRY RUN] would create user", "username", username, "email", jcUser.Email)
			} else {
				newUser, err := types.NewUser(username)
				if err != nil {
					logger.Error("failed to create user object", "username", username, "error", err)
					continue
				}
				md := newUser.GetMetadata()
				if md.Labels == nil {
					md.Labels = make(map[string]string)
				}
				md.Labels["managed-by"] = managedLabel
				md.Labels["jumpcloud-id"] = jcUser.ID
				newUser.SetMetadata(md)

				newUser.SetRoles(cfg.TeleportRoles)
				traits := map[string][]string{
					"email":     {jcUser.Email},
					"full_name": {fmt.Sprintf("%s %s", jcUser.FirstName, jcUser.LastName)},
					"logins":    {username},
				}
				newUser.SetTraits(traits)

				newUser.SetCreatedBy(types.CreatedBy{
					User: types.UserRef{Name: "jumpcloud-scim-sync"},
					Time: time.Now().UTC(),
				})

				if _, err := tc.CreateUser(ctx, newUser); err != nil {
					logger.Error("failed to create user in teleport", "username", username, "error", err)
					continue
				}

				token, err := tc.CreateResetPasswordToken(ctx,
					&proto.CreateResetPasswordTokenRequest{
						Name: username,
						Type: "invite",
					})
				if err != nil {
					logger.Warn("user created but failed to generate invite token",
						"username", username, "error", err)
				} else {
					logger.Info("created user with invite",
						"username", username,
						"invite_url", fmt.Sprintf("https://%s/web/invite/%s",
							strings.Split(cfg.TeleportAddr, ":")[0], token.GetName()),
					)
				}
			}
			created++
		}
	}

	for username, u := range managedUsers {
		if synced[username] {
			continue
		}
		if cfg.DryRun {
			logger.Info("[DRY RUN] would delete user (no longer in group)", "username", username)
		} else {
			lock, err := types.NewLock("jc-sync-"+username, types.LockSpecV2{
				Target: types.LockTarget{User: username},
				Message: fmt.Sprintf("Removed from JumpCloud group %q", cfg.JumpCloudGroupName),
			})
			if err == nil {
				if err := tc.UpsertLock(ctx, lock); err != nil {
					logger.Warn("failed to lock user before deletion", "username", username, "error", err)
				}
			}

			if err := tc.DeleteUser(ctx, username); err != nil {
				logger.Error("failed to delete user", "username", username, "error", err)
				continue
			}
			logger.Info("deleted managed user (removed from JumpCloud group)",
				"username", username, "jumpcloud_id", u.GetMetadata().Labels["jumpcloud-id"])
		}
		deleted++
	}

	logger.Info("sync complete",
		"created", created,
		"updated", updated,
		"deleted", deleted,
		"skipped", skipped,
		"total_jc_users", len(jcUsers),
	)
	return nil
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("sync failed", "error", err)
		os.Exit(1)
	}
}

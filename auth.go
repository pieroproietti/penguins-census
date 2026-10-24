package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type GitHubAuthConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserAPIURL   string
}

func (s *CensusServer) SetGitHubAuth(clientID, clientSecret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clientID == "" || clientSecret == "" {
		s.githubAuth = nil
		return
	}
	s.githubAuth = &GitHubAuthConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthURL:      "https://github.com/login/oauth/authorize",
		TokenURL:     "https://github.com/login/oauth/access_token",
		UserAPIURL:   "https://api.github.com/user",
	}
}

func (s *CensusServer) IsGitHubAuthEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.githubAuth != nil && s.githubAuth.ClientID != "" && s.githubAuth.ClientSecret != ""
}

func (s *CensusServer) handleGitHubAuth(w http.ResponseWriter, r *http.Request) {
	if !s.IsGitHubAuthEnabled() {
		http.Error(w, `{"error":"GitHub OAuth is not configured"}`, http.StatusNotImplemented)
		return
	}

	subpath := strings.TrimPrefix(r.URL.Path, "/auth/github")
	subpath = strings.TrimPrefix(subpath, "/")

	switch subpath {
	case "login":
		s.handleGitHubLogin(w, r)
	case "callback":
		s.handleGitHubCallback(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *CensusServer) handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, `{"error":"failed to generate random state"}`, http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(b)

	http.SetCookie(w, &http.Cookie{
		Name:     "eggs_oauth_state",
		Value:    state,
		Path:     "/auth/github",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	s.mu.Lock()
	authCfg := *s.githubAuth
	s.mu.Unlock()

	authURL := authCfg.AuthURL
	if authURL == "" {
		authURL = "https://github.com/login/oauth/authorize"
	}

	redirectURI := fmt.Sprintf("%s?client_id=%s&state=%s&scope=read:user",
		authURL,
		url.QueryEscape(authCfg.ClientID),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, redirectURI, http.StatusFound)
}

func (s *CensusServer) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		http.Error(w, fmt.Sprintf("GitHub OAuth error: %s (%s)", html.EscapeString(errParam), html.EscapeString(errDesc)), http.StatusBadRequest)
		return
	}

	cookie, err := r.Cookie("eggs_oauth_state")
	if err != nil || cookie.Value == "" {
		http.Error(w, "missing or expired oauth state cookie", http.StatusBadRequest)
		return
	}

	reqState := r.URL.Query().Get("state")
	if reqState == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(reqState)) != 1 {
		http.Error(w, "invalid oauth state parameter", http.StatusBadRequest)
		return
	}

	// Invalidate state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "eggs_oauth_state",
		Value:    "",
		Path:     "/auth/github",
		MaxAge:   -1,
		HttpOnly: true,
	})

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Error(w, "missing code query parameter", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	authCfg := *s.githubAuth
	s.mu.Unlock()

	tokenURL := authCfg.TokenURL
	if tokenURL == "" {
		tokenURL = "https://github.com/login/oauth/access_token"
	}

	tokenPayload, _ := json.Marshal(map[string]string{
		"client_id":     authCfg.ClientID,
		"client_secret": authCfg.ClientSecret,
		"code":          code,
	})

	tokenReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, tokenURL, bytes.NewReader(tokenPayload))
	if err != nil {
		http.Error(w, "failed to build token request", http.StatusInternalServerError)
		return
	}
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenReq.Header.Set("Accept", "application/json")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	tokenResp, err := httpClient.Do(tokenReq)
	if err != nil {
		http.Error(w, "failed to exchange token with GitHub: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer tokenResp.Body.Close()

	var tokenResult struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenResult); err != nil {
		http.Error(w, "failed to parse GitHub token response", http.StatusBadGateway)
		return
	}

	if tokenResult.AccessToken == "" {
		errMsg := tokenResult.Error
		if tokenResult.ErrorDesc != "" {
			errMsg += ": " + tokenResult.ErrorDesc
		}
		http.Error(w, "GitHub OAuth token error: "+errMsg, http.StatusUnauthorized)
		return
	}

	userURL := authCfg.UserAPIURL
	if userURL == "" {
		userURL = "https://api.github.com/user"
	}
	userReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, userURL, nil)
	if err != nil {
		http.Error(w, "failed to build user request", http.StatusInternalServerError)
		return
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResult.AccessToken)
	userReq.Header.Set("User-Agent", "penguins-census")
	userReq.Header.Set("Accept", "application/json")

	userResp, err := httpClient.Do(userReq)
	if err != nil {
		http.Error(w, "failed to fetch GitHub user: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer userResp.Body.Close()

	if userResp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(userResp.Body, 512))
		http.Error(w, fmt.Sprintf("GitHub user API returned status %d: %s", userResp.StatusCode, string(snippet)), http.StatusBadGateway)
		return
	}

	var ghUser struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Bio       string `json:"bio"`
		Location  string `json:"location"`
		Blog      string `json:"blog"`
		HTMLURL   string `json:"html_url"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&ghUser); err != nil {
		http.Error(w, "failed to parse GitHub user data", http.StatusBadGateway)
		return
	}

	cleanLogin := strings.ToLower(strings.TrimSpace(ghUser.Login))
	if cleanLogin == "" {
		http.Error(w, "GitHub login is empty", http.StatusBadRequest)
		return
	}

	authIDStr := strconv.FormatInt(ghUser.ID, 10)

	s.mu.Lock()
	existing, _ := s.getCommunityProfile(cleanLogin)
	if existing == nil {
		// Look up by auth_id in case user previously used another login
		profiles, _ := s.listCommunityProfiles()
		for _, p := range profiles {
			if p.AuthType == "github" && p.AuthID == authIDStr {
				existing = &p
				break
			}
		}
	}

	var targetProfile CommunityProfile
	if existing != nil {
		targetProfile = *existing
	} else {
		targetProfile = CommunityProfile{
			ID:   cleanLogin,
			UUID: cleanLogin,
		}
	}

	targetProfile.AuthType = "github"
	targetProfile.AuthID = authIDStr
	if targetProfile.Nickname == "" {
		if ghUser.Name != "" {
			targetProfile.Nickname = ghUser.Name
		} else {
			targetProfile.Nickname = ghUser.Login
		}
	}
	if targetProfile.Bio == "" && ghUser.Bio != "" {
		targetProfile.Bio = ghUser.Bio
	}
	if targetProfile.Location == "" && ghUser.Location != "" {
		targetProfile.Location = ghUser.Location
	}
	if targetProfile.SourceURL == "" {
		if ghUser.HTMLURL != "" {
			targetProfile.SourceURL = ghUser.HTMLURL
		} else if ghUser.Blog != "" {
			targetProfile.SourceURL = ghUser.Blog
		}
	}
	if targetProfile.Socials == nil {
		targetProfile.Socials = make(map[string]string)
	}
	if _, ok := targetProfile.Socials["github"]; !ok && ghUser.HTMLURL != "" {
		targetProfile.Socials["github"] = ghUser.HTMLURL
	}

	rawToken, tokenHash, err := GenerateArtisanToken()
	if err != nil {
		s.mu.Unlock()
		http.Error(w, "failed to generate artisan token", http.StatusInternalServerError)
		return
	}

	targetProfile.TokenHash = tokenHash
	targetProfile.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	defaultEnabled := true
	if targetProfile.Enabled == nil {
		targetProfile.Enabled = &defaultEnabled
	}

	if err := s.saveCommunityProfile(targetProfile); err != nil {
		s.mu.Unlock()
		http.Error(w, "failed to save artisan profile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	renderGitHubSuccessHTML(w, targetProfile, rawToken, ghUser.AvatarURL)
}

func renderGitHubSuccessHTML(w http.ResponseWriter, profile CommunityProfile, rawToken string, avatarURL string) {
	avatarImg := ""
	if avatarURL != "" {
		avatarImg = fmt.Sprintf(`<img src="%s" alt="GitHub Avatar" class="token-avatar">`, html.EscapeString(avatarURL))
	} else {
		avatarImg = `<div class="token-avatar" style="display:flex;align-items:center;justify-content:center;background:#334155;font-size:1.5rem;">🐧</div>`
	}

	pageHTML := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Penguins Artisan Token - GitHub Authentication</title>
<style>
%s
.token-card {
  max-width: 620px;
  margin: 3rem auto;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 2rem;
  box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1);
}
.token-user-row {
  display: flex;
  align-items: center;
  gap: 1rem;
  margin-bottom: 1.5rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid var(--border);
}
.token-avatar {
  width: 54px;
  height: 54px;
  border-radius: 50%%;
  border: 2px solid var(--accent);
}
.token-code-box {
  background: rgba(0, 0, 0, 0.35);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 1rem;
  margin: 1.25rem 0;
}
.token-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.95rem;
  word-break: break-all;
  color: #38bdf8;
  display: block;
  margin-bottom: 0.85rem;
  background: rgba(15, 23, 42, 0.6);
  padding: 0.5rem 0.75rem;
  border-radius: 4px;
}
.token-copy-btn {
  background: #238636;
  color: #ffffff;
  border: 1px solid #2ea043;
  padding: 0.55rem 1.1rem;
  border-radius: 4px;
  cursor: pointer;
  font-weight: 600;
  font-size: 0.9rem;
}
.token-copy-btn:hover {
  background: #2ea043;
}
.token-guide {
  font-size: 0.85rem;
  color: var(--muted);
  line-height: 1.6;
}
</style>
</head>
<body>
<div class="token-card">
  <div class="token-user-row">
    %s
    <div>
      <h2 style="margin:0 0 0.25rem 0; font-size:1.3rem;">Welcome, @%s!</h2>
      <div style="color:var(--muted); font-size:0.85rem;">Authenticated via GitHub &bull; Distro Artisan</div>
    </div>
  </div>
  <p>Here is your <strong>Artisan Personal Access Token</strong>. Use it to authenticate your remaster machines and synchronize your profile in <code>penguins-gui</code>.</p>
  <div class="token-code-box">
    <code class="token-code" id="tokenVal">%s</code>
    <button class="token-copy-btn" onclick="copyToken()">📋 Copy Artisan Token</button>
  </div>
  <div class="token-guide">
    <strong>How to use:</strong><br>
    &bull; In <strong>penguins-gui</strong>: paste into <em>Settings &rarr; Artisan Token</em>.<br>
    &bull; In <strong>penguins-eggs</strong>: send via <code>X-Eggs-Artisan-Token</code> header.<br>
    &bull; Store this token safely. It links all your ISO builds to your artisan profile.
  </div>
  <div style="margin-top:1.75rem; display:flex; justify-content:space-between; align-items:center;">
    <a href="/api/community" class="btn">&larr; Back to Community Showcase</a>
    <a href="/api/census" class="btn">Telemetry Dashboard &rarr;</a>
  </div>
</div>
<script>
function copyToken() {
  var t = document.getElementById("tokenVal").innerText.trim();
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(t).then(function() {
      alert("Artisan Token copied to clipboard!");
    }).catch(function() {
      prompt("Copy token manually:", t);
    });
  } else {
    prompt("Copy token manually:", t);
  }
}
</script>
</body>
</html>`, embeddedCSS, avatarImg, html.EscapeString(profile.GetID()), html.EscapeString(rawToken))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(pageHTML))
}

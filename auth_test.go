/*
 * penguins-census - Copyright (C) 2026-2026 Piero Proietti
 * Author: Piero Proietti <piero.proietti@gmail.com>
 * https://penguins-eggs.net
 *
 * This file is part of penguins-census.
 * Released under the GNU GPL v3 license.
 */

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestPenguinsGUITokenLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	uuid := "550e8400-e29b-41d4-a716-446655440000"

	// 1. Initial creation from penguins-gui without token
	createPayload := `{
		"uuid": "` + uuid + `",
		"nickname": "PenguinMaster",
		"location": "Rome, Italy",
		"bio": "Building distros with Penguins Eggs",
		"source_url": "https://example.org/distro"
	}`
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	wCreate := httptest.NewRecorder()
	srv.ServeHTTP(wCreate, reqCreate)

	if wCreate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on profile creation, got %d: %s", wCreate.Code, wCreate.Body.String())
	}

	var createResp map[string]string
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to decode creation response: %v", err)
	}
	token := createResp["artisan_token"]
	if token == "" || !strings.HasPrefix(token, "egg_pat_") {
		t.Fatalf("expected egg_pat_ artisan_token in response, got: %v", createResp)
	}

	// 2. Modifica senza token -> 401 Unauthorized
	updatePayload := `{"uuid":"` + uuid + `","nickname":"UpdatedNick"}`
	reqNoToken := httptest.NewRequest(http.MethodPut, "/api/community/"+uuid, strings.NewReader(updatePayload))
	reqNoToken.Header.Set("Content-Type", "application/json")
	wNoToken := httptest.NewRecorder()
	srv.ServeHTTP(wNoToken, reqNoToken)
	if wNoToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without token, got %d: %s", wNoToken.Code, wNoToken.Body.String())
	}

	// 3. Modifica con token errato -> 403 Forbidden
	reqBadToken := httptest.NewRequest(http.MethodPut, "/api/community/"+uuid, strings.NewReader(updatePayload))
	reqBadToken.Header.Set("Content-Type", "application/json")
	reqBadToken.Header.Set("X-Eggs-Artisan-Token", "egg_pat_wrongtoken12345")
	wBadToken := httptest.NewRecorder()
	srv.ServeHTTP(wBadToken, reqBadToken)
	if wBadToken.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden with bad token, got %d: %s", wBadToken.Code, wBadToken.Body.String())
	}

	// 4. Modifica con token valido (header X-Eggs-Artisan-Token) -> 200 OK
	reqValidToken := httptest.NewRequest(http.MethodPut, "/api/community/"+uuid, strings.NewReader(updatePayload))
	reqValidToken.Header.Set("Content-Type", "application/json")
	reqValidToken.Header.Set("X-Eggs-Artisan-Token", token)
	wValidToken := httptest.NewRecorder()
	srv.ServeHTTP(wValidToken, reqValidToken)
	if wValidToken.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with valid token, got %d: %s", wValidToken.Code, wValidToken.Body.String())
	}

	// 5. Modifica con token valido (Authorization: Bearer) -> 200 OK
	updatePayload2 := `{"uuid":"` + uuid + `","location":"Florence, Italy"}`
	reqBearer := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(updatePayload2))
	reqBearer.Header.Set("Content-Type", "application/json")
	reqBearer.Header.Set("Authorization", "Bearer "+token)
	wBearer := httptest.NewRecorder()
	srv.ServeHTTP(wBearer, reqBearer)
	if wBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with Bearer token, got %d: %s", wBearer.Code, wBearer.Body.String())
	}

	// 6. Cancellazione senza token -> 401 Unauthorized
	reqDelNoAuth := httptest.NewRequest(http.MethodDelete, "/api/community/"+uuid, nil)
	wDelNoAuth := httptest.NewRecorder()
	srv.ServeHTTP(wDelNoAuth, reqDelNoAuth)
	if wDelNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized on DELETE without token, got %d", wDelNoAuth.Code)
	}

	// 7. Cancellazione con token errato -> 403 Forbidden
	reqDelBad := httptest.NewRequest(http.MethodDelete, "/api/community/"+uuid, nil)
	reqDelBad.Header.Set("X-Eggs-Artisan-Token", "egg_pat_bad")
	wDelBad := httptest.NewRecorder()
	srv.ServeHTTP(wDelBad, reqDelBad)
	if wDelBad.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on DELETE with bad token, got %d", wDelBad.Code)
	}

	// 8. Cancellazione con token valido -> 200 OK
	reqDelOk := httptest.NewRequest(http.MethodDelete, "/api/community/"+uuid, nil)
	reqDelOk.Header.Set("X-Eggs-Artisan-Token", token)
	wDelOk := httptest.NewRecorder()
	srv.ServeHTTP(wDelOk, reqDelOk)
	if wDelOk.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on DELETE with valid token, got %d", wDelOk.Code)
	}
}

func TestCommunityMeSync(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	uuid := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	createPayload := `{
		"uuid": "` + uuid + `",
		"nickname": "SyncTester",
		"location": "Turin, Italy",
		"bio": "Syncing across VMs",
		"source_url": "https://synctester.dev"
	}`
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	wCreate := httptest.NewRecorder()
	srv.ServeHTTP(wCreate, reqCreate)
	if wCreate.Code != http.StatusOK {
		t.Fatalf("failed to create profile: %s", wCreate.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(wCreate.Body.Bytes(), &resp)
	token := resp["artisan_token"]

	// 1. GET /api/community/me without token -> 401
	reqNoToken := httptest.NewRequest(http.MethodGet, "/api/community/me", nil)
	wNoToken := httptest.NewRecorder()
	srv.ServeHTTP(wNoToken, reqNoToken)
	if wNoToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on /me without token, got %d", wNoToken.Code)
	}

	// 2. GET /community/me with invalid token -> 401
	reqBadToken := httptest.NewRequest(http.MethodGet, "/community/me", nil)
	reqBadToken.Header.Set("X-Eggs-Artisan-Token", "egg_pat_nonexistent")
	wBadToken := httptest.NewRecorder()
	srv.ServeHTTP(wBadToken, reqBadToken)
	if wBadToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on /me with invalid token, got %d", wBadToken.Code)
	}

	// 3. GET /api/community/me with valid X-Eggs-Artisan-Token
	reqMe := httptest.NewRequest(http.MethodGet, "/api/community/me", nil)
	reqMe.Header.Set("X-Eggs-Artisan-Token", token)
	wMe := httptest.NewRecorder()
	srv.ServeHTTP(wMe, reqMe)
	if wMe.Code != http.StatusOK {
		t.Fatalf("expected 200 on /me with valid token, got %d: %s", wMe.Code, wMe.Body.String())
	}

	var prof CommunityProfile
	if err := json.Unmarshal(wMe.Body.Bytes(), &prof); err != nil {
		t.Fatalf("failed to parse /me response: %v", err)
	}
	if prof.UUID != uuid || prof.Nickname != "SyncTester" || prof.Location != "Turin, Italy" {
		t.Fatalf("unexpected profile data in /me: %+v", prof)
	}
	if prof.TokenHash != "" {
		t.Errorf("token_hash must NEVER be exposed in /me JSON, got: %s", prof.TokenHash)
	}

	// 4. GET /community/me with Bearer token
	reqMeBearer := httptest.NewRequest(http.MethodGet, "/community/me", nil)
	reqMeBearer.Header.Set("Authorization", "Bearer "+token)
	wMeBearer := httptest.NewRecorder()
	srv.ServeHTTP(wMeBearer, reqMeBearer)
	if wMeBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 on /community/me with Bearer token, got %d", wMeBearer.Code)
	}
}

func TestGitHubOAuthFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	// Mock GitHub OAuth & API Server
	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost {
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
				return
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["code"] != "valid_mock_code" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             "bad_verification_code",
					"error_description": "The code passed is incorrect or expired.",
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "gh_access_token_123456",
				"token_type":   "bearer",
			})
		case "/user":
			authHdr := r.Header.Get("Authorization")
			if authHdr != "Bearer gh_access_token_123456" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":         10203040,
				"login":      "artisan-dev",
				"name":       "Artisan Developer",
				"bio":        "Custom Linux distros builder",
				"location":   "Bologna, Italy",
				"blog":       "https://artisan-dev.org",
				"html_url":   "https://github.com/artisan-dev",
				"avatar_url": "https://avatars.githubusercontent.com/u/10203040",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGH.Close()

	// 1. Without configuration -> 501 / Not Implemented
	reqUnconfigured := httptest.NewRequest(http.MethodGet, "/auth/github/login", nil)
	wUnconfigured := httptest.NewRecorder()
	srv.ServeHTTP(wUnconfigured, reqUnconfigured)
	if wUnconfigured.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 when GitHub OAuth not configured, got %d", wUnconfigured.Code)
	}

	// 2. Configure GitHub OAuth with mock endpoints
	srv.SetGitHubAuth("mock_client_id", "mock_client_secret")
	srv.githubAuth.TokenURL = mockGH.URL + "/login/oauth/access_token"
	srv.githubAuth.UserAPIURL = mockGH.URL + "/user"
	srv.githubAuth.AuthURL = mockGH.URL + "/login/oauth/authorize"

	if !srv.IsGitHubAuthEnabled() {
		t.Fatalf("expected GitHub auth to be enabled")
	}

	// 3. Initiate Login: GET /auth/github/login
	reqLogin := httptest.NewRequest(http.MethodGet, "/auth/github/login", nil)
	wLogin := httptest.NewRecorder()
	srv.ServeHTTP(wLogin, reqLogin)

	if wLogin.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect on login, got %d", wLogin.Code)
	}

	cookies := wLogin.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "eggs_oauth_state" {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatalf("expected eggs_oauth_state cookie to be set")
	}

	loc := wLogin.Header().Get("Location")
	if !strings.Contains(loc, "client_id=mock_client_id") || !strings.Contains(loc, "state="+stateCookie.Value) {
		t.Fatalf("unexpected redirect location: %s", loc)
	}

	// 4. Callback with invalid state -> 400 Bad Request
	reqBadState := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=valid_mock_code&state=wrong_state", nil)
	reqBadState.AddCookie(stateCookie)
	wBadState := httptest.NewRecorder()
	srv.ServeHTTP(wBadState, reqBadState)
	if wBadState.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on state mismatch, got %d", wBadState.Code)
	}

	// 5. Successful Callback: GET /auth/github/callback
	callbackURL := "/auth/github/callback?code=valid_mock_code&state=" + url.QueryEscape(stateCookie.Value)
	reqCallback := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	reqCallback.AddCookie(stateCookie)
	wCallback := httptest.NewRecorder()
	srv.ServeHTTP(wCallback, reqCallback)

	if wCallback.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on callback, got %d: %s", wCallback.Code, wCallback.Body.String())
	}

	htmlBody := wCallback.Body.String()
	if !strings.Contains(htmlBody, "Welcome, @artisan-dev!") {
		t.Errorf("expected welcome greeting for artisan-dev, got: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, "egg_pat_") {
		t.Errorf("expected generated egg_pat_ token in callback HTML")
	}

	// 6. Verify profile was created in storage with AuthType=github
	prof, err := srv.getCommunityProfile("artisan-dev")
	if err != nil {
		t.Fatalf("failed to load created profile from storage: %v", err)
	}
	if prof.AuthType != "github" || prof.AuthID != "10203040" {
		t.Errorf("expected AuthType=github and AuthID=10203040, got %+v", prof)
	}
	if prof.Nickname != "Artisan Developer" {
		t.Errorf("expected nickname 'Artisan Developer', got %s", prof.Nickname)
	}

	// 7. Verify Community Showcase HTML renders GitHub badge and login button
	reqList := httptest.NewRequest(http.MethodGet, "/api/community?format=html", nil)
	wList := httptest.NewRecorder()
	srv.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/community HTML, got %d", wList.Code)
	}
	listHTML := wList.Body.String()
	if !strings.Contains(listHTML, "Sign in with GitHub (Get Token)") {
		t.Errorf("expected GitHub sign in button in community showcase")
	}
	if !strings.Contains(listHTML, "🐙 GitHub") {
		t.Errorf("expected verified GitHub badge for artisan profile in showcase")
	}
}

func TestIssueArtisanTokenCLI(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	artisanID := "distro-hacker"

	// 1. Issue token via CLI method for brand new profile
	token1, err := srv.IssueArtisanToken(artisanID)
	if err != nil {
		t.Fatalf("IssueArtisanToken failed: %v", err)
	}
	if !strings.HasPrefix(token1, "egg_pat_") {
		t.Fatalf("expected egg_pat_ prefix, got: %s", token1)
	}

	// 2. Query /api/community/me with token1
	reqMe := httptest.NewRequest(http.MethodGet, "/api/community/me", nil)
	reqMe.Header.Set("X-Eggs-Artisan-Token", token1)
	wMe := httptest.NewRecorder()
	srv.ServeHTTP(wMe, reqMe)
	if wMe.Code != http.StatusOK {
		t.Fatalf("expected 200 on /me with token1, got %d: %s", wMe.Code, wMe.Body.String())
	}

	var p CommunityProfile
	_ = json.Unmarshal(wMe.Body.Bytes(), &p)
	if p.GetID() != artisanID {
		t.Fatalf("expected profile ID %s, got %s", artisanID, p.GetID())
	}

	// 3. Rotate token via CLI method
	token2, err := srv.IssueArtisanToken(artisanID)
	if err != nil {
		t.Fatalf("IssueArtisanToken rotation failed: %v", err)
	}
	if token2 == token1 {
		t.Fatalf("expected rotated token to be different from token1")
	}

	// 4. Old token1 is now invalidated (401)
	reqMeOld := httptest.NewRequest(http.MethodGet, "/api/community/me", nil)
	reqMeOld.Header.Set("X-Eggs-Artisan-Token", token1)
	wMeOld := httptest.NewRecorder()
	srv.ServeHTTP(wMeOld, reqMeOld)
	if wMeOld.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with old token1 after rotation, got %d", wMeOld.Code)
	}

	// 5. New token2 works (200)
	reqMeNew := httptest.NewRequest(http.MethodGet, "/api/community/me", nil)
	reqMeNew.Header.Set("X-Eggs-Artisan-Token", token2)
	wMeNew := httptest.NewRecorder()
	srv.ServeHTTP(wMeNew, reqMeNew)
	if wMeNew.Code != http.StatusOK {
		t.Fatalf("expected 200 with new token2, got %d", wMeNew.Code)
	}
}

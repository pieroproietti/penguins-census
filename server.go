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
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

type CensusServer struct {
	mu             sync.Mutex
	dataPath       string
	mmdbPath       string
	mmdb           *maxminddb.Reader
	communityDir   string
	githubAuth     *GitHubAuthConfig
	gravatarClient *GravatarClient
}

func NewCensusServer(dataPath, mmdbPath string) *CensusServer {
	commDir := ""
	if dataPath != "" {
		fi, err := os.Stat(dataPath)
		if err == nil && fi.IsDir() {
			commDir = filepath.Join(dataPath, "community")
		} else {
			commDir = filepath.Join(filepath.Dir(dataPath), "community")
		}
	}
	var ghAuth *GitHubAuthConfig
	ghClientID := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID"))
	ghClientSecret := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_SECRET"))
	if ghClientID != "" && ghClientSecret != "" {
		ghAuth = &GitHubAuthConfig{
			ClientID:     ghClientID,
			ClientSecret: ghClientSecret,
			AuthURL:      "https://github.com/login/oauth/authorize",
			TokenURL:     "https://github.com/login/oauth/access_token",
			UserAPIURL:   "https://api.github.com/user",
		}
	}
	s := &CensusServer{
		dataPath:       dataPath,
		mmdbPath:       mmdbPath,
		communityDir:   commDir,
		githubAuth:     ghAuth,
		gravatarClient: NewGravatarClient(6 * time.Hour),
	}
	if mmdbPath != "" {
		if reader, err := maxminddb.Open(mmdbPath); err == nil {
			s.mmdb = reader
		}
	}
	return s
}

func (s *CensusServer) SetGravatarBaseURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gravatarClient != nil {
		s.gravatarClient.SetBaseURL(url)
	}
}

func (s *CensusServer) SetGravatarClient(client *http.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gravatarClient != nil {
		s.gravatarClient.SetHTTPClient(client)
	}
}

func (s *CensusServer) SetCommunityDir(dir string) {
	s.communityDir = dir
}

func (s *CensusServer) getCommunityDir() string {
	if s.communityDir != "" {
		return s.communityDir
	}
	if s.dataPath != "" {
		fi, err := os.Stat(s.dataPath)
		if err == nil && fi.IsDir() {
			return filepath.Join(s.dataPath, "community")
		}
		return filepath.Join(filepath.Dir(s.dataPath), "community")
	}
	return "/var/lib/penguins-census/community"
}

func (s *CensusServer) Close() error {
	if s.mmdb != nil {
		return s.mmdb.Close()
	}
	return nil
}

// resolveCountry extracts the client IP from X-Real-IP (or X-Forwarded-For fallback)
// strictly in memory to resolve a 2-letter ISO country code, then discards the IP.
func (s *CensusServer) resolveCountry(r *http.Request) string {
	rawIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if rawIP == "" {
		xff := r.Header.Get("X-Forwarded-For")
		if xff != "" {
			parts := strings.Split(xff, ",")
			rawIP = strings.TrimSpace(parts[0])
		}
	}
	if rawIP == "" {
		return "XX"
	}

	if host, _, err := net.SplitHostPort(rawIP); err == nil {
		rawIP = host
	}

	ip := net.ParseIP(rawIP)
	rawIP = "" // discard raw string immediately
	if ip == nil {
		return "XX"
	}

	reader := s.mmdb
	if reader == nil && s.mmdbPath != "" {
		if opened, err := maxminddb.Open(s.mmdbPath); err == nil {
			defer opened.Close()
			reader = opened
		}
	}
	if reader == nil {
		return "XX"
	}

	var rec mmdbCountryRecord
	if err := reader.Lookup(ip, &rec); err != nil || len(rec.Country.ISOCode) != 2 {
		return "XX"
	}
	return strings.ToUpper(rec.Country.ISOCode)
}

func (s *CensusServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/auth/github" || strings.HasPrefix(r.URL.Path, "/auth/github/") {
		s.handleGitHubAuth(w, r)
		return
	}

	if r.URL.Path == "/api/census" {
		switch r.Method {
		case http.MethodPost:
			s.handlePost(w, r)
		case http.MethodGet:
			s.handleGet(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	if r.URL.Path == "/api/community" || strings.HasPrefix(r.URL.Path, "/api/community/") ||
		r.URL.Path == "/community" || strings.HasPrefix(r.URL.Path, "/community/") {
		s.handleCommunity(w, r)
		return
	}

	http.NotFound(w, r)
}

func (s *CensusServer) handlePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()

	var rec CensusRecord
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&rec); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON payload"}`))
		return
	}

	rec.UUID = strings.TrimSpace(rec.UUID)
	if rec.UUID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"uuid is required"}`))
		return
	}

	if rec.Action != "remaster" && rec.Action != "sysinstall" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"action must be remaster or sysinstall"}`))
		return
	}

	rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	rec.Country = s.resolveCountry(r)

	// Verify author token if present
	rawToken := extractAuthorToken(r)
	if rawToken != "" {
		s.mu.Lock()
		profile, err := s.findProfileByToken(rawToken)
		s.mu.Unlock()
		if err == nil && profile != nil {
			rec.AuthorID = profile.GetID()
			rec.ArtisanID = profile.GetID()
		} else {
			rec.AuthorID = ""
			rec.ArtisanID = ""
		}
	} else {
		rec.AuthorID = ""
		rec.ArtisanID = ""
	}

	line, err := json.Marshal(rec)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to serialize record"}`))
		return
	}
	line = append(line, '\n')

	s.mu.Lock()
	err = s.appendLine(line)
	s.mu.Unlock()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to persist record"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}


func extractAuthorToken(r *http.Request) string {
	rawToken := strings.TrimSpace(r.Header.Get("X-Eggs-Author-Token"))
	if rawToken != "" {
		return rawToken
	}
	// Fallback to legacy artisan header
	rawToken = strings.TrimSpace(r.Header.Get("X-Eggs-Artisan-Token"))
	if rawToken != "" {
		return rawToken
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// Deprecated alias
func extractArtisanToken(r *http.Request) string {
	return extractAuthorToken(r)
}

func isValidUUID(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" || len(u) > 64 {
		return false
	}
	if strings.Contains(u, "/") || strings.Contains(u, "\\") || strings.Contains(u, "..") {
		return false
	}
	return uuidSafePattern.MatchString(u)
}

func hasEmailString(val string) bool {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return false
	}
	// Fediverse handles like @user@instance.social are valid handles, not emails
	if strings.HasPrefix(trimmed, "@") && strings.Count(trimmed, "@") >= 2 {
		return false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "mailto:") {
		return true
	}
	return emailForbiddenPattern.MatchString(trimmed)
}

func containsEmailInPayload(data interface{}) bool {
	switch v := data.(type) {
	case string:
		return hasEmailString(v)
	case map[string]interface{}:
		for k, val := range v {
			if strings.EqualFold(k, "email") {
				if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
					return true
				}
				if val != nil && val != false {
					return true
				}
			}
			if containsEmailInPayload(val) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if containsEmailInPayload(item) {
				return true
			}
		}
	}
	return false
}


func (s *CensusServer) handleCommunity(w http.ResponseWriter, r *http.Request) {
	subpath := strings.TrimPrefix(r.URL.Path, "/api/community")
	subpath = strings.TrimPrefix(subpath, "/community")
	subpath = strings.TrimPrefix(subpath, "/")
	subpath = strings.TrimSuffix(subpath, "/")
	subpath = strings.TrimSuffix(subpath, ".json")

	if subpath == "" {
		switch r.Method {
		case http.MethodPost, http.MethodPut:
			s.handleSaveCommunityProfile(w, r, "")
		case http.MethodGet:
			s.handleListCommunityProfiles(w, r)
		default:
			w.Header().Set("Allow", "GET, POST, PUT")
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	if subpath == "me" {
		if r.Method == http.MethodGet {
			s.handleCommunityMe(w, r)
		} else {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	targetUUID := subpath
	if !isValidUUID(targetUUID) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid uuid format in URL"}`))
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		s.handleSaveCommunityProfile(w, r, targetUUID)
	case http.MethodGet:
		s.handleGetCommunityProfile(w, r, targetUUID)
	case http.MethodDelete:
		s.handleDeleteCommunityProfile(w, r, targetUUID)
	default:
		w.Header().Set("Allow", "GET, POST, PUT, DELETE")
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *CensusServer) handleSaveCommunityProfile(w http.ResponseWriter, r *http.Request, pathID string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"failed to read request body or body too large"}`))
		return
	}

	var raw rawCommunityPayload
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &raw); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid JSON structure"}`))
			return
		}
	}

	authorID := strings.TrimSpace(raw.AuthorID)
	if authorID == "" && pathID != "" {
		authorID = pathID
	}
	if authorID == "" && strings.TrimSpace(raw.Email) != "" {
		authorID = ComputeAuthorID(raw.Email)
	}
	if authorID == "" && strings.TrimSpace(raw.UUID) != "" && pathID == "" {
		authorID = strings.TrimSpace(raw.UUID)
	}

	if pathID != "" && authorID != "" && !strings.EqualFold(pathID, authorID) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"author_id in path does not match payload"}`))
		return
	}

	if !isValidUUID(authorID) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"valid author_id is required"}`))
		return
	}

	machineUUID := strings.TrimSpace(raw.UUID)
	if machineUUID != "" && !isValidUUID(machineUUID) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid uuid format"}`))
		return
	}

	s.mu.Lock()
	existing, _ := s.getCommunityProfile(authorID)
	s.mu.Unlock()

	callerToken := extractAuthorToken(r)

	// Caso A: Primo avvio (TOFU - l'author_id non esiste ancora sul server)
	if existing == nil {
		rawToken, tokenHash, err := GenerateAuthorToken()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"failed to generate author token"}`))
			return
		}

		s.mu.Lock()
		if machineUUID != "" {
			_ = s.claimUUIDForAuthor(authorID, machineUUID)
		}

		claimed := []string{}
		if machineUUID != "" {
			claimed = append(claimed, machineUUID)
		}

		enabled := true
		if raw.Enabled != nil {
			enabled = *raw.Enabled
		}

		now := time.Now().UTC().Format(time.RFC3339)
		profile := CommunityProfile{
			AuthorID:     authorID,
			TokenHash:    tokenHash,
			ClaimedUUIDs: claimed,
			Enabled:      enabled,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		err = s.saveCommunityProfile(profile)
		s.mu.Unlock()

		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"failed to save community profile"}`))
			return
		}

		respMap := map[string]string{
			"status":        "ok",
			"author_id":     authorID,
			"uuid":          authorID,
			"author_token":  rawToken,
			"artisan_token": rawToken, // backwards compatibility
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		respBytes, _ := json.Marshal(respMap)
		_, _ = w.Write(respBytes)
		return
	}

	// Caso B: Autore già esistente
	if callerToken == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: author token required"}`))
		return
	}
	callerHash := HashAuthorToken(callerToken)
	if subtle.ConstantTimeCompare([]byte(existing.TokenHash), []byte(callerHash)) != 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: invalid author token"}`))
		return
	}

	s.mu.Lock()
	if machineUUID != "" {
		_ = s.claimUUIDForAuthor(authorID, machineUUID)
		found := false
		for _, u := range existing.ClaimedUUIDs {
			if strings.EqualFold(u, machineUUID) {
				found = true
				break
			}
		}
		if !found {
			existing.ClaimedUUIDs = append(existing.ClaimedUUIDs, machineUUID)
		}
	}

	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	existing.Enabled = enabled
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	err = s.saveCommunityProfile(*existing)
	s.mu.Unlock()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to save community profile"}`))
		return
	}

	respMap := map[string]string{
		"status":    "ok",
		"author_id": authorID,
		"uuid":      authorID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	respBytes, _ := json.Marshal(respMap)
	_, _ = w.Write(respBytes)
}

func (s *CensusServer) handleGetCommunityProfile(w http.ResponseWriter, r *http.Request, authorID string) {
	s.mu.Lock()
	profile, err := s.getCommunityProfile(authorID)
	dataPath := s.dataPath
	s.mu.Unlock()

	if err != nil || profile == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"community profile not found"}`))
		return
	}

	enriched := s.enrichSingleProfile(*profile, dataPath)

	resp, err := json.MarshalIndent(enriched, "", "  ")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to serialize profile"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

func (s *CensusServer) handleListCommunityProfiles(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	dataPath := s.dataPath
	commDir := s.getCommunityDir()
	gClient := s.gravatarClient
	s.mu.Unlock()

	authors, err := loadEnrichedAuthors(dataPath, commDir, gClient)
	if err != nil && !os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to list community profiles"}`))
		return
	}

	format := negotiateFormat(r)
	switch format {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderCommunityHTML(authors, s.IsGitHubAuthEnabled())))
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderCommunityText(authors)))
	default:
		type listResponse struct {
			Total    int             `json:"total"`
			Profiles []AuthorProfile `json:"profiles"`
		}
		writePrettyJSON(w, listResponse{
			Total:    len(authors),
			Profiles: authors,
		})
	}
}

func (s *CensusServer) handleDeleteCommunityProfile(w http.ResponseWriter, r *http.Request, authorID string) {
	s.mu.Lock()
	existing, err := s.getCommunityProfile(authorID)
	s.mu.Unlock()

	if err != nil || existing == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"community profile not found"}`))
		return
	}

	callerToken := extractAuthorToken(r)
	if callerToken == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: author token required"}`))
		return
	}
	callerHash := HashAuthorToken(callerToken)
	if subtle.ConstantTimeCompare([]byte(existing.TokenHash), []byte(callerHash)) != 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: invalid author token"}`))
		return
	}

	// Read optional body to check if a specific machine UUID is being dissociated
	var delPayload struct {
		AuthorID string `json:"author_id"`
		UUID     string `json:"uuid"`
	}
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		_ = json.Unmarshal(bodyBytes, &delPayload)
	}

	uuidToDissociate := strings.TrimSpace(delPayload.UUID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if uuidToDissociate != "" {
		newClaimed := make([]string, 0, len(existing.ClaimedUUIDs))
		for _, u := range existing.ClaimedUUIDs {
			if !strings.EqualFold(u, uuidToDissociate) {
				newClaimed = append(newClaimed, u)
			}
		}
		if len(newClaimed) == 0 {
			_ = s.deleteCommunityProfile(existing.GetID())
		} else {
			existing.ClaimedUUIDs = newClaimed
			existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			_ = s.saveCommunityProfile(*existing)
		}
	} else {
		_ = s.deleteCommunityProfile(existing.GetID())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *CensusServer) handleCommunityMe(w http.ResponseWriter, r *http.Request) {
	token := extractAuthorToken(r)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: author token required"}`))
		return
	}

	s.mu.Lock()
	profile, err := s.findProfileByToken(token)
	s.mu.Unlock()

	if err != nil || profile == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: invalid author token"}`))
		return
	}

	safe := *profile
	safe.TokenHash = ""

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(safe)
}


func (s *CensusServer) handleGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	summary, err := loadCensusSummary(s.dataPath, s.getCommunityDir(), s.gravatarClient)
	s.mu.Unlock()

	if err != nil && !os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to read census data"}`))
		return
	}

	respondSummary(w, r, summary)
}

func negotiateFormat(r *http.Request) string {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	switch format {
	case "json":
		return "json"
	case "txt", "text", "md", "markdown":
		return "txt"
	case "html":
		return "html"
	}

	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return "json"
	}
	if strings.Contains(accept, "text/html") {
		return "html"
	}
	if strings.Contains(accept, "text/plain") {
		return "txt"
	}

	ua := strings.ToLower(strings.TrimSpace(r.Header.Get("User-Agent")))
	if strings.HasPrefix(ua, "curl/") {
		return "txt"
	}

	return "json"
}

func respondSummary(w http.ResponseWriter, r *http.Request, summary CensusSummary) {
	switch negotiateFormat(r) {
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderTextSummary(summary)))
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderHTMLSummary(summary)))
	default:
		writePrettyJSON(w, summary)
	}
}



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
	mu           sync.Mutex
	dataPath     string
	mmdbPath     string
	mmdb         *maxminddb.Reader
	communityDir string
	githubAuth   *GitHubAuthConfig
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
		dataPath:     dataPath,
		mmdbPath:     mmdbPath,
		communityDir: commDir,
		githubAuth:   ghAuth,
	}
	if mmdbPath != "" {
		if reader, err := maxminddb.Open(mmdbPath); err == nil {
			s.mmdb = reader
		}
	}
	return s
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

	// Verify artisan token if present
	rawToken := extractArtisanToken(r)
	if rawToken != "" {
		s.mu.Lock()
		profile, err := s.findProfileByToken(rawToken)
		s.mu.Unlock()
		if err == nil && profile != nil {
			rec.ArtisanID = profile.GetID()
		} else {
			rec.ArtisanID = ""
		}
	} else {
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


func extractArtisanToken(r *http.Request) string {
	rawToken := strings.TrimSpace(r.Header.Get("X-Eggs-Artisan-Token"))
	if rawToken != "" {
		return rawToken
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
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

func (s *CensusServer) handleSaveCommunityProfile(w http.ResponseWriter, r *http.Request, pathUUID string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"failed to read request body or body too large"}`))
		return
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &rawMap); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON payload"}`))
		return
	}

	// Anti-Spam: validate that no email address is present in the payload
	if containsEmailInPayload(rawMap) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"email addresses are forbidden to prevent spam"}`))
		return
	}

	var raw rawCommunityPayload
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON structure"}`))
		return
	}

	// Support wrapping inside "community": { ... }
	if raw.Community != nil {
		if raw.ID == "" {
			raw.ID = raw.Community.ID
		}
		if raw.UUID == "" {
			raw.UUID = raw.Community.UUID
		}
		if raw.AuthType == "" {
			raw.AuthType = raw.Community.AuthType
		}
		if raw.AuthID == "" {
			raw.AuthID = raw.Community.AuthID
		}
		if raw.TokenHash == "" {
			raw.TokenHash = raw.Community.TokenHash
		}
		if raw.Token == "" {
			raw.Token = raw.Community.Token
		}
		if raw.Nickname == "" {
			raw.Nickname = raw.Community.Nickname
		}
		if raw.Location == "" {
			raw.Location = raw.Community.Location
		}
		if raw.Bio == "" {
			raw.Bio = raw.Community.Bio
		}
		if raw.Contact == "" {
			raw.Contact = raw.Community.Contact
		}
		if raw.Social == nil {
			raw.Social = raw.Community.Social
		}
		if raw.Socials == nil {
			raw.Socials = raw.Community.Socials
		}
		if raw.SourceURL == "" {
			raw.SourceURL = raw.Community.SourceURL
		}
		if raw.URL == "" {
			raw.URL = raw.Community.URL
		}
		if raw.Website == "" {
			raw.Website = raw.Community.Website
		}
		if raw.RepositoryURL == "" {
			raw.RepositoryURL = raw.Community.RepositoryURL
		}
		if raw.RepoURL == "" {
			raw.RepoURL = raw.Community.RepoURL
		}
		if raw.Enabled == nil {
			raw.Enabled = raw.Community.Enabled
		}
	}

	finalID := strings.TrimSpace(raw.ID)
	finalUUID := strings.TrimSpace(raw.UUID)
	if finalID == "" && finalUUID != "" {
		finalID = finalUUID
	}
	if finalUUID == "" && finalID != "" {
		finalUUID = finalID
	}

	if pathUUID != "" {
		if (finalID != "" && !strings.EqualFold(finalID, pathUUID)) && (finalUUID != "" && !strings.EqualFold(finalUUID, pathUUID)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"uuid in path does not match uuid in payload"}`))
			return
		}
		if finalID == "" {
			finalID = pathUUID
		}
		if finalUUID == "" {
			finalUUID = pathUUID
		}
	}

	if !isValidUUID(finalID) && !isValidUUID(finalUUID) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"valid uuid is required"}`))
		return
	}

	sourceURL := strings.TrimSpace(raw.SourceURL)
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(raw.URL)
	}
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(raw.Website)
	}
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(raw.RepositoryURL)
	}
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(raw.RepoURL)
	}

	contact := strings.TrimSpace(raw.Contact)
	socials := make(map[string]string)
	if raw.Socials != nil {
		for k, v := range raw.Socials {
			kTrim := strings.TrimSpace(k)
			vTrim := strings.TrimSpace(v)
			if kTrim != "" && vTrim != "" {
				socials[kTrim] = vTrim
			}
		}
	}

	if raw.Social != nil {
		switch s := raw.Social.(type) {
		case string:
			if contact == "" {
				contact = strings.TrimSpace(s)
			}
		case map[string]interface{}:
			for k, v := range s {
				kTrim := strings.TrimSpace(k)
				if strVal, ok := v.(string); ok {
					vTrim := strings.TrimSpace(strVal)
					if kTrim != "" && vTrim != "" {
						socials[kTrim] = vTrim
					}
				}
			}
		}
	}

	defaultEnabled := true
	if raw.Enabled != nil {
		defaultEnabled = *raw.Enabled
	}

	s.mu.Lock()
	existing, _ := s.getCommunityProfile(finalID)
	if existing == nil && finalUUID != finalID {
		existing, _ = s.getCommunityProfile(finalUUID)
	}
	s.mu.Unlock()

	callerToken := extractArtisanToken(r)
	var issuedToken string
	tokenHash := ""

	if existing != nil && existing.TokenHash != "" {
		if callerToken == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized: artisan token required"}`))
			return
		}
		callerHash := HashArtisanToken(callerToken)
		if subtle.ConstantTimeCompare([]byte(existing.TokenHash), []byte(callerHash)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden: invalid artisan token"}`))
			return
		}
		tokenHash = existing.TokenHash
	} else {
		explicitHash := strings.TrimSpace(raw.TokenHash)
		if explicitHash == "" && strings.TrimSpace(raw.Token) != "" {
			explicitHash = HashArtisanToken(strings.TrimSpace(raw.Token))
		}
		if explicitHash != "" {
			tokenHash = explicitHash
		} else {
			rawTok, th, err := GenerateArtisanToken()
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"failed to generate artisan token"}`))
				return
			}
			tokenHash = th
			issuedToken = rawTok
		}
	}

	authType := strings.TrimSpace(raw.AuthType)
	authID := strings.TrimSpace(raw.AuthID)
	if existing != nil {
		if authType == "" {
			authType = existing.AuthType
		}
		if authID == "" {
			authID = existing.AuthID
		}
	}

	profile := CommunityProfile{
		ID:        finalID,
		UUID:      finalUUID,
		AuthType:  authType,
		AuthID:    authID,
		TokenHash: tokenHash,
		Nickname:  strings.TrimSpace(raw.Nickname),
		Location:  strings.TrimSpace(raw.Location),
		Bio:       strings.TrimSpace(raw.Bio),
		Contact:   contact,
		Socials:   socials,
		SourceURL: sourceURL,
		Enabled:   &defaultEnabled,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	s.mu.Lock()
	err = s.saveCommunityProfile(profile)
	s.mu.Unlock()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to save community profile"}`))
		return
	}

	respMap := map[string]string{
		"status": "ok",
		"uuid":   profile.UUID,
		"id":     profile.GetID(),
	}
	if issuedToken != "" {
		respMap["artisan_token"] = issuedToken
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp, _ := json.Marshal(respMap)
	_, _ = w.Write(resp)
}

func (s *CensusServer) handleGetCommunityProfile(w http.ResponseWriter, r *http.Request, uuid string) {
	s.mu.Lock()
	profile, err := s.getCommunityProfile(uuid)
	dataPath := s.dataPath
	s.mu.Unlock()

	if err != nil {
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
	s.mu.Unlock()

	artisans, err := loadEnrichedArtisans(dataPath, commDir)
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
		_, _ = w.Write([]byte(renderCommunityHTML(artisans, s.IsGitHubAuthEnabled())))
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderCommunityText(artisans)))
	default:
		type listResponse struct {
			Total    int              `json:"total"`
			Profiles []ArtisanProfile `json:"profiles"`
		}
		writePrettyJSON(w, listResponse{
			Total:    len(artisans),
			Profiles: artisans,
		})
	}
}

func (s *CensusServer) handleDeleteCommunityProfile(w http.ResponseWriter, r *http.Request, uuid string) {
	s.mu.Lock()
	existing, err := s.getCommunityProfile(uuid)
	s.mu.Unlock()

	if err != nil || existing == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"community profile not found"}`))
		return
	}

	if existing.TokenHash != "" {
		callerToken := extractArtisanToken(r)
		if callerToken == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized: artisan token required"}`))
			return
		}
		callerHash := HashArtisanToken(callerToken)
		if subtle.ConstantTimeCompare([]byte(existing.TokenHash), []byte(callerHash)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden: invalid artisan token"}`))
			return
		}
	}

	s.mu.Lock()
	err = s.deleteCommunityProfile(uuid)
	s.mu.Unlock()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"community profile not found"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *CensusServer) handleCommunityMe(w http.ResponseWriter, r *http.Request) {
	token := extractArtisanToken(r)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: artisan token required"}`))
		return
	}

	s.mu.Lock()
	profile, err := s.findProfileByToken(token)
	s.mu.Unlock()

	if err != nil || profile == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized: invalid artisan token"}`))
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
	summary, err := loadCensusSummary(s.dataPath, s.getCommunityDir())
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



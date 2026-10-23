package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const maxBodyBytes = 16 * 1024 // 16KB

type DistroInfo struct {
	ID       string `json:"id"`
	Like     string `json:"like"`
	Codename string `json:"codename"`
	Release  string `json:"release"`
}

type SystemInfo struct {
	Arch          string `json:"arch"`
	Desktop       string `json:"desktop"`
	DisplayServer string `json:"display_server"`
	Virt          string `json:"virt"`
	Init          string `json:"init"`
}

type OptionsInfo struct {
	Clone   bool `json:"clone"`
	Coexist bool `json:"coexist"`
}

var (
	emailForbiddenPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	uuidSafePattern       = regexp.MustCompile(`^[a-zA-Z0-9_\-]{8,64}$`)
)

type CommunityProfile struct {
	UUID      string            `json:"uuid"`
	Nickname  string            `json:"nickname,omitempty"`
	Location  string            `json:"location,omitempty"`
	Bio       string            `json:"bio,omitempty"`
	Contact   string            `json:"contact,omitempty"`
	Socials   map[string]string `json:"socials,omitempty"`
	SourceURL string            `json:"source_url,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
}

type ArtisanRemasterInfo struct {
	Distro         DistroInfo `json:"distro"`
	System         SystemInfo `json:"system"`
	Country        string     `json:"country,omitempty"`
	Timestamp      string     `json:"timestamp,omitempty"`
	LastRemasterAt string     `json:"last_remaster_at,omitempty"`
	Count          int        `json:"count"`
}

type ArtisanProfile struct {
	UUID           string               `json:"uuid"`
	Nickname       string               `json:"nickname,omitempty"`
	Location       string               `json:"location,omitempty"`
	Bio            string               `json:"bio,omitempty"`
	Contact        string               `json:"contact,omitempty"`
	Socials        map[string]string    `json:"socials,omitempty"`
	SourceURL      string               `json:"source_url,omitempty"`
	Enabled        *bool                `json:"enabled,omitempty"`
	UpdatedAt      string               `json:"updated_at,omitempty"`
	Country        string               `json:"country,omitempty"`
	LastRemasterAt string               `json:"last_remaster_at,omitempty"`
	RemasterCount  int                  `json:"remaster_count,omitempty"`
	LatestRemaster *ArtisanRemasterInfo `json:"latest_remaster,omitempty"`
}

type SocialLink struct {
	Platform string
	Label    string
	URL      string
}

type rawCommunityPayload struct {
	UUID          string               `json:"uuid"`
	Nickname      string               `json:"nickname"`
	Location      string               `json:"location"`
	Bio           string               `json:"bio"`
	Contact       string               `json:"contact"`
	Social        interface{}          `json:"social"`
	Socials       map[string]string    `json:"socials"`
	SourceURL     string               `json:"source_url"`
	URL           string               `json:"url"`
	Website       string               `json:"website"`
	RepositoryURL string               `json:"repository_url"`
	RepoURL       string               `json:"repo_url"`
	Enabled       *bool                `json:"enabled"`
	Community     *rawCommunityPayload `json:"community"`
}

type CensusRecord struct {
	Timestamp   string      `json:"timestamp"`
	Country     string      `json:"country"`
	UUID        string      `json:"uuid"`
	ParentID    string      `json:"parent_id,omitempty"`
	EggsVersion string      `json:"eggs_version"`
	Action      string      `json:"action"`
	Distro      DistroInfo  `json:"distro"`
	System      SystemInfo  `json:"system"`
	Options     OptionsInfo `json:"options"`
}

type RecentEvent struct {
	Date            string `json:"date"`
	Country         string `json:"country"`
	Distro          string `json:"distro"`
	Desktop         string `json:"desktop"`
	DisplayServer   string `json:"display_server"`
	Remaster        bool   `json:"remaster"`
	Sysinstall      bool   `json:"sysinstall"`
	ArtisanNickname string `json:"artisan_nickname,omitempty"`
	ArtisanURL      string `json:"artisan_url,omitempty"`
}

type TopInstallation struct {
	Country       string `json:"country"`
	Distro        string `json:"distro"`
	Desktop       string `json:"desktop"`
	DisplayServer string `json:"display_server"`
	Arch          string `json:"arch"`
	EggsVersion   string `json:"eggs_version"`
	SumSysinstall int    `json:"sum_sysinstall"`
}

type RepoClientStats struct {
	Hits       int
	Families   map[string]bool
	HasIndex   bool
	HasPackage bool
}

type RepoAnalysisSummary struct {
	GeneratedAt   string         `json:"generated_at,omitempty"`
	TotalLogLines int            `json:"total_log_lines"`
	ValidHits     int            `json:"valid_hits"`
	IgnoredBots   int            `json:"ignored_bots"`
	UniqueIPs     int            `json:"unique_ips"`
	ByFamily      map[string]int `json:"by_family"`
	ByCountry     map[string]int `json:"by_country"`
}

type CensusSummary struct {
	TotalEvents      int                  `json:"total_events"`
	UniqueMachines   int                  `json:"unique_machines"`
	TotalCountries   int                  `json:"total_countries"`
	TotalDistros     int                  `json:"total_distros"`
	CountryCount     int                  `json:"country,omitempty"`
	DistroCount      int                  `json:"distro,omitempty"`
	RemasterClones   int                  `json:"remaster_clones"`
	SysinstallClones int                  `json:"sysinstall_clones"`
	CoexistCount     int                  `json:"coexist_count"`
	RecentEvents     []RecentEvent        `json:"recent_events"`
	TopInstallations []TopInstallation    `json:"top_installations"`
	Artisans         []ArtisanProfile     `json:"artisans,omitempty"`
	RepoClients      *RepoAnalysisSummary `json:"repo_clients,omitempty"`
	ByAction         map[string]int       `json:"by_action"`
	ByDistro         map[string]int       `json:"by_distro"`
	ByArch           map[string]int       `json:"by_arch"`
	ByDesktop        map[string]int       `json:"by_desktop"`
	ByDisplayServer  map[string]int       `json:"by_display_server"`
	ByVirt           map[string]int       `json:"by_virt"`
	ByInit           map[string]int       `json:"by_init"`
	ByCountry        map[string]int       `json:"by_country"`
	ByVersion        map[string]int       `json:"by_version"`
}

type TableRow struct {
	Key     string
	Count   int
	Percent float64
}

type mmdbCountryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

type CensusServer struct {
	mu           sync.Mutex
	dataPath     string
	mmdbPath     string
	mmdb         *maxminddb.Reader
	communityDir string
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
	s := &CensusServer{
		dataPath:     dataPath,
		mmdbPath:     mmdbPath,
		communityDir: commDir,
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

func (s *CensusServer) saveCommunityProfile(profile CommunityProfile) error {
	dir := s.getCommunityDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	target := filepath.Join(dir, fmt.Sprintf("%s.json", profile.UUID))
	tmpFile := target + ".tmp"

	if err := os.WriteFile(tmpFile, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmpFile, target)
}

func (s *CensusServer) getCommunityProfile(uuid string) (*CommunityProfile, error) {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", uuid))

	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}

	var p CommunityProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *CensusServer) listCommunityProfiles() ([]CommunityProfile, error) {
	dir := s.getCommunityDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CommunityProfile{}, nil
		}
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	profiles := make([]CommunityProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err == nil && p.UUID != "" {
			profiles = append(profiles, p)
		}
	}
	return profiles, nil
}

func (s *CensusServer) deleteCommunityProfile(uuid string) error {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", uuid))
	return os.Remove(target)
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
		if raw.UUID == "" {
			raw.UUID = raw.Community.UUID
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

	finalUUID := strings.TrimSpace(raw.UUID)
	if pathUUID != "" {
		if finalUUID != "" && !strings.EqualFold(finalUUID, pathUUID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"uuid in path does not match uuid in payload"}`))
			return
		}
		finalUUID = pathUUID
	}

	if !isValidUUID(finalUUID) {
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

	profile := CommunityProfile{
		UUID:      finalUUID,
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp, _ := json.Marshal(map[string]string{
		"status": "ok",
		"uuid":   profile.UUID,
	})
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
		_, _ = w.Write([]byte(renderCommunityHTML(artisans)))
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
	err := s.deleteCommunityProfile(uuid)
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

func (s *CensusServer) appendLine(line []byte) error {
	target := s.dataPath
	fi, err := os.Stat(target)
	if err == nil && fi.IsDir() {
		target = filepath.Join(target, "census.jsonl")
	}
	dir := filepath.Dir(target)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(line)
	return err
}

func forEachLogLine(dataPath string, handleLine func(line []byte)) error {
	info, err := os.Stat(dataPath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		entries, err := os.ReadDir(dataPath)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			filePath := filepath.Join(dataPath, entry.Name())
			if err := scanSingleLogFile(filePath, handleLine); err != nil {
				return err
			}
		}
		return nil
	}

	return scanSingleLogFile(dataPath, handleLine)
}

func scanSingleLogFile(filePath string, handleLine func(line []byte)) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		handleLine(line)
	}
	return scanner.Err()
}

func isCommunityProfileEnabled(p CommunityProfile) bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

func parseFlexibleTimestamp(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

func countryToFlag(countryCode string) string {
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	if countryCode == "XX" || countryCode == "" {
		return ""
	}
	if len(countryCode) == 2 && countryCode[0] >= 'A' && countryCode[0] <= 'Z' && countryCode[1] >= 'A' && countryCode[1] <= 'Z' {
		r1 := rune(0x1F1E6 + int(countryCode[0]-'A'))
		r2 := rune(0x1F1E6 + int(countryCode[1]-'A'))
		return string([]rune{r1, r2})
	}
	return ""
}

func isSafeURL(u string) bool {
	u = strings.TrimSpace(u)
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func formatDistroBadge(distro DistroInfo, sys SystemInfo) string {
	id := strings.TrimSpace(distro.ID)
	codename := strings.TrimSpace(distro.Codename)
	release := strings.TrimSpace(distro.Release)
	arch := strings.TrimSpace(sys.Arch)
	desktop := strings.TrimSpace(sys.Desktop)

	if id == "" {
		id = "linux"
	}

	var versionParts []string
	if codename != "" && release != "" {
		if strings.EqualFold(codename, release) {
			versionParts = append(versionParts, codename)
		} else {
			versionParts = append(versionParts, fmt.Sprintf("%s (%s)", codename, release))
		}
	} else if codename != "" {
		versionParts = append(versionParts, codename)
	} else if release != "" {
		versionParts = append(versionParts, release)
	}

	distroStr := id
	if len(versionParts) > 0 {
		distroStr += " " + strings.Join(versionParts, " ")
	}

	var parts []string
	parts = append(parts, distroStr)

	if arch != "" {
		parts = append(parts, arch)
	}
	if desktop != "" {
		parts = append(parts, desktop)
	} else {
		parts = append(parts, "headless/cli")
	}

	return strings.Join(parts, " • ")
}

func buildSocialLinks(socials map[string]string, contact string) []SocialLink {
	var links []SocialLink
	seenURLs := make(map[string]bool)

	keys := make([]string, 0, len(socials))
	for k := range socials {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		val := strings.TrimSpace(socials[k])
		if val == "" {
			continue
		}
		platform := strings.ToLower(strings.TrimSpace(k))
		var url string
		var label string

		switch platform {
		case "github":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://github.com/" + handle
			}
			label = "GitHub"
		case "gitlab":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://gitlab.com/" + handle
			}
			label = "GitLab"
		case "telegram", "tg":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://t.me/" + handle
			}
			label = "Telegram"
		case "mastodon":
			if isSafeURL(val) {
				url = val
			} else if strings.HasPrefix(val, "@") && strings.Contains(val[1:], "@") {
				parts := strings.Split(val[1:], "@")
				url = "https://" + parts[1] + "/@" + parts[0]
			}
			label = "Mastodon"
		case "twitter", "x":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://x.com/" + handle
			}
			label = "X (Twitter)"
		case "matrix":
			if isSafeURL(val) {
				url = val
			} else {
				url = "https://matrix.to/#/" + val
			}
			label = "Matrix"
		case "sourceforge":
			if isSafeURL(val) {
				url = val
			} else {
				url = "https://sourceforge.net/projects/" + val
			}
			label = "SourceForge"
		default:
			if isSafeURL(val) {
				url = val
				label = strings.Title(platform)
			}
		}

		if url != "" && isSafeURL(url) && !seenURLs[url] {
			seenURLs[url] = true
			links = append(links, SocialLink{
				Platform: platform,
				Label:    label,
				URL:      url,
			})
		}
	}

	contact = strings.TrimSpace(contact)
	if contact != "" {
		if isSafeURL(contact) && !seenURLs[contact] {
			seenURLs[contact] = true
			links = append(links, SocialLink{
				Platform: "contact",
				Label:    "Contact",
				URL:      contact,
			})
		} else if strings.HasPrefix(contact, "@") && !strings.Contains(contact[1:], "@") {
			tgURL := "https://t.me/" + strings.TrimPrefix(contact, "@")
			if !seenURLs[tgURL] {
				seenURLs[tgURL] = true
				links = append(links, SocialLink{
					Platform: "telegram",
					Label:    "Telegram (" + contact + ")",
					URL:      tgURL,
				})
			}
		}
	}

	return links
}

func getArtisanActivityTime(a *ArtisanProfile) time.Time {
	tUp := parseFlexibleTimestamp(a.UpdatedAt)
	tRem := parseFlexibleTimestamp(a.LastRemasterAt)
	if tRem.After(tUp) {
		return tRem
	}
	return tUp
}

func sortArtisansByActivity(artisans []ArtisanProfile) {
	sort.Slice(artisans, func(i, j int) bool {
		ti := getArtisanActivityTime(&artisans[i])
		tj := getArtisanActivityTime(&artisans[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		if artisans[i].Nickname != artisans[j].Nickname {
			return artisans[i].Nickname < artisans[j].Nickname
		}
		return artisans[i].UUID < artisans[j].UUID
	})
}

func (s *CensusServer) enrichSingleProfile(profile CommunityProfile, dataPath string) ArtisanProfile {
	artisan := ArtisanProfile{
		UUID:      profile.UUID,
		Nickname:  profile.Nickname,
		Location:  profile.Location,
		Bio:       profile.Bio,
		Contact:   profile.Contact,
		Socials:   profile.Socials,
		SourceURL: profile.SourceURL,
		Enabled:   profile.Enabled,
		UpdatedAt: profile.UpdatedAt,
	}

	if dataPath == "" {
		return artisan
	}

	_ = forEachLogLine(dataPath, func(line []byte) {
		var rec CensusRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return
		}
		if strings.TrimSpace(rec.UUID) != profile.UUID {
			return
		}

		if rec.Country != "" {
			artisan.Country = strings.TrimSpace(rec.Country)
		}

		if rec.Action == "remaster" {
			artisan.RemasterCount++
			tRec := parseFlexibleTimestamp(rec.Timestamp)
			tOld := parseFlexibleTimestamp(artisan.LastRemasterAt)
			isNewer := false
			if artisan.LastRemasterAt == "" {
				isNewer = true
			} else if !tRec.IsZero() && !tOld.IsZero() {
				isNewer = tRec.After(tOld) || tRec.Equal(tOld)
			} else {
				isNewer = rec.Timestamp >= artisan.LastRemasterAt
			}

			if isNewer {
				artisan.LastRemasterAt = rec.Timestamp
				if rec.Country != "" {
					artisan.Country = strings.TrimSpace(rec.Country)
				}
				artisan.LatestRemaster = &ArtisanRemasterInfo{
					Distro:         rec.Distro,
					System:         rec.System,
					Country:        artisan.Country,
					Timestamp:      rec.Timestamp,
					LastRemasterAt: rec.Timestamp,
					Count:          artisan.RemasterCount,
				}
			}
		}
	})

	if artisan.LatestRemaster != nil {
		artisan.LatestRemaster.Count = artisan.RemasterCount
	}

	return artisan
}

func loadEnrichedArtisans(dataPath, commDir string) ([]ArtisanProfile, error) {
	if commDir == "" {
		return []ArtisanProfile{}, nil
	}

	entries, err := os.ReadDir(commDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ArtisanProfile{}, nil
		}
		return nil, err
	}

	artisanMap := make(map[string]*ArtisanProfile)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(commDir, entry.Name()))
		if err != nil {
			continue
		}
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err != nil || p.UUID == "" {
			continue
		}
		if !isCommunityProfileEnabled(p) {
			continue
		}

		artisanMap[p.UUID] = &ArtisanProfile{
			UUID:      p.UUID,
			Nickname:  p.Nickname,
			Location:  p.Location,
			Bio:       p.Bio,
			Contact:   p.Contact,
			Socials:   p.Socials,
			SourceURL: p.SourceURL,
			Enabled:   p.Enabled,
			UpdatedAt: p.UpdatedAt,
		}
	}

	if len(artisanMap) == 0 {
		return []ArtisanProfile{}, nil
	}

	if dataPath != "" {
		_ = forEachLogLine(dataPath, func(line []byte) {
			var rec CensusRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return
			}
			uuid := strings.TrimSpace(rec.UUID)
			if uuid == "" {
				return
			}
			artisan, exists := artisanMap[uuid]
			if !exists {
				return
			}

			if rec.Country != "" {
				artisan.Country = strings.TrimSpace(rec.Country)
			}

			if rec.Action == "remaster" {
				artisan.RemasterCount++
				tRec := parseFlexibleTimestamp(rec.Timestamp)
				tOld := parseFlexibleTimestamp(artisan.LastRemasterAt)
				isNewer := false
				if artisan.LastRemasterAt == "" {
					isNewer = true
				} else if !tRec.IsZero() && !tOld.IsZero() {
					isNewer = tRec.After(tOld) || tRec.Equal(tOld)
				} else {
					isNewer = rec.Timestamp >= artisan.LastRemasterAt
				}

				if isNewer {
					artisan.LastRemasterAt = rec.Timestamp
					if rec.Country != "" {
						artisan.Country = strings.TrimSpace(rec.Country)
					}
					artisan.LatestRemaster = &ArtisanRemasterInfo{
						Distro:         rec.Distro,
						System:         rec.System,
						Country:        artisan.Country,
						Timestamp:      rec.Timestamp,
						LastRemasterAt: rec.Timestamp,
						Count:          artisan.RemasterCount,
					}
				}
			}
		})

		for _, a := range artisanMap {
			if a.LatestRemaster != nil {
				a.LatestRemaster.Count = a.RemasterCount
			}
		}
	}

	artisans := make([]ArtisanProfile, 0, len(artisanMap))
	for _, a := range artisanMap {
		artisans = append(artisans, *a)
	}

	sortArtisansByActivity(artisans)
	return artisans, nil
}

func loadCensusSummary(dataPath string, commDirs ...string) (CensusSummary, error) {
	var commDir string
	if len(commDirs) > 0 && commDirs[0] != "" {
		commDir = commDirs[0]
	} else if dataPath != "" {
		fi, err := os.Stat(dataPath)
		if err == nil && fi.IsDir() {
			commDir = filepath.Join(dataPath, "community")
		} else {
			commDir = filepath.Join(filepath.Dir(dataPath), "community")
		}
	} else {
		commDir = "/var/lib/penguins-census/community"
	}

	activeArtisansMap := make(map[string]CommunityProfile)
	if commDir != "" {
		if entries, err := os.ReadDir(commDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(commDir, entry.Name()))
				if err != nil {
					continue
				}
				var p CommunityProfile
				if err := json.Unmarshal(data, &p); err == nil && p.UUID != "" && isCommunityProfileEnabled(p) {
					activeArtisansMap[p.UUID] = p
				}
			}
		}
	}

	summary := CensusSummary{
		RecentEvents:     make([]RecentEvent, 0),
		TopInstallations: make([]TopInstallation, 0),
		ByAction:         make(map[string]int),
		ByDistro:         make(map[string]int),
		ByArch:           make(map[string]int),
		ByDesktop:        make(map[string]int),
		ByDisplayServer:  make(map[string]int),
		ByVirt:           make(map[string]int),
		ByInit:           make(map[string]int),
		ByCountry:        make(map[string]int),
		ByVersion:        make(map[string]int),
	}
	uniqueUUIDs := make(map[string]struct{})
	uniqueSysinstalls := make(map[string]CensusRecord)
	var lastRecords []CensusRecord

	err := forEachLogLine(dataPath, func(line []byte) {
		var rec CensusRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return
		}
		lastRecords = append(lastRecords, rec)
		if len(lastRecords) > 10 {
			lastRecords = lastRecords[1:]
		}
		summary.TotalEvents++
		uuid := strings.TrimSpace(rec.UUID)
		if uuid != "" {
			uniqueUUIDs[uuid] = struct{}{}
			if rec.Action == "sysinstall" {
				uniqueSysinstalls[uuid] = rec
			}
		}
		if rec.Action != "" {
			summary.ByAction[rec.Action]++
		}
		if rec.Distro.ID != "" {
			summary.ByDistro[rec.Distro.ID]++
		}
		if rec.System.Arch != "" {
			summary.ByArch[rec.System.Arch]++
		}
		desktop := strings.TrimSpace(rec.System.Desktop)
		if desktop == "" {
			desktop = "headless/cli"
		}
		summary.ByDesktop[desktop]++

		displayServer := strings.TrimSpace(rec.System.DisplayServer)
		if displayServer == "" {
			displayServer = "headless/cli"
		}
		summary.ByDisplayServer[displayServer]++

		virt := strings.TrimSpace(rec.System.Virt)
		if virt == "" || virt == "none" || virt == "physical/none" {
			virt = "bare-metal"
		}
		summary.ByVirt[virt]++

		if rec.System.Init != "" {
			summary.ByInit[rec.System.Init]++
		}
		if rec.Options.Clone {
			if rec.Action == "remaster" {
				summary.RemasterClones++
			} else if rec.Action == "sysinstall" {
				summary.SysinstallClones++
			}
		}
		if rec.Options.Coexist {
			summary.CoexistCount++
		}
		if rec.Country != "" {
			summary.ByCountry[rec.Country]++
		}
		if rec.EggsVersion != "" {
			summary.ByVersion[rec.EggsVersion]++
		}
	})

	if err != nil && !os.IsNotExist(err) {
		return summary, err
	}

	for i := len(lastRecords) - 1; i >= 0; i-- {
		r := lastRecords[i]
		country := strings.TrimSpace(r.Country)
		if country == "" {
			country = "XX"
		}
		distro := strings.TrimSpace(r.Distro.ID)
		if distro == "" {
			distro = "unknown"
		}
		desktop := strings.TrimSpace(r.System.Desktop)
		if desktop == "" {
			desktop = "headless/cli"
		}
		displayServer := strings.TrimSpace(r.System.DisplayServer)
		if displayServer == "" {
			displayServer = "headless/cli"
		}
		ev := RecentEvent{
			Date:          formatEventDate(r.Timestamp),
			Country:       country,
			Distro:        distro,
			Desktop:       desktop,
			DisplayServer: displayServer,
			Remaster:      r.Action == "remaster",
			Sysinstall:    r.Action == "sysinstall",
		}
		recUUID := strings.TrimSpace(r.UUID)
		if artisan, ok := activeArtisansMap[recUUID]; ok {
			ev.ArtisanNickname = artisan.Nickname
			if isSafeURL(artisan.SourceURL) {
				ev.ArtisanURL = artisan.SourceURL
			} else {
				ev.ArtisanURL = "/api/community"
			}
		}
		summary.RecentEvents = append(summary.RecentEvents, ev)
	}

	summary.UniqueMachines = len(uniqueUUIDs)
	summary.TotalCountries = len(summary.ByCountry)
	summary.TotalDistros = len(summary.ByDistro)
	summary.CountryCount = summary.TotalCountries
	summary.DistroCount = summary.TotalDistros

	// Aggregate unique sysinstall installations
	type groupKey struct {
		country       string
		distro        string
		desktop       string
		displayServer string
		arch          string
		eggsVersion   string
	}
	groupCounts := make(map[groupKey]int)
	for _, r := range uniqueSysinstalls {
		country := strings.TrimSpace(r.Country)
		if country == "" {
			country = "XX"
		}
		distro := strings.TrimSpace(r.Distro.ID)
		if distro == "" {
			distro = "unknown"
		}
		desktop := strings.TrimSpace(r.System.Desktop)
		if desktop == "" {
			desktop = "headless/cli"
		}
		displayServer := strings.TrimSpace(r.System.DisplayServer)
		if displayServer == "" {
			displayServer = "headless/cli"
		}
		arch := strings.TrimSpace(r.System.Arch)
		if arch == "" {
			arch = "unknown"
		}
		ver := strings.TrimSpace(r.EggsVersion)
		if ver == "" {
			ver = "unknown"
		}
		key := groupKey{
			country:       country,
			distro:        distro,
			desktop:       desktop,
			displayServer: displayServer,
			arch:          arch,
			eggsVersion:   ver,
		}
		groupCounts[key]++
	}

	topList := make([]TopInstallation, 0, len(groupCounts))
	for k, count := range groupCounts {
		topList = append(topList, TopInstallation{
			Country:       k.country,
			Distro:        k.distro,
			Desktop:       k.desktop,
			DisplayServer: k.displayServer,
			Arch:          k.arch,
			EggsVersion:   k.eggsVersion,
			SumSysinstall: count,
		})
	}

	sort.Slice(topList, func(i, j int) bool {
		if topList[i].SumSysinstall != topList[j].SumSysinstall {
			return topList[i].SumSysinstall > topList[j].SumSysinstall
		}
		if topList[i].Distro != topList[j].Distro {
			return topList[i].Distro < topList[j].Distro
		}
		if topList[i].Country != topList[j].Country {
			return topList[i].Country < topList[j].Country
		}
		if topList[i].Desktop != topList[j].Desktop {
			return topList[i].Desktop < topList[j].Desktop
		}
		if topList[i].DisplayServer != topList[j].DisplayServer {
			return topList[i].DisplayServer < topList[j].DisplayServer
		}
		if topList[i].Arch != topList[j].Arch {
			return topList[i].Arch < topList[j].Arch
		}
		return topList[i].EggsVersion < topList[j].EggsVersion
	})

	if len(topList) > 10 {
		topList = topList[:10]
	}
	summary.TopInstallations = topList

	repoStatsPath := filepath.Join(filepath.Dir(dataPath), "repo-clients.json")
	if fi, err := os.Stat(dataPath); err == nil && fi.IsDir() {
		repoStatsPath = filepath.Join(dataPath, "repo-clients.json")
	}
	if rStats, err := loadRepoAnalysisSummary(repoStatsPath); err == nil {
		summary.RepoClients = rStats
	}

	if commDir != "" {
		if artisans, err := loadEnrichedArtisans(dataPath, commDir); err == nil {
			summary.Artisans = artisans
		}
	}

	return summary, nil
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

func sortedRows(m map[string]int, total int) []TableRow {
	rows := make([]TableRow, 0, len(m))
	for k, v := range m {
		pct := 0.0
		if total > 0 {
			pct = (float64(v) * 100.0) / float64(total)
		}
		rows = append(rows, TableRow{
			Key:     k,
			Count:   v,
			Percent: pct,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

func writeMarkdownTable(buf *bytes.Buffer, title, colName string, m map[string]int, total int) {
	rows := sortedRows(m, total)
	fmt.Fprintf(buf, "### %s\n\n", title)

	keyWidth := len(colName)
	if len(rows) == 0 && len("(no data)") > keyWidth {
		keyWidth = len("(no data)")
	}
	for _, r := range rows {
		if len(r.Key) > keyWidth {
			keyWidth = len(r.Key)
		}
	}

	fmt.Fprintf(buf, "| %-*s | %7s | %6s |\n", keyWidth, colName, "Count", "Share")
	fmt.Fprintf(buf, "|-%s-|--------:|-------:|\n", strings.Repeat("-", keyWidth))
	if len(rows) == 0 {
		fmt.Fprintf(buf, "| %-*s | %7d | %5.1f%% |\n\n", keyWidth, "(no data)", 0, 0.0)
		return
	}
	for _, r := range rows {
		fmt.Fprintf(buf, "| %-*s | %7d | %5.1f%% |\n", keyWidth, r.Key, r.Count, r.Percent)
	}
	buf.WriteString("\n")
}

func renderTextSummary(s CensusSummary) string {
	var buf bytes.Buffer
	buf.WriteString("# Penguins Eggs Census Report\n\n")
	fmt.Fprintf(&buf, "- Total Events:        %d\n", s.TotalEvents)
	fmt.Fprintf(&buf, "- Unique Machines:     %d\n", s.UniqueMachines)
	fmt.Fprintf(&buf, "- Remasters/Clones:    %d/%d\n", s.ByAction["remaster"], s.RemasterClones)
	fmt.Fprintf(&buf, "- Sysinstalls/Clones:  %d/%d\n\n", s.ByAction["sysinstall"], s.SysinstallClones)

	writeMarkdownTable(&buf, "Country", "Country", s.ByCountry, s.TotalEvents)
	writeMarkdownTable(&buf, "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeMarkdownTable(&buf, "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeMarkdownTable(&buf, "Display Server", "Display Server", s.ByDisplayServer, s.TotalEvents)
	writeMarkdownTable(&buf, "Virt", "Virt", s.ByVirt, s.TotalEvents)
	writeMarkdownTable(&buf, "Init", "Init", s.ByInit, s.TotalEvents)
	writeMarkdownTable(&buf, "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeMarkdownTable(&buf, "Version", "Version", s.ByVersion, s.TotalEvents)

	writeMarkdownTopInstallations(&buf, s.TopInstallations)
	writeMarkdownRecentEvents(&buf, s.RecentEvents)

	if s.RepoClients != nil && s.RepoClients.UniqueIPs > 0 {
		totalOps := s.ByAction["remaster"] + s.ByAction["sysinstall"]
		writeMarkdownRepoClients(&buf, *s.RepoClients, s.UniqueMachines, totalOps)
	}

	return buf.String()
}

func writeMarkdownRepoClients(buf *bytes.Buffer, r RepoAnalysisSummary, censusMachines, completedOps int) {
	fmt.Fprintf(buf, "### Unique IP last 15 days\n\n")
	if r.GeneratedAt != "" {
		fmt.Fprintf(buf, "- Updated:                 %s\n", r.GeneratedAt)
	}
	fmt.Fprintf(buf, "- Unique IPs (15 days):    %d\n", r.UniqueIPs)
	fmt.Fprintf(buf, "- Census Active Machines:  %d\n", censusMachines)
	fmt.Fprintf(buf, "- Completed Operations:    %d\n\n", completedOps)

	writeMarkdownTable(buf, "Package Managers", "Family", r.ByFamily, r.UniqueIPs)
	writeMarkdownTable(buf, "Repo Clients by Country", "Country", r.ByCountry, r.UniqueIPs)
}

func writeMarkdownTopInstallations(buf *bytes.Buffer, list []TopInstallation) {
	fmt.Fprintf(buf, "### Top 10 Sysinstalls\n\n")

	countryW := len("Country")
	distroW := len("Distro")
	desktopW := len("Desktop")
	dispW := len("Display Server")
	archW := len("Arch")
	verW := len("Version")
	sumW := len("Sysinstalls")

	if len(list) == 0 {
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*s |\n",
			countryW, "Country",
			distroW, "Distro",
			desktopW, "Desktop",
			dispW, "Display Server",
			archW, "Arch",
			verW, "Version",
			sumW, "Sysinstalls")
		fmt.Fprintf(buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s:|\n",
			strings.Repeat("-", countryW),
			strings.Repeat("-", distroW),
			strings.Repeat("-", desktopW),
			strings.Repeat("-", dispW),
			strings.Repeat("-", archW),
			strings.Repeat("-", verW),
			strings.Repeat("-", sumW-1))
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*d |\n\n",
			countryW, "(no data)",
			distroW, "-",
			desktopW, "-",
			dispW, "-",
			archW, "-",
			verW, "-",
			sumW, 0)
		return
	}

	for _, item := range list {
		if len(item.Country) > countryW {
			countryW = len(item.Country)
		}
		if len(item.Distro) > distroW {
			distroW = len(item.Distro)
		}
		if len(item.Desktop) > desktopW {
			desktopW = len(item.Desktop)
		}
		if len(item.DisplayServer) > dispW {
			dispW = len(item.DisplayServer)
		}
		if len(item.Arch) > archW {
			archW = len(item.Arch)
		}
		if len(item.EggsVersion) > verW {
			verW = len(item.EggsVersion)
		}
	}

	fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*s |\n",
		countryW, "Country",
		distroW, "Distro",
		desktopW, "Desktop",
		dispW, "Display Server",
		archW, "Arch",
		verW, "Version",
		sumW, "Sysinstalls")
	fmt.Fprintf(buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s:|\n",
		strings.Repeat("-", countryW),
		strings.Repeat("-", distroW),
		strings.Repeat("-", desktopW),
		strings.Repeat("-", dispW),
		strings.Repeat("-", archW),
		strings.Repeat("-", verW),
		strings.Repeat("-", sumW-1))

	for _, item := range list {
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*d |\n",
			countryW, item.Country,
			distroW, item.Distro,
			desktopW, item.Desktop,
			dispW, item.DisplayServer,
			archW, item.Arch,
			verW, item.EggsVersion,
			sumW, item.SumSysinstall)
	}
	buf.WriteString("\n")
}

func formatEventDate(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return "-"
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return ts
}

func writeMarkdownRecentEvents(buf *bytes.Buffer, events []RecentEvent) {
	fmt.Fprintf(buf, "### Recent Events\n\n")
	fmt.Fprintf(buf, "| Date                | Country | Distro | Desktop | Display Server | Remaster | Sysinstall |\n")
	fmt.Fprintf(buf, "|---------------------|---------|--------|---------|----------------|:--------:|:----------:|\n")
	if len(events) == 0 {
		fmt.Fprintf(buf, "| (no data)           | -       | -      | -       | -              | -        | -          |\n\n")
		return
	}
	for _, ev := range events {
		rem := "-"
		if ev.Remaster {
			rem = "✓"
		}
		sys := "-"
		if ev.Sysinstall {
			sys = "✓"
		}
		date := ev.Date
		if date == "" {
			date = "-"
		}
		fmt.Fprintf(buf, "| %-19s | %-7s | %-6s | %-7s | %-14s | %-8s | %-10s |\n", date, ev.Country, ev.Distro, ev.Desktop, ev.DisplayServer, rem, sys)
	}
	buf.WriteString("\n")
}

func writeHTMLTableCard(buf *bytes.Buffer, id, title, colName string, m map[string]int, total int, aliases ...string) {
	rows := sortedRows(m, total)
	fmt.Fprintf(buf, `<section class="panel" id="%s">`, html.EscapeString(id))
	for _, alias := range aliases {
		if alias != "" {
			fmt.Fprintf(buf, `<span id="%s" class="anchor-alias"></span>`, html.EscapeString(alias))
		}
	}
	fmt.Fprintf(buf, `<h2><a href="#%s" class="anchor-link">%s</a></h2><table><thead><tr><th>%s</th><th class="num">Count</th><th class="num">Share</th></tr></thead><tbody>`,
		html.EscapeString(id), html.EscapeString(title), html.EscapeString(colName))
	if len(rows) == 0 {
		buf.WriteString(`<tr><td colspan="3" class="empty">No events recorded yet</td></tr>`)
	} else {
		for _, r := range rows {
			fmt.Fprintf(buf, `<tr><td><span class="label">%s</span><div class="bar"><div class="fill" style="width:%.1f%%"></div></div></td><td class="num">%d</td><td class="num">%.1f%%</td></tr>`,
				html.EscapeString(r.Key), r.Percent, r.Count, r.Percent)
		}
	}
	buf.WriteString(`</tbody></table></section>`)
}

func writeHTMLRecentEvents(buf *bytes.Buffer, events []RecentEvent) {
	buf.WriteString(`  <section class="panel" id="recent-events">
    <h2><a href="#recent-events" class="anchor-link">Recent Events</a></h2>
    <table>
      <thead>
        <tr>
          <th>Date</th>
          <th>Country</th>
          <th>Distro</th>
          <th>Desktop</th>
          <th>Display Server</th>
          <th class="center">Remaster</th>
          <th class="center">Sysinstall</th>
        </tr>
      </thead>
      <tbody>
`)
	if len(events) == 0 {
		buf.WriteString(`        <tr><td colspan="7" class="empty">No events recorded yet</td></tr>
`)
	} else {
		for _, e := range events {
			rem := `<span class="muted">&mdash;</span>`
			if e.Remaster {
				rem = `<span class="check">&#10003;</span>`
			}
			sys := `<span class="muted">&mdash;</span>`
			if e.Sysinstall {
				sys = `<span class="check">&#10003;</span>`
			}
			date := html.EscapeString(e.Date)
			if date == "" {
				date = `<span class="muted">&mdash;</span>`
			}
			distroCell := html.EscapeString(e.Distro)
			if e.ArtisanNickname != "" {
				nick := e.ArtisanNickname
				if !strings.HasPrefix(nick, "@") {
					nick = "@" + nick
				}
				artisanURL := e.ArtisanURL
				if !isSafeURL(artisanURL) {
					artisanURL = "/api/community"
				}
				distroCell += fmt.Sprintf(` <a href="%s" class="artisan-badge" target="_blank" rel="noopener noreferrer" title="Distro Artisan">%s</a>`, html.EscapeString(artisanURL), html.EscapeString(nick))
			}
			fmt.Fprintf(buf, `        <tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="center">%s</td><td class="center">%s</td></tr>
`, date, html.EscapeString(e.Country), distroCell, html.EscapeString(e.Desktop), html.EscapeString(e.DisplayServer), rem, sys)
		}
	}
	buf.WriteString(`      </tbody>
    </table>
  </section>
`)
}

func writeHTMLCommunityArtisans(buf *bytes.Buffer, artisans []ArtisanProfile) {
	buf.WriteString(`  <section class="panel" id="community-artisans">
    <span id="community" class="anchor-alias"></span>
    <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:1rem; flex-wrap:wrap; gap:0.5rem;">
      <h2 style="margin:0;"><a href="/api/community" class="anchor-link">🐧 Penguins Community Artisans</a></h2>
      <a class="btn" href="/api/community">View Showcase &rarr;</a>
    </div>
`)
	if len(artisans) == 0 {
		buf.WriteString(`    <div class="empty">No community artisans registered yet &bull; <a href="/api/community" style="color:var(--accent);">Join Penguins Community</a></div>
  </section>
`)
		return
	}

	buf.WriteString(`    <table>
      <thead>
        <tr>
          <th>Artisan</th>
          <th>Location</th>
          <th>Latest ISO</th>
          <th class="num">Remasters</th>
          <th>Source</th>
        </tr>
      </thead>
      <tbody>
`)
	for _, a := range artisans {
		nick := strings.TrimSpace(a.Nickname)
		if nick == "" {
			nick = "artisan"
		}
		if !strings.HasPrefix(nick, "@") {
			nick = "@" + nick
		}

		country := strings.ToUpper(strings.TrimSpace(a.Country))
		flag := countryToFlag(country)
		loc := strings.TrimSpace(a.Location)
		locDisplay := ""
		if loc != "" && flag != "" {
			locDisplay = fmt.Sprintf("%s %s", html.EscapeString(loc), flag)
		} else if loc != "" {
			locDisplay = html.EscapeString(loc)
		} else if flag != "" {
			locDisplay = fmt.Sprintf("%s %s", flag, html.EscapeString(country))
		} else if country != "" {
			locDisplay = html.EscapeString(country)
		} else {
			locDisplay = `<span class="muted">&mdash;</span>`
		}

		isoBadge := `<span class="muted">&mdash;</span>`
		if a.LatestRemaster != nil {
			badgeText := formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
			isoBadge = fmt.Sprintf(`<span class="iso-badge" style="font-size:0.8rem; padding:0.15rem 0.45rem;">%s</span>`, html.EscapeString(badgeText))
		}

		sourceLink := `<span class="muted">&mdash;</span>`
		if isSafeURL(a.SourceURL) {
			sourceLink = fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer" class="btn" style="padding:0.2rem 0.5rem; font-size:0.75rem;">Source &rarr;</a>`, html.EscapeString(a.SourceURL))
		}

		artisanLink := "/api/community"
		if isSafeURL(a.SourceURL) {
			artisanLink = a.SourceURL
		}

		fmt.Fprintf(buf, `        <tr>
          <td><a href="%s" target="_blank" rel="noopener noreferrer" style="color:var(--accent); font-weight:600; text-decoration:none;">%s</a></td>
          <td>%s</td>
          <td>%s</td>
          <td class="num">%d</td>
          <td>%s</td>
        </tr>
`, html.EscapeString(artisanLink), html.EscapeString(nick), locDisplay, isoBadge, a.RemasterCount, sourceLink)
	}

	buf.WriteString(`      </tbody>
    </table>
  </section>
`)
}

func renderHTMLSummary(s CensusSummary) string {
	var buf bytes.Buffer

	buf.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Penguins Eggs Census</title>
<style>
:root {
  --bg: #0f172a;
  --panel: #1e293b;
  --border: #334155;
  --text: #f8fafc;
  --muted: #94a3b8;
  --accent: #38bdf8;
  --accent-bg: rgba(56, 189, 248, 0.16);
}
* { box-sizing: border-box; }
html {
  scroll-behavior: smooth;
}
body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  background: var(--bg);
  color: var(--text);
  line-height: 1.5;
  padding: 2rem 1rem;
}
.container {
  max-width: 1180px;
  margin: 0 auto;
}
header {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  margin-bottom: 1.75rem;
  border-bottom: 1px solid var(--border);
  padding-bottom: 1rem;
}
h1 {
  margin: 0;
  font-size: 1.65rem;
}
.subtitle {
  color: var(--muted);
  font-size: 0.9rem;
  margin-top: 0.25rem;
}
.links {
  display: flex;
  gap: 0.6rem;
  flex-wrap: wrap;
}
.btn {
  display: inline-block;
  background: var(--panel);
  color: var(--text);
  border: 1px solid var(--border);
  padding: 0.45rem 0.85rem;
  border-radius: 6px;
  text-decoration: none;
  font-size: 0.85rem;
  cursor: pointer;
}
.btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 1rem;
  margin-bottom: 1.75rem;
}
.card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1rem 1.25rem;
  scroll-margin-top: 1.5rem;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.card:target, .panel:target {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px var(--accent-bg);
}
.card-label {
  color: var(--muted);
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.card-value {
  font-size: 1.8rem;
  font-weight: 700;
  color: var(--accent);
  margin-top: 0.2rem;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(330px, 1fr));
  gap: 1.25rem;
  margin-bottom: 2rem;
}
.panel {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1rem 1.25rem;
  scroll-margin-top: 1.5rem;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.panel h2 {
  margin: 0 0 0.75rem 0;
  font-size: 1.05rem;
  color: var(--text);
}
.anchor-link {
  color: inherit;
  text-decoration: none;
}
.anchor-link:hover {
  color: var(--accent);
}
.anchor-alias {
  display: block;
  position: relative;
  top: -1.5rem;
  height: 0;
  width: 0;
  visibility: hidden;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}
th, td {
  padding: 0.45rem 0.35rem;
  border-bottom: 1px solid var(--border);
  text-align: left;
  vertical-align: middle;
}
th {
  color: var(--muted);
  font-weight: 600;
  font-size: 0.78rem;
  text-transform: uppercase;
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.center {
  text-align: center;
}
.check {
  color: var(--accent);
  font-weight: bold;
}
.muted {
  color: var(--muted);
}
#recent-events {
  margin-bottom: 1.75rem;
}
.empty {
  color: var(--muted);
  text-align: center;
  padding: 1rem 0;
}
.bar {
  height: 4px;
  background: rgba(255,255,255,0.07);
  border-radius: 2px;
  margin-top: 4px;
  overflow: hidden;
}
.fill {
  height: 100%;
  background: var(--accent);
}
.artisan-badge {
  display: inline-block;
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.4);
  padding: 0.15rem 0.45rem;
  border-radius: 9999px;
  font-size: 0.75rem;
  font-weight: 600;
  text-decoration: none;
  margin-left: 0.4rem;
  transition: all 0.2s;
  vertical-align: middle;
}
.artisan-badge:hover {
  background: var(--accent);
  color: #0f172a;
}
.iso-badge {
  display: inline-block;
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.4);
  padding: 0.2rem 0.45rem;
  border-radius: 6px;
  font-size: 0.8rem;
  font-family: monospace;
  font-weight: 600;
}
</style>
</head>
<body>
<div class="container">
  <header>
    <div>
      <h1>Penguins Eggs Worldwide Usage</h1>
      <div class="subtitle">Since 22 October 2026 &bull; Privacy-respecting aggregated telemetry (no IP addresses stored)</div>
    </div>
    <div class="links">
      <a class="btn" href="/api/community">🐧 Distro Artisans</a>
      <a class="btn" href="https://penguins-eggs.net" target="_blank" rel="noopener">Home</a>
      <a class="btn" href="https://penguins-eggs.net/access/" target="_blank" rel="noopener">Site</a>
      <a class="btn" href="https://github.com/pieroproietti/penguins-eggs" target="_blank" rel="noopener">Repository</a>
`)
	if s.RepoClients != nil && s.RepoClients.UniqueIPs > 0 {
		buf.WriteString(`      <a class="btn" href="#unique-ips">Unique IPs</a>
`)
	}
	buf.WriteString(`      <a class="btn" href="?format=txt">Plain Text / Markdown</a>
      <a class="btn" href="?format=json">Raw JSON</a>
    </div>
  </header>
`)

	fmt.Fprintf(&buf, `  <div class="cards" id="overview">
    <div class="card" id="total-events"><div class="card-label">Total Events</div><div class="card-value">%d</div></div>
    <div class="card" id="unique-machines"><div class="card-label">Unique Machines</div><div class="card-value">%d</div></div>
    <div class="card" id="remasters-clones"><span id="remasters" class="anchor-alias"></span><span id="remaster" class="anchor-alias"></span><span id="remaster-clone" class="anchor-alias"></span><div class="card-label">Remasters/Clones</div><div class="card-value">%d/%d</div></div>
    <div class="card" id="sysinstalls-clones"><span id="sysinstalls" class="anchor-alias"></span><span id="installations" class="anchor-alias"></span><span id="sysinstall" class="anchor-alias"></span><span id="installation" class="anchor-alias"></span><span id="sysinstall-clone" class="anchor-alias"></span><span id="clones" class="anchor-alias"></span><span id="clone" class="anchor-alias"></span><div class="card-label">Sysinstalls/Clones</div><div class="card-value">%d/%d</div></div>
  </div>
  <div class="grid">
`, s.TotalEvents, s.UniqueMachines, s.ByAction["remaster"], s.RemasterClones, s.ByAction["sysinstall"], s.SysinstallClones)

	writeHTMLTableCard(&buf, "country", "Country", "Country", s.ByCountry, s.TotalEvents)
	writeHTMLTableCard(&buf, "distro", "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeHTMLTableCard(&buf, "desktop", "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeHTMLTableCard(&buf, "display-server", "Display Server", "Display Server", s.ByDisplayServer, s.TotalEvents, "display_server")
	writeHTMLTableCard(&buf, "virtualization", "Virtualization", "Virt", s.ByVirt, s.TotalEvents, "virt")
	writeHTMLTableCard(&buf, "init-system", "Init System", "Init", s.ByInit, s.TotalEvents, "init")
	writeHTMLTableCard(&buf, "arch", "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeHTMLTableCard(&buf, "version", "Version", "Version", s.ByVersion, s.TotalEvents, "eggs-version")

	buf.WriteString(`  </div>
`)

	writeHTMLCommunityArtisans(&buf, s.Artisans)
	writeHTMLTopInstallations(&buf, s.TopInstallations)
	writeHTMLRecentEvents(&buf, s.RecentEvents)

	if s.RepoClients != nil && s.RepoClients.UniqueIPs > 0 {
		totalOps := s.ByAction["remaster"] + s.ByAction["sysinstall"]
		writeHTMLRepoClients(&buf, *s.RepoClients, s.UniqueMachines, totalOps)
	}

	buf.WriteString(`</div>
</body>
</html>
`)

	return buf.String()
}

func writeHTMLRepoClients(buf *bytes.Buffer, r RepoAnalysisSummary, censusMachines, completedOps int) {
	fmt.Fprintf(buf, `  <section class="panel" id="unique-ips">
    <span id="repo-clients" class="anchor-alias"></span>
    <span id="unique-ips-last-15-days" class="anchor-alias"></span>
    <h2><a href="#unique-ips" class="anchor-link">Unique IP last 15 days</a></h2>
    <div class="subtitle" style="margin-bottom: 1rem; color: var(--muted); font-size: 0.85rem;">Estimated from package manager access logs (Debian/APT, Arch/ALPM, RPM/DNF, Alpine/APK) &bull; Updated: %s</div>

    <div class="cards" style="margin-bottom: 1.5rem;">
      <div class="card"><div class="card-label">Unique IPs (15 days)</div><div class="card-value">%d</div></div>
      <div class="card"><div class="card-label">Census Active Machines</div><div class="card-value">%d</div></div>
      <div class="card"><div class="card-label">Completed Operations</div><div class="card-value">%d</div></div>
    </div>

    <div class="grid">
`, html.EscapeString(r.GeneratedAt), r.UniqueIPs, censusMachines, completedOps)

	writeHTMLTableCard(buf, "repo-families", "Package Managers", "Family", r.ByFamily, r.UniqueIPs)
	writeHTMLTableCard(buf, "repo-countries", "Repo Clients by Country", "Country", r.ByCountry, r.UniqueIPs)

	buf.WriteString(`    </div>
  </section>
`)
}

func writeHTMLTopInstallations(buf *bytes.Buffer, list []TopInstallation) {
	buf.WriteString(`  <section class="panel" id="top-sysinstalls">
    <span id="top-installations" class="anchor-alias"></span>
    <h2><a href="#top-sysinstalls" class="anchor-link">Top 10 Sysinstalls</a></h2>
    <table>
      <thead>
        <tr>
          <th>Country</th>
          <th>Distro</th>
          <th>Desktop</th>
          <th>Display Server</th>
          <th>Arch</th>
          <th>Version</th>
          <th class="num">Sysinstalls</th>
        </tr>
      </thead>
      <tbody>
`)
	if len(list) == 0 {
		buf.WriteString(`        <tr><td colspan="7" class="empty">No sysinstalls recorded yet</td></tr>
`)
	} else {
		for _, item := range list {
			fmt.Fprintf(buf, `        <tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="num">%d</td></tr>
`,
				html.EscapeString(item.Country),
				html.EscapeString(item.Distro),
				html.EscapeString(item.Desktop),
				html.EscapeString(item.DisplayServer),
				html.EscapeString(item.Arch),
				html.EscapeString(item.EggsVersion),
				item.SumSysinstall,
			)
		}
	}
	buf.WriteString(`      </tbody>
    </table>
  </section>
`)
}

func pluralRemasters(count int) string {
	if count == 1 {
		return "build"
	}
	return "builds"
}

func locDisplayHTML(locDisplay string) string {
	if locDisplay == "" {
		return ""
	}
	return fmt.Sprintf(`<div class="artisan-loc">%s</div>`, locDisplay)
}

func renderCommunityHTML(artisans []ArtisanProfile) string {
	var buf bytes.Buffer

	totalRemasters := 0
	countriesMap := make(map[string]bool)
	for _, a := range artisans {
		totalRemasters += a.RemasterCount
		c := strings.ToUpper(strings.TrimSpace(a.Country))
		if c != "" && c != "XX" {
			countriesMap[c] = true
		}
	}

	buf.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>🐧 Penguins Community &amp; Distro Artisans</title>
<style>
:root {
  --bg: #0f172a;
  --panel: #1e293b;
  --border: #334155;
  --text: #f8fafc;
  --muted: #94a3b8;
  --accent: #38bdf8;
  --accent-bg: rgba(56, 189, 248, 0.16);
  --tag-bg: #334155;
}
* { box-sizing: border-box; }
html { scroll-behavior: smooth; }
body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  background: var(--bg);
  color: var(--text);
  line-height: 1.5;
  padding: 2rem 1rem;
}
.container {
  max-width: 1180px;
  margin: 0 auto;
}
header {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  margin-bottom: 1.75rem;
  border-bottom: 1px solid var(--border);
  padding-bottom: 1rem;
}
h1 {
  margin: 0;
  font-size: 1.65rem;
}
.subtitle {
  color: var(--muted);
  font-size: 0.9rem;
  margin-top: 0.25rem;
}
.links {
  display: flex;
  gap: 0.6rem;
  flex-wrap: wrap;
}
.btn {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  background: var(--panel);
  color: var(--text);
  border: 1px solid var(--border);
  padding: 0.45rem 0.85rem;
  border-radius: 6px;
  text-decoration: none;
  font-size: 0.85rem;
  cursor: pointer;
  transition: all 0.2s;
}
.btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.btn-primary {
  background: var(--accent);
  color: #0f172a;
  font-weight: 600;
  border-color: var(--accent);
}
.btn-primary:hover {
  background: #7dd3fc;
  color: #0f172a;
  border-color: #7dd3fc;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 1rem;
  margin-bottom: 1.75rem;
}
.card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1rem 1.25rem;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.card:hover {
  border-color: var(--accent);
}
.card-label {
  color: var(--muted);
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.card-value {
  font-size: 1.8rem;
  font-weight: 700;
  color: var(--accent);
  margin-top: 0.2rem;
}
.artisans-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 1.25rem;
  margin-bottom: 2rem;
}
.artisan-card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  transition: border-color 0.2s, box-shadow 0.2s, transform 0.15s ease-out;
}
.artisan-card:hover {
  border-color: var(--accent);
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.35);
}
.card-top {
  margin-bottom: 0.75rem;
}
.card-header-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 0.5rem;
  margin-bottom: 0.35rem;
}
.artisan-nick {
  font-size: 1.2rem;
  font-weight: 700;
  color: var(--accent);
  margin: 0;
  word-break: break-all;
}
.country-badge {
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.3);
  padding: 0.15rem 0.45rem;
  border-radius: 4px;
  font-size: 0.75rem;
  font-weight: 600;
  white-space: nowrap;
}
.artisan-loc {
  font-size: 0.85rem;
  color: var(--muted);
  margin-bottom: 0.6rem;
}
.bio {
  color: #cbd5e1;
  font-size: 0.9rem;
  margin: 0.5rem 0 1rem 0;
  line-height: 1.5;
  word-break: break-word;
}
.iso-box {
  background: rgba(15, 23, 42, 0.65);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0.75rem;
  margin-bottom: 1rem;
}
.iso-label {
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--muted);
  margin-bottom: 0.35rem;
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.iso-badge {
  display: inline-block;
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.4);
  padding: 0.25rem 0.55rem;
  border-radius: 6px;
  font-size: 0.85rem;
  font-family: monospace;
  font-weight: 600;
  word-break: break-all;
}
.iso-meta {
  font-size: 0.75rem;
  color: var(--muted);
  margin-top: 0.4rem;
}
.card-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-top: 0.5rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--border);
}
.social-badge {
  display: inline-flex;
  align-items: center;
  background: var(--tag-bg);
  color: var(--text);
  border: 1px solid var(--border);
  padding: 0.25rem 0.5rem;
  border-radius: 6px;
  font-size: 0.8rem;
  text-decoration: none;
  transition: all 0.2s;
}
.social-badge:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.empty {
  color: var(--muted);
  text-align: center;
  padding: 3rem 1rem;
  font-size: 1.05rem;
  grid-column: 1 / -1;
}
</style>
</head>
<body>
<div class="container">
  <header>
    <div>
      <h1>🐧 Penguins Community &amp; Distro Artisans</h1>
      <div class="subtitle">Showcase of independent Linux distributions and distro artisans building with Penguins Eggs</div>
    </div>
    <div class="links">
      <a class="btn" href="/api/census">📊 Telemetry Dashboard</a>
      <a class="btn" href="?format=txt">Terminal / Plain Text</a>
      <a class="btn" href="?format=json">Raw JSON</a>
      <a class="btn" href="https://penguins-eggs.net" target="_blank" rel="noopener noreferrer">Eggs Home</a>
      <a class="btn" href="https://github.com/pieroproietti/penguins-eggs" target="_blank" rel="noopener noreferrer">Eggs Repo</a>
    </div>
  </header>
`)

	fmt.Fprintf(&buf, `  <div class="cards">
    <div class="card"><div class="card-label">Distro Artisans</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Remaster ISOs Built</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Countries Represented</div><div class="card-value">%d</div></div>
  </div>

  <div class="artisans-grid">
`, len(artisans), totalRemasters, len(countriesMap))

	if len(artisans) == 0 {
		buf.WriteString(`    <div class="empty">No community artisans registered yet &bull; Register your profile to join the showcase!</div>
`)
	} else {
		for _, a := range artisans {
			nick := strings.TrimSpace(a.Nickname)
			if nick == "" {
				nick = a.UUID
				if len(nick) > 8 {
					nick = nick[:8]
				}
			}
			if !strings.HasPrefix(nick, "@") {
				nick = "@" + nick
			}

			country := strings.ToUpper(strings.TrimSpace(a.Country))
			flag := countryToFlag(country)

			loc := strings.TrimSpace(a.Location)
			locDisplay := ""
			if loc != "" && flag != "" {
				locDisplay = fmt.Sprintf("📍 %s %s", html.EscapeString(loc), flag)
			} else if loc != "" {
				locDisplay = fmt.Sprintf("📍 %s", html.EscapeString(loc))
			} else if flag != "" {
				locDisplay = fmt.Sprintf("📍 %s %s", flag, html.EscapeString(country))
			}

			countryBadge := ""
			if country != "" && country != "XX" {
				if flag != "" {
					countryBadge = fmt.Sprintf(`<span class="country-badge">%s %s</span>`, flag, html.EscapeString(country))
				} else {
					countryBadge = fmt.Sprintf(`<span class="country-badge">%s</span>`, html.EscapeString(country))
				}
			}

			bio := strings.TrimSpace(a.Bio)
			bioHTML := ""
			if bio != "" {
				bioHTML = fmt.Sprintf(`<p class="bio">%s</p>`, html.EscapeString(bio))
			} else {
				bioHTML = `<p class="bio" style="color:var(--muted); font-style:italic;">No bio provided.</p>`
			}

			isoBox := ""
			if a.LatestRemaster != nil {
				badgeText := formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
				dateStr := formatEventDate(a.LastRemasterAt)
				isoBox = fmt.Sprintf(`      <div class="iso-box">
        <div class="iso-label">
          <span>Ultima ISO sfornata</span>
          <span>%d %s</span>
        </div>
        <div class="iso-badge">%s</div>
        <div class="iso-meta">Last build: %s</div>
      </div>`, a.RemasterCount, pluralRemasters(a.RemasterCount), html.EscapeString(badgeText), html.EscapeString(dateStr))
			} else {
				isoBox = `      <div class="iso-box">
        <div class="iso-label"><span>Ultima ISO sfornata</span></div>
        <div class="iso-badge" style="opacity:0.6; font-size:0.8rem;">No ISO recorded yet</div>
      </div>`
			}

			var actionBtns []string
			if isSafeURL(a.SourceURL) {
				actionBtns = append(actionBtns, fmt.Sprintf(`<a href="%s" class="btn btn-primary" target="_blank" rel="noopener noreferrer">🌐 Source / Project &rarr;</a>`, html.EscapeString(a.SourceURL)))
			}

			socialLinks := buildSocialLinks(a.Socials, a.Contact)
			for _, sl := range socialLinks {
				if isSafeURL(sl.URL) {
					actionBtns = append(actionBtns, fmt.Sprintf(`<a href="%s" class="social-badge" target="_blank" rel="noopener noreferrer">%s</a>`, html.EscapeString(sl.URL), html.EscapeString(sl.Label)))
				}
			}

			actionsHTML := ""
			if len(actionBtns) > 0 {
				actionsHTML = fmt.Sprintf(`    <div class="card-actions">
      %s
    </div>`, strings.Join(actionBtns, "\n      "))
			}

			fmt.Fprintf(&buf, `    <div class="artisan-card" id="artisan-%s">
      <div class="card-top">
        <div class="card-header-row">
          <h2 class="artisan-nick">%s</h2>
          %s
        </div>
        %s
        %s
      </div>
%s
%s
    </div>
`, html.EscapeString(a.UUID), html.EscapeString(nick), countryBadge, locDisplayHTML(locDisplay), bioHTML, isoBox, actionsHTML)
		}
	}

	buf.WriteString(`  </div>
</div>
</body>
</html>
`)

	return buf.String()
}

func renderCommunityText(artisans []ArtisanProfile) string {
	var buf bytes.Buffer
	buf.WriteString("# 🐧 Penguins Community & Distro Artisans\n\n")
	fmt.Fprintf(&buf, "- Total Artisans: %d\n\n", len(artisans))

	type tableRow struct {
		Nickname string
		Location string
		Distro   string
		Source   string
		Contacts string
	}

	rows := make([]tableRow, 0, len(artisans))
	for _, a := range artisans {
		nick := strings.TrimSpace(a.Nickname)
		if nick == "" {
			nick = a.UUID
			if len(nick) > 8 {
				nick = nick[:8]
			}
		}
		if !strings.HasPrefix(nick, "@") {
			nick = "@" + nick
		}

		loc := strings.TrimSpace(a.Location)
		country := strings.ToUpper(strings.TrimSpace(a.Country))
		if loc != "" && country != "" && !strings.Contains(strings.ToUpper(loc), country) {
			loc = loc + " [" + country + "]"
		} else if loc == "" && country != "" {
			loc = country
		}
		if loc == "" {
			loc = "-"
		}

		distro := "-"
		if a.LatestRemaster != nil {
			distro = formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
		}

		source := strings.TrimSpace(a.SourceURL)
		if source == "" {
			source = "-"
		}

		var contactParts []string
		links := buildSocialLinks(a.Socials, a.Contact)
		for _, l := range links {
			contactParts = append(contactParts, fmt.Sprintf("%s:%s", l.Platform, l.URL))
		}
		contactStr := "-"
		if len(contactParts) > 0 {
			contactStr = strings.Join(contactParts, ", ")
		} else if a.Contact != "" {
			contactStr = a.Contact
		}

		rows = append(rows, tableRow{
			Nickname: nick,
			Location: loc,
			Distro:   distro,
			Source:   source,
			Contacts: contactStr,
		})
	}

	nickW := len("Nickname")
	locW := len("Location")
	distroW := len("Ultima Distro")
	sourceW := len("Source URL")
	contactW := len("Contatti")

	if len(rows) == 0 {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n", nickW, "Nickname", locW, "Location", distroW, "Ultima Distro", sourceW, "Source URL", contactW, "Contatti")
		fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|\n", strings.Repeat("-", nickW), strings.Repeat("-", locW), strings.Repeat("-", distroW), strings.Repeat("-", sourceW), strings.Repeat("-", contactW))
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n\n", nickW, "(no data)", locW, "-", distroW, "-", sourceW, "-", contactW, "-")
		return buf.String()
	}

	for _, r := range rows {
		if len(r.Nickname) > nickW {
			nickW = len(r.Nickname)
		}
		if len(r.Location) > locW {
			locW = len(r.Location)
		}
		if len(r.Distro) > distroW {
			distroW = len(r.Distro)
		}
		if len(r.Source) > sourceW {
			sourceW = len(r.Source)
		}
		if len(r.Contacts) > contactW {
			contactW = len(r.Contacts)
		}
	}

	fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n", nickW, "Nickname", locW, "Location", distroW, "Ultima Distro", sourceW, "Source URL", contactW, "Contatti")
	fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|\n", strings.Repeat("-", nickW), strings.Repeat("-", locW), strings.Repeat("-", distroW), strings.Repeat("-", sourceW), strings.Repeat("-", contactW))

	for _, r := range rows {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n", nickW, r.Nickname, locW, r.Location, distroW, r.Distro, sourceW, r.Source, contactW, r.Contacts)
	}
	buf.WriteString("\n")

	return buf.String()
}

func writePrettyJSON(w http.ResponseWriter, v any) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to encode summary"}`))
		return
	}
	out = append(out, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func parseAccessLogLine(line string) (ip, method, path, status, ua string, ok bool) {
	sp1 := strings.IndexByte(line, ' ')
	if sp1 <= 0 {
		return "", "", "", "", "", false
	}
	ip = line[:sp1]

	q1 := strings.IndexByte(line[sp1:], '"')
	if q1 < 0 {
		return "", "", "", "", "", false
	}
	q1 += sp1
	q2 := strings.IndexByte(line[q1+1:], '"')
	if q2 < 0 {
		return "", "", "", "", "", false
	}
	q2 += q1 + 1
	req := line[q1+1 : q2]
	parts := strings.Split(req, " ")
	if len(parts) >= 2 {
		method = parts[0]
		path = parts[1]
	} else {
		return "", "", "", "", "", false
	}

	afterReq := line[q2+1:]
	afterParts := strings.Fields(afterReq)
	if len(afterParts) >= 1 {
		status = afterParts[0]
	}

	lastQ := strings.LastIndexByte(line, '"')
	if lastQ > 0 {
		prevQ := strings.LastIndexByte(line[:lastQ], '"')
		if prevQ >= 0 {
			ua = line[prevQ+1 : lastQ]
		}
	}

	return ip, method, path, status, ua, true
}

func isCrawlerUserAgent(ua string) bool {
	l := strings.ToLower(ua)
	for _, kw := range []string{
		"bot", "crawler", "spider", "ahrefs", "semrush", "petalbot",
		"yandex", "bingbot", "googlebot", "bytespider", "baiduspider",
	} {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
}

func matchRepoRequest(path, ua string) (family string, isIndex, isPkg bool, ok bool) {
	lp := strings.ToLower(path)
	lua := strings.ToLower(ua)

	// Debian / Ubuntu / Devuan / Mint
	if strings.Contains(lua, "apt-http") || strings.Contains(lp, "/repos/deb/") || strings.Contains(lp, "/dists/") || strings.Contains(lp, "inrelease") {
		family = "Debian/APT"
		if strings.Contains(lp, "inrelease") || strings.Contains(lp, "release") || strings.Contains(lp, "packages") {
			isIndex = true
		}
		if strings.HasSuffix(lp, ".deb") {
			isPkg = true
		}
		return family, isIndex, isPkg, isIndex || isPkg
	}

	// Arch / Manjaro
	if strings.Contains(lua, "pacman") || strings.Contains(lua, "libalpm") || strings.Contains(lp, "/repos/arch/") || strings.HasSuffix(lp, ".db") || strings.HasSuffix(lp, ".db.tar.gz") {
		family = "Arch/ALPM"
		if strings.HasSuffix(lp, ".db") || strings.HasSuffix(lp, ".db.tar.gz") || strings.HasSuffix(lp, ".files") || strings.HasSuffix(lp, ".files.tar.gz") {
			isIndex = true
		}
		if strings.HasSuffix(lp, ".pkg.tar.zst") || strings.HasSuffix(lp, ".pkg.tar.xz") {
			isPkg = true
		}
		return family, isIndex, isPkg, isIndex || isPkg
	}

	// RPM (Fedora, openSUSE)
	if strings.Contains(lua, "dnf") || strings.Contains(lua, "libdnf") || strings.Contains(lua, "yum") || strings.Contains(lua, "zypp") || strings.Contains(lp, "/repos/rpm/") || strings.Contains(lp, "repomd.xml") {
		family = "RPM/DNF"
		if strings.Contains(lp, "repomd.xml") || strings.Contains(lp, "primary.xml") || strings.Contains(lp, "filelists.xml") {
			isIndex = true
		}
		if strings.HasSuffix(lp, ".rpm") {
			isPkg = true
		}
		return family, isIndex, isPkg, isIndex || isPkg
	}

	// Alpine
	if strings.Contains(lua, "apk-tools") || strings.Contains(lp, "/repos/alpine/") || strings.Contains(lp, "apkindex") {
		family = "Alpine/APK"
		if strings.Contains(lp, "apkindex") {
			isIndex = true
		}
		if strings.HasSuffix(lp, ".apk") {
			isPkg = true
		}
		return family, isIndex, isPkg, isIndex || isPkg
	}

	return "", false, false, false
}

func lookupIPCountry(db *maxminddb.Reader, ipStr string) string {
	if db == nil {
		return "XX"
	}
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return "XX"
	}
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := db.Lookup(ip, &record); err != nil || record.Country.ISOCode == "" {
		return "XX"
	}
	return record.Country.ISOCode
}

func renderRepoAnalysisReport(repoSummary RepoAnalysisSummary, censusSummary *CensusSummary) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Stima Macchine Reali Penguins-Eggs (Repository vs Telemetria)\n\n")

	fmt.Fprintf(&buf, "## 🎯 I Tre Livelli di Misurazione\n\n")
	fmt.Fprintf(&buf, "| Livello di Misurazione | Metrica / Significato | Conteggio Rilevato |\n")
	fmt.Fprintf(&buf, "|:---|:---|:---:|\n")
	fmt.Fprintf(&buf, "| **1. Client Repository Osservati** | IP unici de-duplicati che interrogano i repo (APT, ALPM, DNF, APK) | **%d** |\n", repoSummary.UniqueIPs)

	if censusSummary != nil {
		fmt.Fprintf(&buf, "| **2. Macchine Censite da Eggs** | UUID unici hardware (`/etc/machine-id`) con eggs eseguito | **%d** |\n", censusSummary.UniqueMachines)
		totalOps := censusSummary.ByAction["remaster"] + censusSummary.ByAction["sysinstall"]
		fmt.Fprintf(&buf, "| **3. Operazioni Riuscite** | Totale rimasterizzazioni (`remaster`) e installazioni (`sysinstall`) | **%d** (R:%d, S:%d) |\n\n",
			totalOps, censusSummary.ByAction["remaster"], censusSummary.ByAction["sysinstall"])
	} else {
		fmt.Fprintf(&buf, "| **2. Macchine Censite da Eggs** | UUID unici hardware (`/etc/machine-id`) | *(nessun dato censimento)* |\n")
		fmt.Fprintf(&buf, "| **3. Operazioni Riuscite** | Remaster e Sysinstall completati | *(nessun dato censimento)* |\n\n")
	}

	fmt.Fprintf(&buf, "- **Righe totali di log analizzate:** %d\n", repoSummary.TotalLogLines)
	fmt.Fprintf(&buf, "- **Richieste repository valide (200/304):** %d\n", repoSummary.ValidHits)
	fmt.Fprintf(&buf, "- **Richieste bot/crawler scartate:** %d\n\n", repoSummary.IgnoredBots)

	writeMarkdownTable(&buf, "Famiglie Repository", "Famiglia", repoSummary.ByFamily, repoSummary.UniqueIPs)
	writeMarkdownTable(&buf, "Distribuzione Geografica Client Repository (\"e da dove?\")", "Country", repoSummary.ByCountry, repoSummary.UniqueIPs)

	return buf.String()
}

func processAccessLogReaders(readers []io.Reader, db *maxminddb.Reader) RepoAnalysisSummary {
	merged := RepoAnalysisSummary{
		ByFamily:  make(map[string]int),
		ByCountry: make(map[string]int),
	}
	clients := make(map[string]*RepoClientStats)

	for _, reader := range readers {
		scanner := bufio.NewScanner(reader)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 256*1024)

		for scanner.Scan() {
			line := scanner.Text()
			merged.TotalLogLines++

			ip, _, path, status, ua, ok := parseAccessLogLine(line)
			if !ok {
				continue
			}

			if isCrawlerUserAgent(ua) {
				merged.IgnoredBots++
				continue
			}

			if status != "200" && status != "304" && status != "206" {
				continue
			}

			family, isIndex, isPkg, isRepo := matchRepoRequest(path, ua)
			if !isRepo {
				continue
			}

			merged.ValidHits++
			client, exists := clients[ip]
			if !exists {
				client = &RepoClientStats{
					Families: make(map[string]bool),
				}
				clients[ip] = client
			}
			client.Hits++
			client.Families[family] = true
			if isIndex {
				client.HasIndex = true
			}
			if isPkg {
				client.HasPackage = true
			}
		}
	}

	merged.UniqueIPs = len(clients)
	for ip, cl := range clients {
		for f := range cl.Families {
			merged.ByFamily[f]++
		}
		c := lookupIPCountry(db, ip)
		merged.ByCountry[c]++
	}

	return merged
}

func processAccessLogStream(r io.Reader, db *maxminddb.Reader) RepoAnalysisSummary {
	return processAccessLogReaders([]io.Reader{r}, db)
}

func saveRepoAnalysisSummary(path string, summary RepoAnalysisSummary) error {
	if summary.GeneratedAt == "" {
		summary.GeneratedAt = time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}
	tmpFile := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, path)
}

func loadRepoAnalysisSummary(path string) (*RepoAnalysisSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var res RepoAnalysisSummary
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func runAnalyzeAccessLogs(paths []string, mmdbPath, dataPath, savePath string) error {
	var db *maxminddb.Reader
	if mmdbPath != "" {
		var err error
		db, err = maxminddb.Open(mmdbPath)
		if err == nil {
			defer db.Close()
		}
	}

	var readers []io.Reader
	var closers []io.Closer

	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	if len(paths) == 0 || (len(paths) == 1 && paths[0] == "-") {
		readers = append(readers, os.Stdin)
	} else {
		for _, p := range paths {
			f, err := os.Open(p)
			if err != nil {
				log.Printf("warning: skipping %s: %v", p, err)
				continue
			}
			closers = append(closers, f)
			if strings.HasSuffix(p, ".gz") {
				gzReader, err := gzip.NewReader(f)
				if err != nil {
					log.Printf("warning: skipping gzip file %s: %v", p, err)
					continue
				}
				closers = append(closers, gzReader)
				readers = append(readers, gzReader)
			} else {
				readers = append(readers, f)
			}
		}
	}

	merged := processAccessLogReaders(readers, db)
	merged.GeneratedAt = time.Now().UTC().Format("2006-01-02 15:04:05 UTC")

	if savePath != "" {
		if err := saveRepoAnalysisSummary(savePath, merged); err != nil {
			log.Printf("warning: failed to save repo analysis to %s: %v", savePath, err)
		} else {
			log.Printf("Repository analysis saved to: %s", savePath)
		}
	}

	var census *CensusSummary
	if dataPath != "" {
		if c, err := loadCensusSummary(dataPath); err == nil {
			census = &c
		}
	}

	fmt.Print(renderRepoAnalysisReport(merged, census))
	return nil
}

func resetCensusData(dataPath string) error {
	fi, err := os.Stat(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		entries, err := os.ReadDir(dataPath)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() {
				filePath := filepath.Join(dataPath, e.Name())
				if err := os.Truncate(filePath, 0); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return os.Truncate(dataPath, 0)
}

func main() {
	defaultAddr := os.Getenv("CENSUS_ADDR")
	if defaultAddr == "" {
		defaultAddr = "127.0.0.1:8080"
	}
	defaultCommDir := os.Getenv("CENSUS_COMMUNITY_DIR")

	addr := flag.String("addr", defaultAddr, "HTTP listen address (env CENSUS_ADDR)")
	dataPath := flag.String("data", "/var/lib/penguins-census/census.jsonl", "Path to census JSONL file or directory")
	mmdbPath := flag.String("mmdb", "/var/lib/penguins-census/dbip-country.mmdb", "Path to GeoIP MMDB file")
	communityDir := flag.String("community-dir", defaultCommDir, "Directory to store community profiles (default: <dataDir>/community)")
	reset := flag.Bool("reset", false, "Reset (truncate) all census counts and exit")
	top := flag.Bool("top", false, "Print top 10 unique sysinstall rankings and exit")
	report := flag.Bool("report", false, "Print full census markdown report to terminal and exit")
	analyzeAccess := flag.String("analyze-access", "", "Path to Apache access log (or '-' for stdin) to estimate active repo machines and exit")
	saveFlag := flag.Bool("save", false, "When used with -analyze-access, save results to repo-clients.json in census data directory")
	outPath := flag.String("out", "", "Custom path to save repo analysis JSON")
	flag.Parse()

	if *analyzeAccess != "" {
		paths := append([]string{*analyzeAccess}, flag.Args()...)
		savePath := *outPath
		if savePath == "" && *saveFlag {
			savePath = filepath.Join(filepath.Dir(*dataPath), "repo-clients.json")
			if fi, err := os.Stat(*dataPath); err == nil && fi.IsDir() {
				savePath = filepath.Join(*dataPath, "repo-clients.json")
			}
		}
		if err := runAnalyzeAccessLogs(paths, *mmdbPath, *dataPath, savePath); err != nil {
			log.Fatalf("failed to analyze access logs: %v", err)
		}
		return
	}

	if *reset {
		if err := resetCensusData(*dataPath); err != nil {
			log.Fatalf("failed to reset census data (%s): %v", *dataPath, err)
		}
		fmt.Printf("Census data reset: %s\n", *dataPath)
		return
	}

	if *top {
		summary, err := loadCensusSummary(*dataPath)
		if err != nil {
			log.Fatalf("failed to read census data (%s): %v", *dataPath, err)
		}
		var buf bytes.Buffer
		writeMarkdownTopInstallations(&buf, summary.TopInstallations)
		fmt.Print(buf.String())
		return
	}

	if *report {
		summary, err := loadCensusSummary(*dataPath)
		if err != nil {
			log.Fatalf("failed to read census data (%s): %v", *dataPath, err)
		}
		fmt.Print(renderTextSummary(summary))
		return
	}

	srv := NewCensusServer(*dataPath, *mmdbPath)
	if *communityDir != "" {
		srv.SetCommunityDir(*communityDir)
	}
	defer srv.Close()

	log.Printf("penguins-census listening on %s (data=%s, mmdb=%s, community=%s)", *addr, *dataPath, *mmdbPath, srv.getCommunityDir())
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCensusPostAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	missingMMDB := filepath.Join(tmpDir, "nonexistent.mmdb")

	srv := NewCensusServer(dataFile, missingMMDB)
	defer srv.Close()

	// 1. GET on empty/non-existent JSONL should return initialized empty summary
	reqGetEmpty := httptest.NewRequest(http.MethodGet, "/api/census", nil)
	wGetEmpty := httptest.NewRecorder()
	srv.ServeHTTP(wGetEmpty, reqGetEmpty)
	if wGetEmpty.Code != http.StatusOK {
		t.Fatalf("expected 200 on empty GET, got %d", wGetEmpty.Code)
	}
	var emptySummary CensusSummary
	if err := json.Unmarshal(wGetEmpty.Body.Bytes(), &emptySummary); err != nil {
		t.Fatalf("failed to unmarshal empty summary: %v", err)
	}
	if emptySummary.TotalEvents != 0 || emptySummary.UniqueMachines != 0 {
		t.Fatalf("expected 0 events and 0 unique machines, got %+v", emptySummary)
	}

	// 2. POST valid remaster event
	payload1 := `{
		"uuid": "machine-uuid-001",
		"eggs_version": "10.0.50",
		"action": "remaster",
		"distro": { "id": "debian", "like": "debian", "codename": "bookworm", "release": "12" },
		"system": { "arch": "amd64", "desktop": "xfce", "display_server": "x11", "virt": "kvm", "init": "systemd" },
		"options": { "clone": true, "compression": "zstd", "coexist": false }
	}`
	reqPost1 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(payload1))
	reqPost1.Header.Set("Content-Type", "application/json")
	reqPost1.Header.Set("X-Real-IP", "203.0.113.42")
	wPost1 := httptest.NewRecorder()
	srv.ServeHTTP(wPost1, reqPost1)

	if wPost1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wPost1.Code, wPost1.Body.String())
	}
	if strings.TrimSpace(wPost1.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("expected {\"status\":\"ok\"}, got %s", wPost1.Body.String())
	}

	// 3. POST second event from same machine (sysinstall) + third event from second machine
	payload2 := `{
		"uuid": "machine-uuid-001",
		"eggs_version": "10.0.50",
		"action": "sysinstall",
		"distro": { "id": "debian", "like": "debian", "codename": "bookworm", "release": "12" },
		"system": { "arch": "amd64", "desktop": "xfce", "display_server": "x11", "virt": "none", "init": "systemd" },
		"options": { "clone": false, "compression": "xz", "coexist": true }
	}`
	reqPost2 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(payload2))
	reqPost2.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	wPost2 := httptest.NewRecorder()
	srv.ServeHTTP(wPost2, reqPost2)
	if wPost2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on second post, got %d", wPost2.Code)
	}

	payload3 := `{
		"uuid": "machine-uuid-002",
		"eggs_version": "10.1.0",
		"action": "remaster",
		"distro": { "id": "arch", "like": "arch", "codename": "rolling", "release": "rolling" },
		"system": { "arch": "arm64", "desktop": "gnome", "display_server": "wayland", "virt": "none", "init": "systemd" },
		"options": { "clone": false, "compression": "zstd", "coexist": false }
	}`
	reqPost3 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(payload3))
	wPost3 := httptest.NewRecorder()
	srv.ServeHTTP(wPost3, reqPost3)
	if wPost3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on third post, got %d", wPost3.Code)
	}

	// Verify strict privacy: raw file must NEVER contain the IP addresses
	rawBytes, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("failed to read census.jsonl: %v", err)
	}
	rawContent := string(rawBytes)
	if strings.Contains(rawContent, "203.0.113.42") || strings.Contains(rawContent, "198.51.100.7") {
		t.Fatalf("PRIVACY VIOLATION: client IP address found in stored JSONL")
	}

	// Verify enriched fields (timestamp & country "XX" fallback) in stored JSONL lines
	lines := strings.Split(strings.TrimSpace(rawContent), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines, got %d", len(lines))
	}
	var firstRec CensusRecord
	if err := json.Unmarshal([]byte(lines[0]), &firstRec); err != nil {
		t.Fatalf("invalid JSON line: %v", err)
	}
	if firstRec.Timestamp == "" {
		t.Errorf("expected non-empty RFC3339 timestamp")
	}
	if firstRec.Country != "XX" {
		t.Errorf("expected country fallback 'XX', got %q", firstRec.Country)
	}

	// 4. GET summary and verify counts
	reqGet := httptest.NewRequest(http.MethodGet, "/api/census?format=json", nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET, got %d", wGet.Code)
	}
	if !strings.Contains(wGet.Header().Get("Content-Type"), "application/json") {
		t.Errorf("expected application/json Content-Type, got %q", wGet.Header().Get("Content-Type"))
	}

	var summary CensusSummary
	if err := json.Unmarshal(wGet.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to parse summary JSON: %v", err)
	}
	if summary.TotalEvents != 3 {
		t.Errorf("expected total_events=3, got %d", summary.TotalEvents)
	}
	if summary.UniqueMachines != 2 {
		t.Errorf("expected unique_machines=2, got %d", summary.UniqueMachines)
	}
	if summary.RemasterClones != 1 {
		t.Errorf("expected remaster_clones=1, got %d", summary.RemasterClones)
	}
	if summary.SysinstallClones != 0 {
		t.Errorf("expected sysinstall_clones=0, got %d", summary.SysinstallClones)
	}
	if summary.CoexistCount != 1 {
		t.Errorf("expected coexist_count=1, got %d", summary.CoexistCount)
	}
	if summary.ByAction["remaster"] != 2 || summary.ByAction["sysinstall"] != 1 {
		t.Errorf("unexpected by_action: %+v", summary.ByAction)
	}
	if summary.ByDistro["debian"] != 2 || summary.ByDistro["arch"] != 1 {
		t.Errorf("unexpected by_distro: %+v", summary.ByDistro)
	}
	if summary.ByArch["amd64"] != 2 || summary.ByArch["arm64"] != 1 {
		t.Errorf("unexpected by_arch: %+v", summary.ByArch)
	}
	if summary.ByDesktop["xfce"] != 2 || summary.ByDesktop["gnome"] != 1 {
		t.Errorf("unexpected by_desktop: %+v", summary.ByDesktop)
	}
	if summary.ByDisplayServer["x11"] != 2 || summary.ByDisplayServer["wayland"] != 1 {
		t.Errorf("unexpected by_display_server: %+v", summary.ByDisplayServer)
	}
	if summary.ByVirt["bare-metal"] != 2 || summary.ByVirt["kvm"] != 1 {
		t.Errorf("unexpected by_virt: %+v", summary.ByVirt)
	}
	if summary.ByInit["systemd"] != 3 {
		t.Errorf("unexpected by_init: %+v", summary.ByInit)
	}
	if summary.ByCountry["XX"] != 3 {
		t.Errorf("unexpected by_country: %+v", summary.ByCountry)
	}
	if summary.ByVersion["10.0.50"] != 2 || summary.ByVersion["10.1.0"] != 1 {
		t.Errorf("unexpected by_version: %+v", summary.ByVersion)
	}

	// 5. Verify recent events (last 3, newest first)
	if len(summary.RecentEvents) != 3 {
		t.Fatalf("expected 3 recent events, got %d", len(summary.RecentEvents))
	}
	// Verify date format (YYYY-MM-DD HH:MM:SS)
	if summary.RecentEvents[0].Date == "" {
		t.Errorf("expected non-empty date in recent_events[0]")
	}
	if _, err := time.Parse("2006-01-02 15:04:05", summary.RecentEvents[0].Date); err != nil {
		t.Errorf("expected date in 'YYYY-MM-DD HH:MM:SS' format, got %q: %v", summary.RecentEvents[0].Date, err)
	}
	// Most recent (event 3): arch, gnome, wayland, remaster
	if summary.RecentEvents[0].Country != "XX" || summary.RecentEvents[0].Distro != "arch" ||
		summary.RecentEvents[0].Desktop != "gnome" || summary.RecentEvents[0].DisplayServer != "wayland" ||
		!summary.RecentEvents[0].Remaster || summary.RecentEvents[0].Sysinstall {
		t.Errorf("unexpected recent_events[0]: %+v", summary.RecentEvents[0])
	}
	// Second recent (event 2): debian, xfce, x11, sysinstall
	if summary.RecentEvents[1].Country != "XX" || summary.RecentEvents[1].Distro != "debian" ||
		summary.RecentEvents[1].Desktop != "xfce" || summary.RecentEvents[1].DisplayServer != "x11" ||
		summary.RecentEvents[1].Remaster || !summary.RecentEvents[1].Sysinstall {
		t.Errorf("unexpected recent_events[1]: %+v", summary.RecentEvents[1])
	}
	// Third recent (event 1): debian, xfce, x11, remaster
	if summary.RecentEvents[2].Country != "XX" || summary.RecentEvents[2].Distro != "debian" ||
		summary.RecentEvents[2].Desktop != "xfce" || summary.RecentEvents[2].DisplayServer != "x11" ||
		!summary.RecentEvents[2].Remaster || summary.RecentEvents[2].Sysinstall {
		t.Errorf("unexpected recent_events[2]: %+v", summary.RecentEvents[2])
	}
}

func TestCensusValidationAndLimits(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	defer srv.Close()

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "missing uuid",
			body:       `{"uuid": "   ", "action": "remaster"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid action",
			body:       `{"uuid": "1234", "action": "update"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed json",
			body:       `{"uuid": "1234",`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "oversized body (>16KB)",
			body:       `{"uuid": "` + strings.Repeat("a", 17*1024) + `", "action": "remaster"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/census", bytes.NewBufferString(tc.body))
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("%s: expected status %d, got %d", tc.name, tc.wantStatus, w.Code)
			}
		})
	}
}

func TestCommunityPostAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	uuid := "01f86488-6b11-59ff-9630-4215b4d2de3a"
	payload := `{
		"uuid": "` + uuid + `",
		"nickname": "artisan",
		"location": "Rome, Italy",
		"bio": "Open source developer and Linux enthusiast",
		"contact": "@artisan",
		"socials": {
			"github": "artisan-dev",
			"telegram": "@artisan"
		},
		"source_url": "https://github.com/artisan/distro"
	}`

	// 1. POST /api/community
	req := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var postResp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &postResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if postResp["status"] != "ok" || postResp["uuid"] != uuid {
		t.Errorf("unexpected POST response: %+v", postResp)
	}

	// 2. Verify file persisted on disk as <uuid>.json
	targetFile := filepath.Join(commDir, uuid+".json")
	fileBytes, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("expected profile file at %s: %v", targetFile, err)
	}
	type Profile struct {
		UUID      string            `json:"uuid"`
		Nickname  string            `json:"nickname"`
		Location  string            `json:"location"`
		Bio       string            `json:"bio"`
		Contact   string            `json:"contact"`
		Socials   map[string]string `json:"socials"`
		SourceURL string            `json:"source_url"`
		UpdatedAt string            `json:"updated_at"`
	}
	var p Profile
	if err := json.Unmarshal(fileBytes, &p); err != nil {
		t.Fatalf("failed to parse saved profile JSON: %v", err)
	}
	if p.UUID != uuid || p.Nickname != "artisan" || p.Location != "Rome, Italy" || p.Socials["github"] != "artisan-dev" {
		t.Errorf("saved profile mismatch: %+v", p)
	}
	if p.UpdatedAt == "" {
		t.Errorf("expected updated_at to be populated")
	}

	// 3. GET /api/community/{uuid}
	reqGet := httptest.NewRequest(http.MethodGet, "/api/community/"+uuid, nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("expected status 200 on GET, got %d (body: %s)", wGet.Code, wGet.Body.String())
	}
	var getProfile Profile
	if err := json.Unmarshal(wGet.Body.Bytes(), &getProfile); err != nil {
		t.Fatalf("failed to decode GET profile: %v", err)
	}
	if getProfile.UUID != uuid || getProfile.Nickname != "artisan" {
		t.Errorf("GET profile mismatch: %+v", getProfile)
	}

	// 4. GET /api/community (list)
	reqList := httptest.NewRequest(http.MethodGet, "/api/community", nil)
	wList := httptest.NewRecorder()
	srv.ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected status 200 on list, got %d", wList.Code)
	}
	var listResp struct {
		Total    int       `json:"total"`
		Profiles []Profile `json:"profiles"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if listResp.Total != 1 || len(listResp.Profiles) != 1 || listResp.Profiles[0].UUID != uuid {
		t.Errorf("unexpected list response: %+v", listResp)
	}
}

func TestCommunityPutAndUrlParams(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	uuid := "02f86488-6b11-59ff-9630-4215b4d2de3b"

	// 1. PUT /api/community/{uuid} with matching UUID
	payload := `{
		"uuid": "` + uuid + `",
		"nickname": "new-nick",
		"location": "Milan, Italy",
		"bio": "Linux creator",
		"source_url": "https://penguins-eggs.net"
	}`
	reqPut := httptest.NewRequest(http.MethodPut, "/api/community/"+uuid, strings.NewReader(payload))
	reqPut.Header.Set("Content-Type", "application/json")
	wPut := httptest.NewRecorder()
	srv.ServeHTTP(wPut, reqPut)

	if wPut.Code != http.StatusOK {
		t.Fatalf("expected 200 on PUT, got %d: %s", wPut.Code, wPut.Body.String())
	}

	// 2. PUT /api/community/{uuid} without UUID in body (should use path UUID)
	payloadNoUUID := `{
		"nickname": "updated-nick",
		"location": "Turin, Italy",
		"bio": "Dev",
		"url": "https://github.com/test/repo"
	}`
	reqPut2 := httptest.NewRequest(http.MethodPut, "/api/community/"+uuid, strings.NewReader(payloadNoUUID))
	reqPut2.Header.Set("Content-Type", "application/json")
	wPut2 := httptest.NewRecorder()
	srv.ServeHTTP(wPut2, reqPut2)

	if wPut2.Code != http.StatusOK {
		t.Fatalf("expected 200 on PUT without body UUID, got %d: %s", wPut2.Code, wPut2.Body.String())
	}

	// Verify profile was updated
	reqGet := httptest.NewRequest(http.MethodGet, "/api/community/"+uuid, nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)
	if !strings.Contains(wGet.Body.String(), "updated-nick") || !strings.Contains(wGet.Body.String(), "Turin, Italy") {
		t.Errorf("expected updated profile content, got: %s", wGet.Body.String())
	}

	// 3. PUT with mismatching UUID between path and body
	mismatchPayload := `{"uuid": "different-uuid-1111-2222-333344445555", "nickname": "bad"}`
	reqMismatch := httptest.NewRequest(http.MethodPut, "/api/community/"+uuid, strings.NewReader(mismatchPayload))
	reqMismatch.Header.Set("Content-Type", "application/json")
	wMismatch := httptest.NewRecorder()
	srv.ServeHTTP(wMismatch, reqMismatch)

	if wMismatch.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request on UUID mismatch, got %d", wMismatch.Code)
	}
}

func TestCommunityAntiSpamEmailBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	testCases := []struct {
		name        string
		payload     string
		expectBlock bool
	}{
		{
			name:        "explicit email field",
			payload:     `{"uuid":"uuid-spam-1","nickname":"spammer","email":"spam@spammer.org"}`,
			expectBlock: true,
		},
		{
			name:        "email in bio",
			payload:     `{"uuid":"uuid-spam-2","nickname":"legit","bio":"Contact me at hello@spam.com for offers"}`,
			expectBlock: true,
		},
		{
			name:        "mailto in contact",
			payload:     `{"uuid":"uuid-spam-3","nickname":"legit","contact":"mailto:dev@domain.org"}`,
			expectBlock: true,
		},
		{
			name:        "raw email in contact",
			payload:     `{"uuid":"uuid-spam-4","nickname":"legit","contact":"dev@domain.org"}`,
			expectBlock: true,
		},
		{
			name:        "email in nickname",
			payload:     `{"uuid":"uuid-spam-5","nickname":"user@domain.com"}`,
			expectBlock: true,
		},
		{
			name:        "email in socials map",
			payload:     `{"uuid":"uuid-spam-6","nickname":"user","socials":{"matrix":"alice@matrix.org","other":"user@domain.net"}}`,
			expectBlock: true,
		},
		{
			name:        "valid Fediverse handle allowed",
			payload:     `{"uuid":"uuid-legit-1","nickname":"fedidev","contact":"@user@fosstodon.org","source_url":"https://github.com/myuser"}`,
			expectBlock: false,
		},
		{
			name:        "valid normal handle allowed",
			payload:     `{"uuid":"uuid-legit-2","nickname":"penguin","contact":"@penguin_master","source_url":"https://example.org"}`,
			expectBlock: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(tc.payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			if tc.expectBlock {
				if w.Code != http.StatusBadRequest {
					t.Errorf("expected 400 Bad Request for %s, got %d (body: %s)", tc.name, w.Code, w.Body.String())
				}
				if !strings.Contains(w.Body.String(), "email") {
					t.Errorf("expected error message to mention email for %s, got %s", tc.name, w.Body.String())
				}
			} else {
				if w.Code != http.StatusOK {
					t.Errorf("expected 200 OK for %s, got %d (body: %s)", tc.name, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestCommunityValidationAndSafety(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	// Missing UUID on POST
	req1 := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(`{"nickname":"noname"}`))
	w1 := httptest.NewRecorder()
	srv.ServeHTTP(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing UUID, got %d", w1.Code)
	}

	// Path traversal attempt in UUID
	req2 := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(`{"uuid":"../../etc/passwd","nickname":"hacker"}`))
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal UUID, got %d", w2.Code)
	}

	// Path traversal attempt in URL
	req3 := httptest.NewRequest(http.MethodGet, "/api/community/../../etc/passwd", nil)
	w3 := httptest.NewRecorder()
	srv.ServeHTTP(w3, req3)
	// Router or isValidUUID will reject
	if w3.Code != http.StatusBadRequest && w3.Code != http.StatusNotFound {
		t.Errorf("expected 400 or 404 for path traversal in URL, got %d", w3.Code)
	}

	// Non-existent profile GET
	req4 := httptest.NewRequest(http.MethodGet, "/api/community/11111111-2222-3333-4444-555555555555", nil)
	w4 := httptest.NewRecorder()
	srv.ServeHTTP(w4, req4)
	if w4.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent profile, got %d", w4.Code)
	}

	// Method Not Allowed
	req5 := httptest.NewRequest(http.MethodPatch, "/api/community", nil)
	w5 := httptest.NewRecorder()
	srv.ServeHTTP(w5, req5)
	if w5.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", w5.Code)
	}
}

func TestCommunityDelete(t *testing.T) {
	tmpDir := t.TempDir()
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	srv.SetCommunityDir(commDir)

	uuid := "12345678-abcd-ef01-2345-6789abcdef01"

	// Create profile
	reqPost := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(`{"uuid":"`+uuid+`","nickname":"temp"}`))
	wPost := httptest.NewRecorder()
	srv.ServeHTTP(wPost, reqPost)
	if wPost.Code != http.StatusOK {
		t.Fatalf("failed to create profile: %s", wPost.Body.String())
	}

	// DELETE profile
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/community/"+uuid, nil)
	wDel := httptest.NewRecorder()
	srv.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("expected 200 on DELETE, got %d: %s", wDel.Code, wDel.Body.String())
	}

	// Verify file is gone
	if _, err := os.Stat(filepath.Join(commDir, uuid+".json")); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted")
	}

	// GET after delete returns 404
	reqGet := httptest.NewRequest(http.MethodGet, "/api/community/"+uuid, nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusNotFound {
		t.Errorf("expected 404 on GET after delete, got %d", wGet.Code)
	}

	// Second DELETE returns 404
	reqDel2 := httptest.NewRequest(http.MethodDelete, "/api/community/"+uuid, nil)
	wDel2 := httptest.NewRecorder()
	srv.ServeHTTP(wDel2, reqDel2)
	if wDel2.Code != http.StatusNotFound {
		t.Errorf("expected 404 on DELETE non-existent, got %d", wDel2.Code)
	}
}

func TestCommunityCrossReferenceWithCensus(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")
	if err := os.MkdirAll(commDir, 0755); err != nil {
		t.Fatalf("failed to create commDir: %v", err)
	}

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	uuid1 := "11111111-1111-1111-1111-111111111111"
	uuid2 := "22222222-2222-2222-2222-222222222222"
	uuidDisabled := "44444444-4444-4444-4444-444444444444"
	uuidAnon := "99999999-9999-9999-9999-999999999999"

	// 1. Create telemetry events in census.jsonl
	events := []string{
		// UUID 1: first remaster
		`{"timestamp":"2026-10-23T10:00:00Z","country":"IT","uuid":"` + uuid1 + `","action":"remaster","distro":{"id":"debian","codename":"bookworm","release":"12"},"system":{"arch":"amd64","desktop":"xfce"}}`,
		// UUID 1: second remaster (latest)
		`{"timestamp":"2026-10-23T14:30:00Z","country":"IT","uuid":"` + uuid1 + `","action":"remaster","distro":{"id":"debian","codename":"trixie","release":"13"},"system":{"arch":"amd64","desktop":"xfce"}}`,
		// UUID 1: sysinstall (should not overwrite latest remaster info)
		`{"timestamp":"2026-10-23T15:00:00Z","country":"IT","uuid":"` + uuid1 + `","action":"sysinstall","distro":{"id":"debian","codename":"trixie","release":"13"},"system":{"arch":"amd64","desktop":"xfce"}}`,
		// UUID 2: one remaster
		`{"timestamp":"2026-10-23T12:00:00Z","country":"DE","uuid":"` + uuid2 + `","action":"remaster","distro":{"id":"arch","codename":"rolling","release":"rolling"},"system":{"arch":"x86_64","desktop":"kde"}}`,
		// UUID Disabled: one remaster
		`{"timestamp":"2026-10-23T11:00:00Z","country":"FR","uuid":"` + uuidDisabled + `","action":"remaster","distro":{"id":"ubuntu","codename":"noble","release":"24.04"},"system":{"arch":"amd64","desktop":"gnome"}}`,
		// Anonymous machine: remaster
		`{"timestamp":"2026-10-23T16:00:00Z","country":"US","uuid":"` + uuidAnon + `","action":"remaster","distro":{"id":"fedora","codename":"rawhide","release":"41"},"system":{"arch":"x86_64","desktop":"gnome"}}`,
	}
	censusContent := strings.Join(events, "\n") + "\n"
	if err := os.WriteFile(dataFile, []byte(censusContent), 0644); err != nil {
		t.Fatalf("failed to write census.jsonl: %v", err)
	}

	// Record census.jsonl size & content to verify privacy: file must NEVER be modified
	originalCensusBytes, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("failed to read census file: %v", err)
	}

	// 2. Save community profiles
	p1 := `{
		"uuid": "` + uuid1 + `",
		"nickname": "artisan1",
		"location": "Roma, IT",
		"bio": "Distro artisan building Debian remixes",
		"contact": "@artisan1",
		"socials": {
			"github": "artisan1-dev",
			"telegram": "@artisan1"
		},
		"source_url": "https://github.com/artisan1/distro",
		"enabled": true
	}`
	p2 := `{
		"uuid": "` + uuid2 + `",
		"nickname": "artisan2",
		"location": "Berlin, DE",
		"bio": "Arch Linux customizer",
		"socials": {
			"gitlab": "artisan2"
		},
		"source_url": "https://gitlab.com/artisan2/arch-distro",
		"enabled": true
	}`
	pDisabled := `{
		"uuid": "` + uuidDisabled + `",
		"nickname": "hidden_artisan",
		"location": "Paris, FR",
		"bio": "Disabled profile",
		"enabled": false
	}`

	for _, payload := range []string{p1, p2, pDisabled} {
		req := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(payload))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("failed to save profile: %s", w.Body.String())
		}
	}

	// 3. Test JSON view: GET /api/community?format=json
	reqJSON := httptest.NewRequest(http.MethodGet, "/api/community?format=json", nil)
	wJSON := httptest.NewRecorder()
	srv.ServeHTTP(wJSON, reqJSON)
	if wJSON.Code != http.StatusOK {
		t.Fatalf("expected 200 on JSON, got %d: %s", wJSON.Code, wJSON.Body.String())
	}
	var jsonResp struct {
		Total    int              `json:"total"`
		Profiles []ArtisanProfile `json:"profiles"`
	}
	if err := json.Unmarshal(wJSON.Body.Bytes(), &jsonResp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	// Disabled profile should be excluded from active list
	if jsonResp.Total != 2 || len(jsonResp.Profiles) != 2 {
		t.Fatalf("expected 2 active profiles, got total=%d, len=%d", jsonResp.Total, len(jsonResp.Profiles))
	}

	foundP1 := false
	foundP2 := false
	for _, prof := range jsonResp.Profiles {
		if prof.UUID == uuid1 {
			foundP1 = true
			if prof.Nickname != "artisan1" {
				t.Errorf("artisan1 nickname mismatch: %s", prof.Nickname)
			}
			if prof.RemasterCount != 2 {
				t.Errorf("expected 2 remasters for artisan1, got %d", prof.RemasterCount)
			}
			if prof.Country != "IT" {
				t.Errorf("expected country IT, got %s", prof.Country)
			}
			if prof.LastRemasterAt != "2026-10-23T14:30:00Z" {
				t.Errorf("expected last remaster at 14:30, got %s", prof.LastRemasterAt)
			}
			if prof.LatestRemaster == nil {
				t.Fatalf("expected latest_remaster block to be populated")
			}
			if prof.LatestRemaster.Distro.ID != "debian" || prof.LatestRemaster.Distro.Codename != "trixie" || prof.LatestRemaster.Distro.Release != "13" {
				t.Errorf("latest remaster distro mismatch: %+v", prof.LatestRemaster.Distro)
			}
			if prof.LatestRemaster.System.Arch != "amd64" || prof.LatestRemaster.System.Desktop != "xfce" {
				t.Errorf("latest remaster system mismatch: %+v", prof.LatestRemaster.System)
			}
			if prof.LatestRemaster.Count != 2 {
				t.Errorf("expected latest_remaster.count=2, got %d", prof.LatestRemaster.Count)
			}
		}
		if prof.UUID == uuid2 {
			foundP2 = true
			if prof.RemasterCount != 1 {
				t.Errorf("expected 1 remaster for artisan2, got %d", prof.RemasterCount)
			}
			if prof.Country != "DE" {
				t.Errorf("expected country DE, got %s", prof.Country)
			}
			if prof.LatestRemaster == nil || prof.LatestRemaster.Distro.ID != "arch" {
				t.Errorf("expected arch distro in latest_remaster: %+v", prof.LatestRemaster)
			}
		}
		if prof.UUID == uuidDisabled {
			t.Errorf("disabled profile should NOT be in active list")
		}
	}
	if !foundP1 || !foundP2 {
		t.Fatalf("did not find both expected profiles")
	}

	// 4. Test HTML view: GET /api/community?format=html and Accept: text/html
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/community?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	if wHTML.Code != http.StatusOK {
		t.Fatalf("expected 200 on HTML, got %d: %s", wHTML.Code, wHTML.Body.String())
	}
	htmlBody := wHTML.Body.String()
	for _, expected := range []string{
		"🐧 Penguins Community &amp; Distro Artisans",
		"@artisan1",
		"Roma, IT",
		"Distro artisan building Debian remixes",
		"debian trixie (13) • amd64 • xfce",
		"https://github.com/artisan1/distro",
		"rel=\"noopener noreferrer\"",
		"@artisan2",
		"Berlin, DE",
		"arch rolling • x86_64 • kde",
		"https://gitlab.com/artisan2/arch-distro",
	} {
		if !strings.Contains(htmlBody, expected) {
			t.Errorf("expected community HTML to contain %q", expected)
		}
	}
	if strings.Contains(htmlBody, "hidden_artisan") {
		t.Errorf("disabled profile should NOT be rendered in community HTML")
	}

	// 5. Test TXT view: GET /api/community?format=txt
	reqTXT := httptest.NewRequest(http.MethodGet, "/api/community?format=txt", nil)
	wTXT := httptest.NewRecorder()
	srv.ServeHTTP(wTXT, reqTXT)
	if wTXT.Code != http.StatusOK {
		t.Fatalf("expected 200 on TXT, got %d", wTXT.Code)
	}
	txtBody := wTXT.Body.String()
	for _, expected := range []string{
		"# 🐧 Penguins Community & Distro Artisans",
		"Nickname",
		"Location",
		"Ultima Distro",
		"Source URL",
		"Contatti",
		"@artisan1",
		"debian trixie (13) • amd64 • xfce",
		"@artisan2",
		"arch rolling • x86_64 • kde",
	} {
		if !strings.Contains(txtBody, expected) {
			t.Errorf("expected community TXT to contain %q", expected)
		}
	}

	// 6. Test Dashboard integration: GET /api/census?format=html
	reqCensus := httptest.NewRequest(http.MethodGet, "/api/census?format=html", nil)
	wCensus := httptest.NewRecorder()
	srv.ServeHTTP(wCensus, reqCensus)
	if wCensus.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/census HTML, got %d", wCensus.Code)
	}
	censusHTML := wCensus.Body.String()

	// Verify "🐧 Penguins Community Artisans" section exists with link to /api/community
	if !strings.Contains(censusHTML, "🐧 Penguins Community Artisans") {
		t.Errorf("expected census HTML to contain '🐧 Penguins Community Artisans'")
	}
	if !strings.Contains(censusHTML, `id="community-artisans"`) {
		t.Errorf("expected census HTML to have panel id=\"community-artisans\"")
	}
	if !strings.Contains(censusHTML, `/api/community`) {
		t.Errorf("expected census HTML to link to /api/community")
	}

	// Verify Recent Events table:
	// Events from artisan1 should have clickable @artisan1 badge
	if !strings.Contains(censusHTML, `class="artisan-badge"`) || !strings.Contains(censusHTML, `@artisan1`) {
		t.Errorf("expected recent events table to have @artisan1 badge")
	}
	// Anonymous events (uuidAnon) must NOT display any badge or nickname
	if strings.Contains(censusHTML, uuidAnon) {
		t.Errorf("anonymous events must NOT display uuid")
	}

	// 7. PRIVACY CHECK: Verify census.jsonl file on disk was NEVER modified
	currentCensusBytes, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("failed to re-read census file: %v", err)
	}
	if !bytes.Equal(originalCensusBytes, currentCensusBytes) {
		t.Fatalf("census.jsonl su disco must NEVER be modified! Bytes differ.")
	}
}

func TestArtisanTokenAuthAndMultiMachine(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	defer srv.Close()
	srv.SetCommunityDir(commDir)

	// 1. Generate token
	rawToken, tokenHash, err := GenerateArtisanToken()
	if err != nil {
		t.Fatalf("GenerateArtisanToken failed: %v", err)
	}
	if !strings.HasPrefix(rawToken, "egg_pat_") {
		t.Errorf("token must start with egg_pat_, got: %s", rawToken)
	}
	if HashArtisanToken(rawToken) != tokenHash {
		t.Errorf("HashArtisanToken mismatch")
	}

	// 2. Register community profile with token_hash
	profilePayload := `{
		"id": "distromaster",
		"uuid": "distromaster",
		"nickname": "distromaster",
		"location": "Napoli, IT",
		"token_hash": "` + tokenHash + `",
		"source_url": "https://distromaster.org"
	}`
	reqComm := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(profilePayload))
	reqComm.Header.Set("Content-Type", "application/json")
	wComm := httptest.NewRecorder()
	srv.ServeHTTP(wComm, reqComm)
	if wComm.Code != http.StatusOK {
		t.Fatalf("failed to create community profile: %s", wComm.Body.String())
	}

	// 3. Scenario A: 1 Artisan sending remaster from 2 distinct machines (M1, M2)
	// Machine M1: X-Eggs-Artisan-Token header
	m1Payload := `{
		"uuid": "M1",
		"eggs_version": "10.1.0",
		"action": "remaster",
		"distro": {"id": "debian", "codename": "trixie", "release": "13"},
		"system": {"arch": "amd64", "desktop": "xfce"}
	}`
	reqM1 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(m1Payload))
	reqM1.Header.Set("Content-Type", "application/json")
	reqM1.Header.Set("X-Eggs-Artisan-Token", rawToken)
	wM1 := httptest.NewRecorder()
	srv.ServeHTTP(wM1, reqM1)
	if wM1.Code != http.StatusOK {
		t.Fatalf("M1 POST failed: %d: %s", wM1.Code, wM1.Body.String())
	}

	// Machine M2: Authorization: Bearer header
	m2Payload := `{
		"uuid": "M2",
		"eggs_version": "10.1.0",
		"action": "remaster",
		"distro": {"id": "debian", "codename": "trixie", "release": "13"},
		"system": {"arch": "arm64", "desktop": "gnome"}
	}`
	reqM2 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(m2Payload))
	reqM2.Header.Set("Content-Type", "application/json")
	reqM2.Header.Set("Authorization", "Bearer "+rawToken)
	wM2 := httptest.NewRecorder()
	srv.ServeHTTP(wM2, reqM2)
	if wM2.Code != http.StatusOK {
		t.Fatalf("M2 POST failed: %d: %s", wM2.Code, wM2.Body.String())
	}

	// Verify Scenario A on /api/community/distromaster
	reqGetA := httptest.NewRequest(http.MethodGet, "/api/community/distromaster", nil)
	wGetA := httptest.NewRecorder()
	srv.ServeHTTP(wGetA, reqGetA)
	if wGetA.Code != http.StatusOK {
		t.Fatalf("GET /api/community/distromaster failed: %d: %s", wGetA.Code, wGetA.Body.String())
	}

	var profA ArtisanProfile
	if err := json.Unmarshal(wGetA.Body.Bytes(), &profA); err != nil {
		t.Fatalf("failed to decode artisan profile: %v", err)
	}
	if profA.MachinesCount != 2 {
		t.Errorf("expected MachinesCount == 2, got %d", profA.MachinesCount)
	}
	if profA.RemasterCount != 2 {
		t.Errorf("expected RemasterCount == 2, got %d", profA.RemasterCount)
	}
	if len(profA.LiveDistros) != 2 {
		t.Fatalf("expected LiveDistros length == 2, got %d: %+v", len(profA.LiveDistros), profA.LiveDistros)
	}

	// 4. Scenario B: 3 users sending sysinstall from new machines (U1, U2, U3) with parent_id: "M1" or "M2" (without token)
	users := []struct {
		uuid     string
		parentID string
		arch     string
		desktop  string
	}{
		{uuid: "U1", parentID: "M1", arch: "amd64", desktop: "xfce"},
		{uuid: "U2", parentID: "M2", arch: "arm64", desktop: "gnome"},
		{uuid: "U3", parentID: "M1", arch: "amd64", desktop: "xfce"},
	}

	for _, u := range users {
		uPayload := `{
			"uuid": "` + u.uuid + `",
			"parent_id": "` + u.parentID + `",
			"eggs_version": "10.1.0",
			"action": "sysinstall",
			"distro": {"id": "debian", "codename": "trixie", "release": "13"},
			"system": {"arch": "` + u.arch + `", "desktop": "` + u.desktop + `"}
		}`
		reqU := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(uPayload))
		reqU.Header.Set("Content-Type", "application/json")
		wU := httptest.NewRecorder()
		srv.ServeHTTP(wU, reqU)
		if wU.Code != http.StatusOK {
			t.Fatalf("user %s POST failed: %d: %s", u.uuid, wU.Code, wU.Body.String())
		}
	}

	// Verify Scenario B on /api/community/distromaster -> InstallsCount == 3
	reqGetB := httptest.NewRequest(http.MethodGet, "/api/community/distromaster", nil)
	wGetB := httptest.NewRecorder()
	srv.ServeHTTP(wGetB, reqGetB)
	if wGetB.Code != http.StatusOK {
		t.Fatalf("GET /api/community/distromaster failed: %d", wGetB.Code)
	}
	var profB ArtisanProfile
	if err := json.Unmarshal(wGetB.Body.Bytes(), &profB); err != nil {
		t.Fatalf("failed to decode artisan profile B: %v", err)
	}
	if profB.InstallsCount != 3 {
		t.Errorf("expected InstallsCount == 3, got %d", profB.InstallsCount)
	}
	if profB.MachinesCount != 2 {
		t.Errorf("expected MachinesCount == 2 (should not count child user machines), got %d", profB.MachinesCount)
	}
	if profB.RemasterCount != 2 {
		t.Errorf("expected RemasterCount == 2, got %d", profB.RemasterCount)
	}

	// 5. Verify /api/census?format=json TopInstallations and RecentEvents enrichment
	reqCensusJSON := httptest.NewRequest(http.MethodGet, "/api/census?format=json", nil)
	wCensusJSON := httptest.NewRecorder()
	srv.ServeHTTP(wCensusJSON, reqCensusJSON)
	if wCensusJSON.Code != http.StatusOK {
		t.Fatalf("GET /api/census failed: %d", wCensusJSON.Code)
	}

	var censusSum CensusSummary
	if err := json.Unmarshal(wCensusJSON.Body.Bytes(), &censusSum); err != nil {
		t.Fatalf("failed to decode census summary: %v", err)
	}

	// Verify TopInstallations are enriched with artisan nickname & URL
	if len(censusSum.TopInstallations) == 0 {
		t.Fatalf("expected TopInstallations to not be empty")
	}
	foundTopArtisan := false
	for _, ti := range censusSum.TopInstallations {
		if ti.ArtisanNickname == "distromaster" && ti.ArtisanURL == "https://distromaster.org" {
			foundTopArtisan = true
			if ti.SumSysinstall < 1 {
				t.Errorf("expected positive sum_sysinstall for artisan top install, got %d", ti.SumSysinstall)
			}
		}
	}
	if !foundTopArtisan {
		t.Errorf("expected TopInstallations to contain artisan 'distromaster': %+v", censusSum.TopInstallations)
	}

	// Verify RecentEvents are enriched with artisan nickname & URL for child installs
	foundRecentChild := false
	for _, ev := range censusSum.RecentEvents {
		if ev.Sysinstall && ev.ArtisanNickname == "distromaster" && ev.ArtisanURL == "https://distromaster.org" {
			foundRecentChild = true
		}
	}
	if !foundRecentChild {
		t.Errorf("expected RecentEvents to have child sysinstall with distromaster artisan: %+v", censusSum.RecentEvents)
	}

	// 6. Verify HTML output has artisan badges in Top 10 Sysinstalls and Recent Events
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/census?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	html := wHTML.Body.String()
	if !strings.Contains(html, "@distromaster") {
		t.Errorf("expected HTML summary to contain @distromaster, got:\n%s", html)
	}
	if !strings.Contains(html, "https://distromaster.org") {
		t.Errorf("expected HTML summary to contain artisan URL, got:\n%s", html)
	}

	// 7. Test transitive lineage: child machine U1 creates ISO or installs U4
	u4Payload := `{
		"uuid": "U4",
		"parent_id": "U1",
		"eggs_version": "10.1.0",
		"action": "sysinstall",
		"distro": {"id": "debian", "codename": "trixie", "release": "13"},
		"system": {"arch": "amd64", "desktop": "xfce"}
	}`
	reqU4 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(u4Payload))
	reqU4.Header.Set("Content-Type", "application/json")
	wU4 := httptest.NewRecorder()
	srv.ServeHTTP(wU4, reqU4)
	if wU4.Code != http.StatusOK {
		t.Fatalf("U4 POST failed: %d", wU4.Code)
	}

	// After transitive install, InstallsCount should be 4
	reqGetC := httptest.NewRequest(http.MethodGet, "/api/community/distromaster", nil)
	wGetC := httptest.NewRecorder()
	srv.ServeHTTP(wGetC, reqGetC)
	var profC ArtisanProfile
	_ = json.Unmarshal(wGetC.Body.Bytes(), &profC)
	if profC.InstallsCount != 4 {
		t.Errorf("expected transitive InstallsCount == 4, got %d", profC.InstallsCount)
	}
}

func TestArtisanTokenValidationAndSpoofingPrevention(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	defer srv.Close()
	srv.SetCommunityDir(commDir)

	_, tokenHash, _ := GenerateArtisanToken()

	// Create valid profile
	_ = srv.saveCommunityProfile(CommunityProfile{
		ID:        "target-artisan",
		UUID:      "target-artisan",
		TokenHash: tokenHash,
		Nickname:  "target",
	})

	// 1. Post with wrong token: should NOT fail (returns 200 OK), but should NOT set artisan_id
	wrongPayload := `{"uuid":"rogue-machine-1","eggs_version":"10.0","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64"}}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(wrongPayload))
	req1.Header.Set("X-Eggs-Artisan-Token", "egg_pat_badtoken00000000000000000000000000000000000000000000000000000000")
	w1 := httptest.NewRecorder()
	srv.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on invalid token fallback, got %d", w1.Code)
	}

	// 2. Post with spoofed artisan_id in body without token: should NOT be attributed
	spoofPayload := `{"uuid":"rogue-machine-2","artisan_id":"target-artisan","eggs_version":"10.0","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64"}}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(spoofPayload))
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on body-only artisan_id, got %d", w2.Code)
	}

	// Verify profile stats: target-artisan must have 0 remasters and 0 machines!
	reqGet := httptest.NewRequest(http.MethodGet, "/api/community/target-artisan", nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)
	var prof ArtisanProfile
	_ = json.Unmarshal(wGet.Body.Bytes(), &prof)
	if prof.RemasterCount != 0 || prof.MachinesCount != 0 {
		t.Errorf("spoofing prevention failed: expected RemasterCount=0, MachinesCount=0, got remasters=%d machines=%d", prof.RemasterCount, prof.MachinesCount)
	}
}

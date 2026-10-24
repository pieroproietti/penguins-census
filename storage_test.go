package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	defer srv.Close()

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			body := `{"uuid":"concurrent-uuid","eggs_version":"10.0.0","action":"remaster","distro":{"id":"ubuntu"},"system":{"arch":"amd64"}}`
			req := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(body))
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("concurrent POST failed with %d", w.Code)
			}
		}()
	}
	wg.Wait()

	req := httptest.NewRequest(http.MethodGet, "/api/census", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var summary CensusSummary
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to decode summary: %v", err)
	}
	if summary.TotalEvents != workers || summary.UniqueMachines != 1 {
		t.Fatalf("expected %d events and 1 unique machine, got %+v", workers, summary)
	}
}

func TestResetCensusData(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")

	// Reset on non-existent file should succeed without creating a file
	if err := resetCensusData(dataFile); err != nil {
		t.Fatalf("expected nil error on missing file, got %v", err)
	}
	if _, err := os.Stat(dataFile); !os.IsNotExist(err) {
		t.Fatalf("expected missing file to stay uncreated after reset")
	}

	srv := NewCensusServer(dataFile, "")
	defer srv.Close()

	// Post an event
	body := `{"uuid":"u-reset","eggs_version":"10.1.0","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64"}}`
	reqPost := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(body))
	wPost := httptest.NewRecorder()
	srv.ServeHTTP(wPost, reqPost)
	if wPost.Code != http.StatusOK {
		t.Fatalf("POST failed: %d", wPost.Code)
	}

	// Reset census data
	if err := resetCensusData(dataFile); err != nil {
		t.Fatalf("resetCensusData failed: %v", err)
	}

	// Verify file is truncated to 0 bytes and GET returns 0 counts
	info, err := os.Stat(dataFile)
	if err != nil {
		t.Fatalf("stat failed after reset: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected 0 bytes after reset, got %d", info.Size())
	}

	reqGet := httptest.NewRequest(http.MethodGet, "/api/census?format=json", nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)
	var summary CensusSummary
	if err := json.Unmarshal(wGet.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if summary.TotalEvents != 0 || summary.UniqueMachines != 0 {
		t.Fatalf("expected 0 events after reset, got %+v", summary)
	}
	if len(summary.RecentEvents) != 0 {
		t.Fatalf("expected 0 recent events after reset, got %d", len(summary.RecentEvents))
	}
}

func TestClonesByAction(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	defer srv.Close()

	events := []string{
		`{"uuid":"u1","action":"remaster","options":{"clone":true}}`,
		`{"uuid":"u2","action":"remaster","options":{"clone":false}}`,
		`{"uuid":"u3","action":"sysinstall","options":{"clone":true}}`,
		`{"uuid":"u4","action":"sysinstall","options":{"clone":true}}`,
		`{"uuid":"u5","action":"sysinstall","options":{"clone":false}}`,
	}
	for _, ev := range events {
		req := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(ev))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST failed: %d", w.Code)
		}
	}

	// 1. JSON
	reqGet := httptest.NewRequest(http.MethodGet, "/api/census?format=json", nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("GET failed: %d", wGet.Code)
	}
	var sum CensusSummary
	if err := json.Unmarshal(wGet.Body.Bytes(), &sum); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if sum.RemasterClones != 1 {
		t.Errorf("expected remaster_clones=1, got %d", sum.RemasterClones)
	}
	if sum.SysinstallClones != 2 {
		t.Errorf("expected sysinstall_clones=2, got %d", sum.SysinstallClones)
	}

	// 2. TXT
	reqTxt := httptest.NewRequest(http.MethodGet, "/api/census?format=txt", nil)
	wTxt := httptest.NewRecorder()
	srv.ServeHTTP(wTxt, reqTxt)
	txt := wTxt.Body.String()
	if !strings.Contains(txt, "- Remasters/Clones:    2/1") {
		t.Errorf("expected TXT to contain '- Remasters/Clones:    2/1', got:\n%s", txt)
	}
	if !strings.Contains(txt, "- Sysinstalls/Clones:  3/2") {
		t.Errorf("expected TXT to contain '- Sysinstalls/Clones:  3/2', got:\n%s", txt)
	}

	// 3. HTML
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/census?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	html := wHTML.Body.String()
	if !strings.Contains(html, "Remasters/Clones") || !strings.Contains(html, "Sysinstalls/Clones") {
		t.Errorf("expected HTML to contain both clone cards")
	}
	if !strings.Contains(html, "2/1") || !strings.Contains(html, "3/2") {
		t.Errorf("expected HTML to contain '2/1' and '3/2'")
	}
}

func TestRecentEventsLimitAndOrder(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	defer srv.Close()

	// Post 12 events in sequence to test limit of 10
	distros := []string{
		"debian", "ubuntu", "arch", "fedora", "manjaro",
		"opensuse", "void", "alpine", "gentoo", "kali",
		"mint", "popos",
	}
	for i, d := range distros {
		action := "remaster"
		if i%2 == 1 {
			action = "sysinstall"
		}
		ev := fmt.Sprintf(`{"uuid":"u%d","eggs_version":"10.0","action":%q,"distro":{"id":%q},"system":{"desktop":"desktop%d"}}`,
			i+1, action, d, i+1)
		req := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(ev))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST failed: %d", w.Code)
		}
	}

	// Verify JSON format has exactly 10 recent events: popos down to arch
	reqGet := httptest.NewRequest(http.MethodGet, "/api/census?format=json", nil)
	wGet := httptest.NewRecorder()
	srv.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("GET failed: %d", wGet.Code)
	}

	var sum CensusSummary
	if err := json.Unmarshal(wGet.Body.Bytes(), &sum); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if sum.TotalEvents != 12 {
		t.Fatalf("expected 12 total events, got %d", sum.TotalEvents)
	}
	if len(sum.RecentEvents) != 10 {
		t.Fatalf("expected 10 recent events, got %d", len(sum.RecentEvents))
	}

	// 1st recent event should be popos (from 12th event)
	if sum.RecentEvents[0].Distro != "popos" || sum.RecentEvents[0].Desktop != "desktop12" ||
		sum.RecentEvents[0].Remaster || !sum.RecentEvents[0].Sysinstall {
		t.Errorf("expected 1st recent event to be popos/sysinstall, got %+v", sum.RecentEvents[0])
	}
	// Verify date format
	if sum.RecentEvents[0].Date == "" {
		t.Errorf("expected non-empty date in recent_events[0]")
	}
	if _, err := time.Parse("2006-01-02 15:04:05", sum.RecentEvents[0].Date); err != nil {
		t.Errorf("expected date formatted as 'YYYY-MM-DD HH:MM:SS', got %q", sum.RecentEvents[0].Date)
	}

	// 2nd recent event should be mint (from 11th event)
	if sum.RecentEvents[1].Distro != "mint" || sum.RecentEvents[1].Desktop != "desktop11" ||
		!sum.RecentEvents[1].Remaster || sum.RecentEvents[1].Sysinstall {
		t.Errorf("expected 2nd recent event to be mint/remaster, got %+v", sum.RecentEvents[1])
	}

	// 10th recent event should be arch (from 3rd event)
	if sum.RecentEvents[9].Distro != "arch" || sum.RecentEvents[9].Desktop != "desktop3" ||
		!sum.RecentEvents[9].Remaster || sum.RecentEvents[9].Sysinstall {
		t.Errorf("expected 10th recent event to be arch/remaster, got %+v", sum.RecentEvents[9])
	}

	// Verify HTML contains the recent events table with headers and rows
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/census?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	htmlBody := wHTML.Body.String()
	for _, expected := range []string{
		"Recent Events",
		"<th>Date</th>",
		"popos",
		"mint",
		"kali",
		"desktop12",
		"&#10003;", // checkmark for remaster/sysinstall
	} {
		if !strings.Contains(htmlBody, expected) {
			t.Errorf("expected HTML to contain %q", expected)
		}
	}

	// Verify text / markdown output contains Date header
	reqTxt := httptest.NewRequest(http.MethodGet, "/api/census?format=txt", nil)
	wTxt := httptest.NewRecorder()
	srv.ServeHTTP(wTxt, reqTxt)
	txtBody := wTxt.Body.String()
	for _, expected := range []string{
		"### Recent Events",
		"| Date                | Country | Distro | Desktop | Display Server | Remaster | Sysinstall |",
		"popos",
		"mint",
	} {
		if !strings.Contains(txtBody, expected) {
			t.Errorf("expected Text to contain %q", expected)
		}
	}
}

func TestTopInstallationsRankingAndDeduplication(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")

	srv := NewCensusServer(dataFile, "")
	defer srv.Close()

	events := []string{
		`{"timestamp":"2026-10-21T09:00:00Z","uuid":"u1","country":"IT","eggs_version":"v26.10.21","action":"sysinstall","distro":{"id":"openkylin"},"system":{"arch":"amd64","desktop":"cli"}}`,
		`{"timestamp":"2026-10-21T09:01:00Z","uuid":"u1","country":"IT","eggs_version":"v26.10.21","action":"sysinstall","distro":{"id":"openkylin"},"system":{"arch":"amd64","desktop":"cli"}}`,
		`{"timestamp":"2026-10-21T09:02:00Z","uuid":"u2","country":"IT","eggs_version":"v26.10.21","action":"sysinstall","distro":{"id":"openkylin"},"system":{"arch":"amd64","desktop":"cli"}}`,
		`{"timestamp":"2026-10-21T09:03:00Z","uuid":"u3","country":"IT","eggs_version":"v26.10.21","action":"sysinstall","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"xfce"}}`,
		`{"timestamp":"2026-10-21T09:04:00Z","uuid":"u4","country":"IT","eggs_version":"v26.10.21","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"xfce"}}`,
		`{"timestamp":"2026-10-21T09:05:00Z","uuid":"u5","country":"US","eggs_version":"v26.10.20","action":"sysinstall","distro":{"id":"ubuntu"},"system":{"arch":"amd64","desktop":"gnome"}}`,
		`{"timestamp":"2026-10-21T09:06:00Z","uuid":"u6","country":"DE","eggs_version":"v26.10.19","action":"sysinstall","distro":{"id":"arch"},"system":{"arch":"arm64","desktop":"kde"}}`,
	}

	content := strings.Join(events, "\n") + "\n"
	if err := os.WriteFile(dataFile, []byte(content), 0640); err != nil {
		t.Fatalf("failed to write dataFile: %v", err)
	}

	// 1. Verify JSON summary
	reqJSON := httptest.NewRequest(http.MethodGet, "/api/census?format=json", nil)
	wJSON := httptest.NewRecorder()
	srv.ServeHTTP(wJSON, reqJSON)
	if wJSON.Code != http.StatusOK {
		t.Fatalf("GET json failed: %d", wJSON.Code)
	}

	var summary CensusSummary
	if err := json.Unmarshal(wJSON.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if len(summary.TopInstallations) != 4 {
		t.Fatalf("expected 4 distinct sysinstall combinations, got %d: %+v", len(summary.TopInstallations), summary.TopInstallations)
	}

	// Top 1 must be openkylin IT with sum_sysinstall = 2
	top1 := summary.TopInstallations[0]
	if top1.Distro != "openkylin" || top1.Country != "IT" || top1.Desktop != "cli" ||
		top1.DisplayServer != "headless/cli" || top1.Arch != "amd64" || top1.EggsVersion != "v26.10.21" || top1.SumSysinstall != 2 {
		t.Errorf("unexpected top 1: %+v", top1)
	}

	// 2. Verify plain text / markdown output contains the table with required columns
	reqTxt := httptest.NewRequest(http.MethodGet, "/api/census?format=txt", nil)
	wTxt := httptest.NewRecorder()
	srv.ServeHTTP(wTxt, reqTxt)
	txtBody := wTxt.Body.String()

	for _, col := range []string{
		"### Top 10 Sysinstalls",
		"Country",
		"Distro",
		"Desktop",
		"Display Server",
		"Arch",
		"Version",
		"Sysinstalls",
		"openkylin",
		"debian",
		"ubuntu",
		"arch",
	} {
		if !strings.Contains(txtBody, col) {
			t.Errorf("expected text report to contain %q, got:\n%s", col, txtBody)
		}
	}

	// Verify Top 10 Sysinstalls appears before Recent Events, with Recent Events at the very end
	idxTop := strings.Index(txtBody, "### Top 10 Sysinstalls")
	idxRecent := strings.Index(txtBody, "### Recent Events")
	if idxRecent == -1 || idxTop == -1 || idxRecent <= idxTop {
		t.Errorf("expected Top 10 Sysinstalls to appear before Recent Events (Recent Events in finale) in text summary")
	}

	// 3. Verify HTML output
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/census?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	htmlBody := wHTML.Body.String()

	idxHTMLTop := strings.Index(htmlBody, `id="top-sysinstalls"`)
	idxHTMLRecent := strings.Index(htmlBody, `id="recent-events"`)
	if idxHTMLTop == -1 || idxHTMLRecent == -1 || idxHTMLRecent <= idxHTMLTop {
		t.Errorf("expected top-sysinstalls panel to appear before recent-events panel in HTML")
	}

	if !strings.Contains(htmlBody, `id="top-sysinstalls"`) {
		t.Errorf("expected HTML to contain id=\"top-sysinstalls\"")
	}
	if !strings.Contains(htmlBody, "Top 10 Sysinstalls") {
		t.Errorf("expected HTML to contain Top 10 Sysinstalls")
	}
	if !strings.Contains(htmlBody, "openkylin") {
		t.Errorf("expected HTML to contain openkylin")
	}
}

func TestTopInstallationsFromDirectory(t *testing.T) {
	logDir := t.TempDir()

	file1 := filepath.Join(logDir, "day1.jsonl")
	file2 := filepath.Join(logDir, "day2.jsonl")

	data1 := `{"uuid":"uuid-a","country":"FR","eggs_version":"v26.10.21","action":"sysinstall","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"xfce"}}` + "\n"
	data2 := `{"uuid":"uuid-b","country":"FR","eggs_version":"v26.10.21","action":"sysinstall","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"xfce"}}` + "\n"

	if err := os.WriteFile(file1, []byte(data1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte(data2), 0644); err != nil {
		t.Fatal(err)
	}

	summary, err := loadCensusSummary(logDir)
	if err != nil {
		t.Fatalf("loadCensusSummary failed on directory: %v", err)
	}

	if summary.TotalEvents != 2 {
		t.Fatalf("expected 2 total events, got %d", summary.TotalEvents)
	}
	if len(summary.TopInstallations) != 1 {
		t.Fatalf("expected 1 top installation group, got %d", len(summary.TopInstallations))
	}
	if summary.TopInstallations[0].SumSysinstall != 2 {
		t.Fatalf("expected sum_sysinstall=2, got %d", summary.TopInstallations[0].SumSysinstall)
	}
}

func TestCommunityDecouplingFromCensusTelemetry(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	// 1. Post census telemetry to /api/census with extra community-like fields
	censusPayload := `{
		"uuid": "census-uuid-12345",
		"eggs_version": "v26.10.23",
		"action": "remaster",
		"distro": {"id": "debian", "codename": "trixie"},
		"system": {"arch": "amd64", "desktop": "xfce", "display_server": "x11"},
		"options": {"clone": false},
		"nickname": "rogue-field",
		"community": {"nickname": "should-not-persist-in-census"}
	}`
	reqCensus := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(censusPayload))
	reqCensus.Header.Set("Content-Type", "application/json")
	wCensus := httptest.NewRecorder()
	srv.ServeHTTP(wCensus, reqCensus)

	if wCensus.Code != http.StatusOK {
		t.Fatalf("expected 200 on census POST, got %d", wCensus.Code)
	}

	// Read census.jsonl line: verify NO rogue community field was saved
	censusLineBytes, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("failed to read census file: %v", err)
	}
	censusLine := string(censusLineBytes)
	if strings.Contains(censusLine, "should-not-persist-in-census") || strings.Contains(censusLine, "rogue-field") {
		t.Errorf("census.jsonl is contaminated with community data: %s", censusLine)
	}
	if !strings.Contains(censusLine, `"uuid":"census-uuid-12345"`) || !strings.Contains(censusLine, `"action":"remaster"`) {
		t.Errorf("census.jsonl missing valid telemetry: %s", censusLine)
	}

	// 2. Post community profile to /api/community
	commPayload := `{
		"uuid": "community-uuid-99999",
		"nickname": "community-user",
		"location": "Berlin, Germany",
		"bio": "Open source advocate",
		"contact": "@commuser",
		"source_url": "https://github.com/commuser/repo"
	}`
	reqComm := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(commPayload))
	reqComm.Header.Set("Content-Type", "application/json")
	wComm := httptest.NewRecorder()
	srv.ServeHTTP(wComm, reqComm)

	if wComm.Code != http.StatusOK {
		t.Fatalf("expected 200 on community POST, got %d", wComm.Code)
	}

	// Verify community file exists separately
	commFile := filepath.Join(commDir, "community-uuid-99999.json")
	if _, err := os.Stat(commFile); err != nil {
		t.Fatalf("expected community file at %s: %v", commFile, err)
	}

	// Verify census file has not received community profile
	censusLineBytesAfter, _ := os.ReadFile(dataFile)
	if strings.Contains(string(censusLineBytesAfter), "community-uuid-99999") || strings.Contains(string(censusLineBytesAfter), "community-user") {
		t.Errorf("census.jsonl contains community profile data: %s", string(censusLineBytesAfter))
	}

	// Verify /api/census reports do not include community uuid or profile
	reqRep := httptest.NewRequest(http.MethodGet, "/api/census?format=txt", nil)
	wRep := httptest.NewRecorder()
	srv.ServeHTTP(wRep, reqRep)
	repTxt := wRep.Body.String()
	if strings.Contains(repTxt, "community-user") || strings.Contains(repTxt, "Berlin") {
		t.Errorf("census text report contains community profile data: %s", repTxt)
	}
}

func TestTokenGenerationAndStorage(t *testing.T) {
	rawToken, tokenHash, err := GenerateArtisanToken()
	if err != nil {
		t.Fatalf("GenerateArtisanToken error: %v", err)
	}
	if !strings.HasPrefix(rawToken, "egg_pat_") {
		t.Fatalf("expected egg_pat_ prefix, got %s", rawToken)
	}
	if len(rawToken) != 8+64 {
		t.Fatalf("expected 72 char token, got %d", len(rawToken))
	}
	if HashArtisanToken(rawToken) != tokenHash {
		t.Fatalf("HashArtisanToken mismatch")
	}

	tmpDir := t.TempDir()
	commDir := filepath.Join(tmpDir, "community")
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	srv.SetCommunityDir(commDir)

	// Test profile storage using ID
	prof := CommunityProfile{
		ID:        "artisan-test-id",
		UUID:      "artisan-test-uuid",
		Nickname:  "artisan-tester",
		TokenHash: tokenHash,
	}
	if err := srv.saveCommunityProfile(prof); err != nil {
		t.Fatalf("saveCommunityProfile error: %v", err)
	}

	// Verify file is saved as <id>.json
	if _, err := os.Stat(filepath.Join(commDir, "artisan-test-id.json")); err != nil {
		t.Fatalf("expected file artisan-test-id.json to exist: %v", err)
	}

	// Test getCommunityProfile by ID and by UUID
	byID, err := srv.getCommunityProfile("artisan-test-id")
	if err != nil || byID.Nickname != "artisan-tester" {
		t.Fatalf("failed getCommunityProfile by ID: %v", err)
	}
	byUUID, err := srv.getCommunityProfile("artisan-test-uuid")
	if err != nil || byUUID.Nickname != "artisan-tester" {
		t.Fatalf("failed getCommunityProfile by UUID: %v", err)
	}

	// Test findProfileByToken
	found, err := srv.findProfileByToken(rawToken)
	if err != nil {
		t.Fatalf("findProfileByToken error: %v", err)
	}
	if found.GetID() != "artisan-test-id" {
		t.Fatalf("expected ID artisan-test-id, got %s", found.GetID())
	}

	// Test with invalid token
	_, err = srv.findProfileByToken("egg_pat_invalid")
	if err == nil {
		t.Fatalf("expected error for invalid token, got nil")
	}

	// Test deleteCommunityProfile by UUID
	if err := srv.deleteCommunityProfile("artisan-test-uuid"); err != nil {
		t.Fatalf("delete error: %v", err)
	}
	_, err = srv.getCommunityProfile("artisan-test-id")
	if err == nil {
		t.Fatalf("expected error after delete, got nil")
	}
}

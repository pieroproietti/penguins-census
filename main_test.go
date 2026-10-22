package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
		"system": { "arch": "amd64", "desktop": "xfce", "virt": "kvm", "init": "systemd" },
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
		"system": { "arch": "amd64", "desktop": "xfce", "virt": "none", "init": "systemd" },
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
		"system": { "arch": "arm64", "desktop": "gnome", "virt": "none", "init": "systemd" },
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
	if summary.CloneCount != 1 {
		t.Errorf("expected clone_count=1, got %d", summary.CloneCount)
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
	if summary.ByVirt["none"] != 2 || summary.ByVirt["kvm"] != 1 {
		t.Errorf("unexpected by_virt: %+v", summary.ByVirt)
	}
	if summary.ByInit["systemd"] != 3 {
		t.Errorf("unexpected by_init: %+v", summary.ByInit)
	}
	if summary.ByCompression["zstd"] != 2 || summary.ByCompression["xz"] != 1 {
		t.Errorf("unexpected by_compression: %+v", summary.ByCompression)
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
	// Most recent (event 3): arch, gnome, remaster
	if summary.RecentEvents[0].Country != "XX" || summary.RecentEvents[0].Distro != "arch" ||
		summary.RecentEvents[0].Desktop != "gnome" || !summary.RecentEvents[0].Remaster || summary.RecentEvents[0].Sysinstall {
		t.Errorf("unexpected recent_events[0]: %+v", summary.RecentEvents[0])
	}
	// Second recent (event 2): debian, xfce, sysinstall
	if summary.RecentEvents[1].Country != "XX" || summary.RecentEvents[1].Distro != "debian" ||
		summary.RecentEvents[1].Desktop != "xfce" || summary.RecentEvents[1].Remaster || !summary.RecentEvents[1].Sysinstall {
		t.Errorf("unexpected recent_events[1]: %+v", summary.RecentEvents[1])
	}
	// Third recent (event 1): debian, xfce, remaster
	if summary.RecentEvents[2].Country != "XX" || summary.RecentEvents[2].Distro != "debian" ||
		summary.RecentEvents[2].Desktop != "xfce" || !summary.RecentEvents[2].Remaster || summary.RecentEvents[2].Sysinstall {
		t.Errorf("unexpected recent_events[2]: %+v", summary.RecentEvents[2])
	}
}

func TestCensusFormatsAndFallbacks(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	defer srv.Close()

	// Post event with empty desktop and empty virt to verify fallback defaults
	events := []string{
		`{"uuid":"u1","eggs_version":"10.1.0","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"","virt":"","init":"systemd"},"options":{"clone":true,"compression":"zstd","coexist":false}}`,
		`{"uuid":"u2","eggs_version":"10.1.0","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"kde","virt":"kvm","init":"systemd"},"options":{"clone":false,"compression":"xz","coexist":true}}`,
		`{"uuid":"u3","eggs_version":"10.0.9","action":"sysinstall","distro":{"id":"ubuntu"},"system":{"arch":"arm64","desktop":"kde","virt":"","init":"openrc"},"options":{"clone":false,"compression":"zstd","coexist":false}}`,
	}
	for _, ev := range events {
		req := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(ev))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST failed: %d", w.Code)
		}
	}

	// 1. ?format=json
	reqJSON := httptest.NewRequest(http.MethodGet, "/api/census?format=json", nil)
	wJSON := httptest.NewRecorder()
	srv.ServeHTTP(wJSON, reqJSON)
	if wJSON.Code != http.StatusOK {
		t.Fatalf("expected 200 for ?format=json, got %d", wJSON.Code)
	}
	var sum CensusSummary
	if err := json.Unmarshal(wJSON.Body.Bytes(), &sum); err != nil {
		t.Fatalf("failed to decode ?format=json: %v", err)
	}
	if sum.ByDesktop["headless/cli"] != 1 || sum.ByDesktop["kde"] != 2 {
		t.Errorf("expected headless/cli=1 and kde=2, got %+v", sum.ByDesktop)
	}
	if sum.ByVirt["physical/none"] != 2 || sum.ByVirt["kvm"] != 1 {
		t.Errorf("expected physical/none=2 and kvm=1, got %+v", sum.ByVirt)
	}

	// 2. ?format=txt and User-Agent: curl/8.5.0
	for _, tc := range []struct {
		name string
		url  string
		ua   string
	}{
		{name: "query format=txt", url: "/api/census?format=txt", ua: "Mozilla/5.0"},
		{name: "curl user-agent", url: "/api/census", ua: "curl/8.5.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reqTxt := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if tc.ua != "" {
				reqTxt.Header.Set("User-Agent", tc.ua)
			}
			wTxt := httptest.NewRecorder()
			srv.ServeHTTP(wTxt, reqTxt)
			if wTxt.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", wTxt.Code)
			}
			ct := wTxt.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("expected text/plain Content-Type, got %q", ct)
			}
			body := wTxt.Body.String()
			for _, expected := range []string{
				"Total Events:    3",
				"Unique Machines: 3",
				"Remasters:       2",
				"Installations:   1",
				"### Distro",
				"### Arch",
				"### Desktop",
				"### Virt",
				"### Init",
				"### Country",
				"### Version",
				"headless/cli",
				"physical/none",
			} {
				if !strings.Contains(body, expected) {
					t.Errorf("expected text report to contain %q, got:\n%s", expected, body)
				}
			}
			if strings.Contains(body, "### Action") {
				t.Errorf("expected text report to not contain Action table, got:\n%s", body)
			}
			// Verify ranking: debian (2) appears before ubuntu (1)
			idxDebian := strings.Index(body, "debian")
			idxUbuntu := strings.Index(body, "ubuntu")
			if idxDebian == -1 || idxUbuntu == -1 || idxDebian > idxUbuntu {
				t.Errorf("expected debian (count 2) to be ranked before ubuntu (count 1)")
			}
		})
	}

	// 3. ?format=html and Accept: text/html
	for _, tc := range []struct {
		name   string
		url    string
		accept string
	}{
		{name: "query format=html", url: "/api/census?format=html", accept: ""},
		{name: "browser Accept text/html", url: "/api/census", accept: "text/html,application/xhtml+xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reqHTML := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if tc.accept != "" {
				reqHTML.Header.Set("Accept", tc.accept)
			}
			wHTML := httptest.NewRecorder()
			srv.ServeHTTP(wHTML, reqHTML)
			if wHTML.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", wHTML.Code)
			}
			ct := wHTML.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Errorf("expected text/html Content-Type, got %q", ct)
			}
			body := wHTML.Body.String()
			for _, expected := range []string{
				"<!DOCTYPE html>",
				"Penguins Eggs Worldwide Usage",
				"Since 22 October 2026",
				"https://penguins-eggs.net",
				"https://penguins-eggs.net/access/",
				"https://github.com/pieroproietti/penguins-eggs",
				"Home",
				"Site",
				"Repository",
				"Total Events",
				"Unique Machines",
				"Remasters",
				"Installations",
				"Clones",
				"Recent Events",
				"id=\"recent-events\"",
				"headless/cli",
				"physical/none",
			} {
				if !strings.Contains(body, expected) {
					t.Errorf("expected HTML dashboard to contain %q", expected)
				}
			}
			if strings.Contains(body, "ai-report") {
				t.Errorf("expected HTML dashboard to not contain ai-report section")
			}
			if strings.Contains(body, "Coexist") {
				t.Errorf("expected HTML dashboard to not display Coexist")
			}

			if strings.Contains(body, `id="action"`) {
				t.Errorf("expected HTML dashboard to not display Action table")
			}

			// Verify table order and anchor IDs: country, distro, desktop, virtualization, init-system, compression, arch, version
			headers := []string{
				`id="country"`,
				`id="distro"`,
				`id="desktop"`,
				`id="virtualization"`,
				`id="init-system"`,
				`id="compression"`,
				`id="arch"`,
				`id="version"`,
			}
			lastIdx := -1
			for _, h := range headers {
				idx := strings.Index(body, h)
				if idx == -1 {
					t.Errorf("expected section anchor %s in HTML dashboard", h)
				} else if idx <= lastIdx {
					t.Errorf("expected anchor %s (pos %d) to appear after previous anchor (pos %d)", h, idx, lastIdx)
				}
				lastIdx = idx
			}

			// Verify card anchors and table aliases
			for _, expectedAnchor := range []string{
				`id="overview"`,
				`id="total-events"`,
				`id="unique-machines"`,
				`id="remasters"`,
				`id="remaster"`,
				`id="installations"`,
				`id="sysinstall"`,
				`id="clones"`,
				`id="clone"`,
				`id="virt"`,
				`id="init"`,
				`id="eggs-version"`,
				`href="#country"`,
				`href="#distro"`,
				`href="#desktop"`,
			} {
				if !strings.Contains(body, expectedAnchor) {
					t.Errorf("expected HTML dashboard to contain anchor %q", expectedAnchor)
				}
			}
		})
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

func TestRecentEventsLimitAndOrder(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	defer srv.Close()

	// Post 5 events in sequence
	events := []string{
		`{"uuid":"u1","eggs_version":"10.0","action":"remaster","distro":{"id":"debian"},"system":{"desktop":"xfce"}}`,
		`{"uuid":"u2","eggs_version":"10.0","action":"sysinstall","distro":{"id":"ubuntu"},"system":{"desktop":"gnome"}}`,
		`{"uuid":"u3","eggs_version":"10.0","action":"remaster","distro":{"id":"arch"},"system":{"desktop":"kde"}}`,
		`{"uuid":"u4","eggs_version":"10.0","action":"sysinstall","distro":{"id":"fedora"},"system":{"desktop":"cinnamon"}}`,
		`{"uuid":"u5","eggs_version":"10.0","action":"remaster","distro":{"id":"manjaro"},"system":{"desktop":"mate"}}`,
	}
	for _, ev := range events {
		req := httptest.NewRequest(http.MethodPost, "/api/census", strings.NewReader(ev))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST failed: %d", w.Code)
		}
	}

	// Verify JSON format has exactly 3 recent events: u5, u4, u3
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
	if sum.TotalEvents != 5 {
		t.Fatalf("expected 5 total events, got %d", sum.TotalEvents)
	}
	if len(sum.RecentEvents) != 3 {
		t.Fatalf("expected 3 recent events, got %d", len(sum.RecentEvents))
	}

	// 1st recent event should be manjaro (from event 5)
	if sum.RecentEvents[0].Distro != "manjaro" || sum.RecentEvents[0].Desktop != "mate" ||
		!sum.RecentEvents[0].Remaster || sum.RecentEvents[0].Sysinstall {
		t.Errorf("expected 1st recent event to be manjaro/remaster, got %+v", sum.RecentEvents[0])
	}

	// 2nd recent event should be fedora (from event 4)
	if sum.RecentEvents[1].Distro != "fedora" || sum.RecentEvents[1].Desktop != "cinnamon" ||
		sum.RecentEvents[1].Remaster || !sum.RecentEvents[1].Sysinstall {
		t.Errorf("expected 2nd recent event to be fedora/sysinstall, got %+v", sum.RecentEvents[1])
	}

	// 3rd recent event should be arch (from event 3)
	if sum.RecentEvents[2].Distro != "arch" || sum.RecentEvents[2].Desktop != "kde" ||
		!sum.RecentEvents[2].Remaster || sum.RecentEvents[2].Sysinstall {
		t.Errorf("expected 3rd recent event to be arch/remaster, got %+v", sum.RecentEvents[2])
	}

	// Verify HTML contains the recent events table with rows
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/census?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	htmlBody := wHTML.Body.String()
	for _, expected := range []string{
		"Recent Events",
		"manjaro",
		"fedora",
		"arch",
		"mate",
		"cinnamon",
		"kde",
		"&#10003;", // checkmark for remaster/sysinstall
	} {
		if !strings.Contains(htmlBody, expected) {
			t.Errorf("expected HTML to contain %q", expected)
		}
	}
}

func TestTopInstallationsRankingAndDeduplication(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")

	srv := NewCensusServer(dataFile, "")
	defer srv.Close()

	// Post events:
	// - UUID u1 does sysinstall on openkylin in IT (twice, should be counted ONCE)
	// - UUID u2 does sysinstall on openkylin in IT (once)
	// -> openkylin IT should have sum_sysinstall = 2
	// - UUID u3 does sysinstall on debian in IT
	// - UUID u4 does remaster on debian in IT (action=remaster, should NOT count towards sysinstall)
	// - UUID u5 does sysinstall on ubuntu in US
	// - UUID u6 does sysinstall on arch in DE
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
		top1.Arch != "amd64" || top1.EggsVersion != "v26.10.21" || top1.SumSysinstall != 2 {
		t.Errorf("unexpected top 1: %+v", top1)
	}

	// 2. Verify plain text / markdown output contains the table with required columns
	reqTxt := httptest.NewRequest(http.MethodGet, "/api/census?format=txt", nil)
	wTxt := httptest.NewRecorder()
	srv.ServeHTTP(wTxt, reqTxt)
	txtBody := wTxt.Body.String()

	for _, col := range []string{
		"### Top 10 Installations (sysinstall)",
		"Country",
		"Distro",
		"Desktop",
		"Arch",
		"Eggs Version",
		"sum_sysinstall",
		"openkylin",
		"debian",
		"ubuntu",
		"arch",
	} {
		if !strings.Contains(txtBody, col) {
			t.Errorf("expected text report to contain %q, got:\n%s", col, txtBody)
		}
	}

	// Verify Top 10 Installations appears before Recent Events, with Recent Events at the very end
	idxTop := strings.Index(txtBody, "### Top 10 Installations (sysinstall)")
	idxRecent := strings.Index(txtBody, "### Recent Events")
	if idxRecent == -1 || idxTop == -1 || idxRecent <= idxTop {
		t.Errorf("expected Top 10 Installations to appear before Recent Events (Recent Events in finale) in text summary")
	}

	// 3. Verify HTML output
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/census?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	htmlBody := wHTML.Body.String()

	idxHTMLTop := strings.Index(htmlBody, `id="top-installations"`)
	idxHTMLRecent := strings.Index(htmlBody, `id="recent-events"`)
	if idxHTMLTop == -1 || idxHTMLRecent == -1 || idxHTMLRecent <= idxHTMLTop {
		t.Errorf("expected top-installations panel to appear before recent-events panel in HTML")
	}

	if !strings.Contains(htmlBody, `id="top-installations"`) {
		t.Errorf("expected HTML to contain id=\"top-installations\"")
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

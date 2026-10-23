package main

import (
	"bytes"
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

func TestCensusFormatsAndFallbacks(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewCensusServer(filepath.Join(tmpDir, "census.jsonl"), "")
	defer srv.Close()

	// Post event with empty desktop and empty virt to verify fallback defaults
	events := []string{
		`{"uuid":"u1","eggs_version":"10.1.0","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"","display_server":"","virt":"","init":"systemd"},"options":{"clone":true,"compression":"zstd","coexist":false}}`,
		`{"uuid":"u2","eggs_version":"10.1.0","action":"remaster","distro":{"id":"debian"},"system":{"arch":"amd64","desktop":"kde","display_server":"x11","virt":"kvm","init":"systemd"},"options":{"clone":false,"compression":"xz","coexist":true}}`,
		`{"uuid":"u3","eggs_version":"10.0.9","action":"sysinstall","distro":{"id":"ubuntu"},"system":{"arch":"arm64","desktop":"kde","display_server":"wayland","virt":"","init":"openrc"},"options":{"clone":false,"compression":"zstd","coexist":false}}`,
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
	if sum.ByDisplayServer["headless/cli"] != 1 || sum.ByDisplayServer["x11"] != 1 || sum.ByDisplayServer["wayland"] != 1 {
		t.Errorf("expected headless/cli=1, x11=1, wayland=1, got %+v", sum.ByDisplayServer)
	}
	if sum.ByVirt["bare-metal"] != 2 || sum.ByVirt["kvm"] != 1 {
		t.Errorf("expected bare-metal=2 and kvm=1, got %+v", sum.ByVirt)
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
				"Total Events:        3",
				"Unique Machines:     3",
				"Remasters/Clones:    2/1",
				"Sysinstalls/Clones:  1/0",
				"### Country",
				"### Distro",
				"### Desktop",
				"### Display Server",
				"### Virt",
				"### Init",
				"### Arch",
				"### Version",
				"headless/cli",
				"bare-metal",
			} {
				if !strings.Contains(body, expected) {
					t.Errorf("expected text report to contain %q, got:\n%s", expected, body)
				}
			}
			if strings.Contains(body, "### Action") {
				t.Errorf("expected text report to not contain Action table, got:\n%s", body)
			}
			if strings.Contains(body, "Compression") {
				t.Errorf("expected text report to not contain Compression table, got:\n%s", body)
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
				"Sysinstalls",
				"Remasters/Clones",
				"Sysinstalls/Clones",
				"Recent Events",
				"id=\"recent-events\"",
				"headless/cli",
				"bare-metal",
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
			if strings.Contains(body, "Compression") {
				t.Errorf("expected HTML dashboard to not display Compression")
			}

			if strings.Contains(body, `id="action"`) {
				t.Errorf("expected HTML dashboard to not display Action table")
			}

			// Verify table order and anchor IDs: country, distro, desktop, display-server, virtualization, init-system, arch, version
			headers := []string{
				`id="country"`,
				`id="distro"`,
				`id="desktop"`,
				`id="display-server"`,
				`id="virtualization"`,
				`id="init-system"`,
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
				`id="display_server"`,
				`id="virt"`,
				`id="init"`,
				`id="eggs-version"`,
				`href="#country"`,
				`href="#distro"`,
				`href="#desktop"`,
				`href="#display-server"`,
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

func TestAnalyzeAccessLogs(t *testing.T) {
	tmpDir := t.TempDir()

	// Sample Apache combined access log
	sampleLog := strings.Join([]string{
		// 1. Valid Debian client (IP 198.51.100.10) checking InRelease and Packages
		`198.51.100.10 - - [18/Oct/2026:06:25:31 +0200] "GET /repos/deb/dists/bookworm/InRelease HTTP/1.1" 200 4567 "-" "Debian APT-HTTP/1.3 (2.6.1)"`,
		`198.51.100.10 - - [18/Oct/2026:06:25:32 +0200] "GET /repos/deb/dists/bookworm/main/binary-amd64/Packages.xz HTTP/1.1" 304 0 "-" "Debian APT-HTTP/1.3 (2.6.1)"`,
		// Repeat request next day from same IP: must deduplicate to 1 client
		`198.51.100.10 - - [19/Oct/2026:08:12:00 +0200] "GET /repos/deb/dists/bookworm/InRelease HTTP/1.1" 304 0 "-" "Debian APT-HTTP/1.3 (2.6.1)"`,

		// 2. Valid Arch client (IP 198.51.100.20) querying pacman db
		`198.51.100.20 - - [18/Oct/2026:07:00:00 +0200] "GET /repos/arch/os/x86_64/eggs.db HTTP/1.1" 200 12340 "-" "pacman/6.0.2 (Linux x86_64) libalpm/13.0.2"`,

		// 3. Valid RPM client (IP 198.51.100.30) querying dnf repomd.xml
		`198.51.100.30 - - [18/Oct/2026:08:00:00 +0200] "GET /repos/rpm/repodata/repomd.xml HTTP/1.1" 200 3456 "-" "dnf/4.14.0"`,

		// 4. Valid Alpine client (IP 198.51.100.40) querying APKINDEX
		`198.51.100.40 - - [18/Oct/2026:09:00:00 +0200] "GET /repos/alpine/v3.20/main/x86_64/APKINDEX.tar.gz HTTP/1.1" 200 890 "-" "apk-tools/2.14.4"`,

		// 5. Crawler / Bot accessing an index (must be ignored)
		`198.51.100.99 - - [18/Oct/2026:10:00:00 +0200] "GET /repos/deb/dists/bookworm/InRelease HTTP/1.1" 200 4567 "-" "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"`,

		// 6. Generic HTML page view (not repo)
		`198.51.100.88 - - [18/Oct/2026:11:00:00 +0200] "GET /blog/ HTTP/1.1" 200 12000 "-" "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"`,

		// 7. 404 error (failed request)
		`198.51.100.77 - - [18/Oct/2026:12:00:00 +0200] "GET /repos/deb/dists/nonexistent/InRelease HTTP/1.1" 404 123 "-" "Debian APT-HTTP/1.3"`,
	}, "\n") + "\n"

	logFile := filepath.Join(tmpDir, "access.log")
	if err := os.WriteFile(logFile, []byte(sampleLog), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	summary := processAccessLogStream(f, nil)

	// Verify deduplicated IP count: exactly 4 clients (10, 20, 30, 40)
	if summary.UniqueIPs != 4 {
		t.Errorf("expected 4 unique repo client IPs, got %d", summary.UniqueIPs)
	}
	// Verify families
	if summary.ByFamily["Debian/APT"] != 1 {
		t.Errorf("expected 1 Debian/APT client, got %d", summary.ByFamily["Debian/APT"])
	}
	if summary.ByFamily["Arch/ALPM"] != 1 {
		t.Errorf("expected 1 Arch/ALPM client, got %d", summary.ByFamily["Arch/ALPM"])
	}
	if summary.ByFamily["RPM/DNF"] != 1 {
		t.Errorf("expected 1 RPM/DNF client, got %d", summary.ByFamily["RPM/DNF"])
	}
	if summary.ByFamily["Alpine/APK"] != 1 {
		t.Errorf("expected 1 Alpine/APK client, got %d", summary.ByFamily["Alpine/APK"])
	}

	// Verify bots ignored
	if summary.IgnoredBots != 1 {
		t.Errorf("expected 1 ignored bot, got %d", summary.IgnoredBots)
	}

	// Verify report generation
	census := &CensusSummary{
		UniqueMachines: 3,
		ByAction: map[string]int{
			"remaster":   5,
			"sysinstall": 2,
		},
	}
	rep := renderRepoAnalysisReport(summary, census)
	for _, expected := range []string{
		"Stima Macchine Reali Penguins-Eggs",
		"Client Repository Osservati",
		"Macchine Censite da Eggs",
		"Operazioni Riuscite",
		"Debian/APT",
		"Arch/ALPM",
		"RPM/DNF",
		"Alpine/APK",
	} {
		if !strings.Contains(rep, expected) {
			t.Errorf("expected report to contain %q", expected)
		}
	}

	// Test saving and automatic append to census dashboard
	repoJSONPath := filepath.Join(tmpDir, "repo-clients.json")
	if err := saveRepoAnalysisSummary(repoJSONPath, summary); err != nil {
		t.Fatalf("saveRepoAnalysisSummary failed: %v", err)
	}

	censusDataFile := filepath.Join(tmpDir, "census.jsonl")
	_ = os.WriteFile(censusDataFile, []byte(`{"uuid":"m1","action":"remaster"}`+"\n"), 0644)

	sumLoaded, err := loadCensusSummary(censusDataFile)
	if err != nil {
		t.Fatalf("loadCensusSummary failed: %v", err)
	}
	if sumLoaded.RepoClients == nil {
		t.Fatalf("expected RepoClients to be loaded from repo-clients.json")
	}
	if sumLoaded.RepoClients.UniqueIPs != 4 {
		t.Errorf("expected 4 unique IPs in loaded RepoClients, got %d", sumLoaded.RepoClients.UniqueIPs)
	}

	// Verify HTML dashboard includes the appended unique-ips panel and button at the end
	htmlRep := renderHTMLSummary(sumLoaded)
	for _, expected := range []string{
		`id="unique-ips"`,
		`id="repo-clients"`,
		`href="#unique-ips"`,
		"Unique IP last 15 days",
		"Unique IPs (15 days)",
		"Package Managers",
		"Repo Clients by Country",
		"Debian/APT",
	} {
		if !strings.Contains(htmlRep, expected) {
			t.Errorf("expected HTML dashboard to contain %q", expected)
		}
	}

	// Verify order: Unique IP last 15 days is placed after Recent Events and Top Sysinstalls
	topIdx := strings.Index(htmlRep, `id="top-sysinstalls"`)
	recentIdx := strings.Index(htmlRep, `id="recent-events"`)
	uniqueIPIdx := strings.Index(htmlRep, `id="unique-ips"`)
	if topIdx == -1 || recentIdx == -1 || uniqueIPIdx == -1 {
		t.Fatalf("expected top-sysinstalls, recent-events, and unique-ips panels in HTML")
	}
	if !(recentIdx > topIdx && uniqueIPIdx > recentIdx) {
		t.Errorf("expected order: top-sysinstalls < recent-events < unique-ips, got %d, %d, %d", topIdx, recentIdx, uniqueIPIdx)
	}

	// Verify Text summary includes the appended repo-clients section at the end
	txtRep := renderTextSummary(sumLoaded)
	for _, expected := range []string{
		"### Unique IP last 15 days",
		"- Unique IPs (15 days):    4",
		"Package Managers",
		"Repo Clients by Country",
		"Debian/APT",
	} {
		if !strings.Contains(txtRep, expected) {
			t.Errorf("expected Text summary to contain %q", expected)
		}
	}
	txtTopIdx := strings.Index(txtRep, "### Top 10 Sysinstalls")
	txtRecentIdx := strings.Index(txtRep, "### Recent Events")
	txtUniqueIPIdx := strings.Index(txtRep, "### Unique IP last 15 days")
	if !(txtRecentIdx > txtTopIdx && txtUniqueIPIdx > txtRecentIdx) {
		t.Errorf("expected text order: Top Sysinstalls < Recent Events < Unique IP last 15 days, got %d, %d, %d", txtTopIdx, txtRecentIdx, txtUniqueIPIdx)
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

func TestCommunityXSSAndURLSanitization(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "census.jsonl")
	commDir := filepath.Join(tmpDir, "community")

	srv := NewCensusServer(dataFile, "")
	srv.SetCommunityDir(commDir)

	uuid := "55555555-5555-5555-5555-555555555555"

	// Malicious payload with potential XSS in text fields and javascript: URLs
	payload := `{
		"uuid": "` + uuid + `",
		"nickname": "<script>alert('xss-nick')</script>",
		"location": "<img src=x onerror=alert('xss-loc')>",
		"bio": "<b onmouseover=\"alert('xss-bio')\">Malicious Bio</b>",
		"contact": "javascript:alert('xss-contact')",
		"source_url": "javascript:alert('xss-source')",
		"socials": {
			"github": "javascript:alert('xss-gh')",
			"custom": "data:text/html,<script>alert(1)</script>"
		},
		"enabled": true
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/community", strings.NewReader(payload))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to save profile: %s", w.Body.String())
	}

	// Request HTML
	reqHTML := httptest.NewRequest(http.MethodGet, "/api/community?format=html", nil)
	wHTML := httptest.NewRecorder()
	srv.ServeHTTP(wHTML, reqHTML)
	if wHTML.Code != http.StatusOK {
		t.Fatalf("expected 200 on HTML, got %d", wHTML.Code)
	}

	htmlBody := wHTML.Body.String()

	// Verify unescaped scripts / tags are NOT present
	if strings.Contains(htmlBody, "<script>alert('xss-nick')</script>") {
		t.Errorf("raw unescaped script tag found in HTML")
	}
	if strings.Contains(htmlBody, "<img src=x") {
		t.Errorf("raw unescaped img tag found in HTML")
	}
	if strings.Contains(htmlBody, "<b onmouseover=") {
		t.Errorf("raw unescaped b tag with onmouseover found in HTML")
	}

	// Verify properly escaped HTML entities ARE present
	if !strings.Contains(htmlBody, "&lt;script&gt;alert(&#39;xss-nick&#39;)&lt;/script&gt;") && !strings.Contains(htmlBody, "&lt;script&gt;alert('xss-nick')&lt;/script&gt;") {
		t.Errorf("escaped script was not found as expected")
	}

	// Verify javascript: or data: was NEVER put in href attribute
	if strings.Contains(htmlBody, `href="javascript:`) || strings.Contains(htmlBody, `href='javascript:`) {
		t.Errorf("unsafe javascript: URI found in href attribute!")
	}
	if strings.Contains(htmlBody, `href="data:`) || strings.Contains(htmlBody, `href='data:`) {
		t.Errorf("unsafe data: URI found in href attribute!")
	}
}


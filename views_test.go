package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
			// Verify ranking in Distro table: debian (2) appears before ubuntu (1)
			idxDistroSection := strings.Index(body, "### Distro")
			if idxDistroSection == -1 {
				t.Fatalf("expected ### Distro section in text report")
			}
			distroSection := body[idxDistroSection:]
			idxDebian := strings.Index(distroSection, "debian")
			idxUbuntu := strings.Index(distroSection, "ubuntu")
			if idxDebian == -1 || idxUbuntu == -1 || idxDebian > idxUbuntu {
				t.Errorf("expected debian (count 2) to be ranked before ubuntu (count 1) in Distro table")
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
				"Home",
				"🐧 Artisans",
				"Plain Text / Markdown",
				"Raw JSON",
				"Total Events",
				"Unique Machines",
				"Remasters",
				"Sysinstalls",
				"Remasters/Clones",
				"Sysinstalls/Clones",
				"Recent Sysinstalls",
				"id=\"recent-sysinstalls\"",
				"Recent Remasters",
				"id=\"recent-remasters\"",
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

func TestCountryToFlag(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"IT", "🇮🇹"},
		{"it", "🇮🇹"},
		{"US", "🇺🇸"},
		{"DE", "🇩🇪"},
		{"XX", ""},
		{"", ""},
		{"INVALID", ""},
	}
	for _, tc := range tests {
		got := countryToFlag(tc.code)
		if got != tc.want {
			t.Errorf("countryToFlag(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestFormatDistroBadge(t *testing.T) {
	d := DistroInfo{ID: "debian", Codename: "trixie", Release: "13"}
	s := SystemInfo{Arch: "amd64", Desktop: "xfce"}
	badge := formatDistroBadge(d, s)
	expected := "debian trixie (13) • amd64 • xfce"
	if badge != expected {
		t.Errorf("expected badge %q, got %q", expected, badge)
	}

	// Empty defaults
	badgeEmpty := formatDistroBadge(DistroInfo{}, SystemInfo{})
	if !strings.Contains(badgeEmpty, "linux") || !strings.Contains(badgeEmpty, "headless/cli") {
		t.Errorf("expected fallback badge, got %q", badgeEmpty)
	}
}

func TestBuildSocialLinks(t *testing.T) {
	socials := map[string]string{
		"github":   "johndoe",
		"mastodon": "@john@fosstodon.org",
		"telegram": "@johndoe",
		"unsafe":   "javascript:alert(1)",
	}
	links := buildSocialLinks(socials, "https://example.org/contact")
	foundGH := false
	foundTG := false
	foundContact := false
	for _, l := range links {
		if l.Platform == "github" && l.URL == "https://github.com/johndoe" {
			foundGH = true
		}
		if l.Platform == "telegram" && l.URL == "https://t.me/johndoe" {
			foundTG = true
		}
		if l.Platform == "contact" && l.URL == "https://example.org/contact" {
			foundContact = true
		}
		if strings.Contains(l.URL, "javascript:") {
			t.Errorf("unsafe URL included: %+v", l)
		}
	}
	if !foundGH || !foundTG || !foundContact {
		t.Errorf("missing expected social links: %+v", links)
	}
}

func TestPluralRemasters(t *testing.T) {
	if pluralRemasters(1) != "build" {
		t.Errorf("expected build for count 1, got %s", pluralRemasters(1))
	}
	if pluralRemasters(0) != "builds" || pluralRemasters(2) != "builds" {
		t.Errorf("expected builds for count 0 or 2")
	}
}

func TestSortedRows(t *testing.T) {
	m := map[string]int{
		"apple":  10,
		"banana": 30,
		"cherry": 20,
	}
	rows := sortedRows(m, 60)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Key != "banana" || rows[0].Count != 30 {
		t.Errorf("expected first row banana/30, got %+v", rows[0])
	}
	if rows[1].Key != "cherry" || rows[1].Count != 20 {
		t.Errorf("expected second row cherry/20, got %+v", rows[1])
	}
	if rows[2].Key != "apple" || rows[2].Count != 10 {
		t.Errorf("expected third row apple/10, got %+v", rows[2])
	}
}

func TestSortedVersionRows(t *testing.T) {
	m := map[string]int{
		"10.0.9":   50,
		"10.0.50":  100,
		"26.10.22": 5,
		"26.10.9":  20,
		"10.1.0":   10,
	}
	rows := sortedVersionRows(m, 185)
	expectedOrder := []string{"26.10.22", "26.10.9", "10.1.0", "10.0.50", "10.0.9"}
	if len(rows) != len(expectedOrder) {
		t.Fatalf("expected %d rows, got %d", len(expectedOrder), len(rows))
	}
	for i, want := range expectedOrder {
		if rows[i].Key != want {
			t.Errorf("row[%d].Key = %q, want %q", i, rows[i].Key, want)
		}
	}
}

func TestRenderViewsEmptySummary(t *testing.T) {
	s := CensusSummary{
		TotalEvents:    0,
		UniqueMachines: 0,
		ByAction:       map[string]int{},
		ByDistro:       map[string]int{},
	}
	txt := renderTextSummary(s)
	if !strings.Contains(txt, "# Penguins Eggs Census Report") {
		t.Errorf("expected header in empty txt summary")
	}

	html := renderHTMLSummary(s)
	if !strings.Contains(html, "<!DOCTYPE html>") || !strings.Contains(html, "Penguins Eggs Worldwide Usage") {
		t.Errorf("expected html header in empty html summary")
	}

	commTxt := renderCommunityText(nil)
	if !strings.Contains(commTxt, "Total Artisans: 0") {
		t.Errorf("expected 0 artisans in community text")
	}

	commHTML := renderCommunityHTML(nil)
	if !strings.Contains(commHTML, "No community artisans registered yet") {
		t.Errorf("expected empty message in community html")
	}
}

func TestRenderArtisanStatsAndCatalog(t *testing.T) {
	artisans := []ArtisanProfile{
		{
			ID:            "distro-artisan",
			UUID:          "distro-artisan",
			Nickname:      "masterbuilder",
			Location:      "Milano, IT",
			Country:       "IT",
			MachinesCount: 3,
			RemasterCount: 5,
			InstallsCount: 12,
			LiveDistros: []ArtisanLiveSummary{
				{
					DistroName: "debian",
					Version:    "13",
					Desktop:    "xfce",
					Arch:       "amd64",
					Count:      3,
					LastSeen:   "2026-10-24T10:00:00Z",
				},
				{
					DistroName: "ubuntu",
					Version:    "24.04",
					Desktop:    "gnome",
					Arch:       "arm64",
					Count:      2,
					LastSeen:   "2026-10-24T11:00:00Z",
				},
			},
			LatestRemaster: &ArtisanRemasterInfo{
				Distro:         DistroInfo{ID: "debian", Release: "13"},
				System:         SystemInfo{Arch: "amd64", Desktop: "xfce"},
				Country:        "IT",
				LastRemasterAt: "2026-10-24T10:00:00Z",
				Count:          5,
			},
		},
	}

	// 1. Test HTML rendering
	html := renderCommunityHTML(artisans)
	for _, expected := range []string{
		"3 machines",
		"5 builds",
		"12 installs",
		"Live Distros Catalog:",
		"debian 13 • amd64 • xfce",
		"ubuntu 24.04 • arm64 • gnome",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("expected community HTML to contain %q", expected)
		}
	}

	// 2. Test Text rendering
	txt := renderCommunityText(artisans)
	for _, expected := range []string{
		"Machines",
		"Remasters",
		"Installs",
		"3",
		"5",
		"12",
		"Live Distro Catalogs",
		"debian 13 • amd64 • xfce: 3 builds",
		"ubuntu 24.04 • arm64 • gnome: 2 builds",
	} {
		if !strings.Contains(txt, expected) {
			t.Errorf("expected community text to contain %q", expected)
		}
	}

	// 3. Test HTML Summary rendering
	sum := CensusSummary{
		TotalEvents:    10,
		UniqueMachines: 5,
		Artisans:       artisans,
		TopInstallations: []TopInstallation{
			{
				Country:         "IT",
				Distro:          "debian",
				Desktop:         "xfce",
				DisplayServer:   "x11",
				Arch:            "amd64",
				EggsVersion:     "10.1.0",
				SumSysinstall:   12,
				ArtisanNickname: "masterbuilder",
				ArtisanURL:      "https://example.com/builder",
			},
		},
		ByAction: map[string]int{"remaster": 5, "sysinstall": 12},
		ByDistro: map[string]int{"debian": 10},
	}
	sumHTML := renderHTMLSummary(sum)
	if !strings.Contains(sumHTML, "@masterbuilder") {
		t.Errorf("expected HTML summary top installations to contain artisan nickname @masterbuilder")
	}
	if strings.Contains(sumHTML, `id="community-artisans"`) {
		t.Errorf("expected HTML summary to not contain community-artisans table")
	}
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now().UTC()
	if got := formatTimeAgo(""); got != "-" {
		t.Errorf("expected '-' for empty string, got %q", got)
	}
	if got := formatTimeAgo(now.Format(time.RFC3339)); got != "just now" {
		t.Errorf("expected 'just now', got %q", got)
	}
	if got := formatTimeAgo(now.Add(-30 * time.Second).Format("2006-01-02 15:04:05")); !strings.HasSuffix(got, "s ago") {
		t.Errorf("expected seconds ago, got %q", got)
	}
	if got := formatTimeAgo(now.Add(-5 * time.Minute).Format("2006-01-02 15:04:05")); got != "5m ago" {
		t.Errorf("expected '5m ago', got %q", got)
	}
	if got := formatTimeAgo(now.Add(-3 * time.Hour).Format("2006-01-02 15:04:05")); got != "3h ago" {
		t.Errorf("expected '3h ago', got %q", got)
	}
	if got := formatTimeAgo(now.Add(-48 * time.Hour).Format("2006-01-02 15:04:05")); got != "2d ago" {
		t.Errorf("expected '2d ago', got %q", got)
	}
}

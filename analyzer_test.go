package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

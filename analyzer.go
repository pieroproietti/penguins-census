package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

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


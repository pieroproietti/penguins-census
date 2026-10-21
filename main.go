package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	Arch    string `json:"arch"`
	Desktop string `json:"desktop"`
	Virt    string `json:"virt"`
	Init    string `json:"init"`
}

type OptionsInfo struct {
	Clone       bool   `json:"clone"`
	Compression string `json:"compression"`
	Coexist     bool   `json:"coexist"`
}

type CensusRecord struct {
	Timestamp   string      `json:"timestamp"`
	Country     string      `json:"country"`
	UUID        string      `json:"uuid"`
	EggsVersion string      `json:"eggs_version"`
	Action      string      `json:"action"`
	Distro      DistroInfo  `json:"distro"`
	System      SystemInfo  `json:"system"`
	Options     OptionsInfo `json:"options"`
}

type RecentEvent struct {
	Country    string `json:"country"`
	Distro     string `json:"distro"`
	Desktop    string `json:"desktop"`
	Remaster   bool   `json:"remaster"`
	Sysinstall bool   `json:"sysinstall"`
}

type TopInstallation struct {
	Country       string `json:"country"`
	Distro        string `json:"distro"`
	Desktop       string `json:"desktop"`
	Arch          string `json:"arch"`
	EggsVersion   string `json:"eggs_version"`
	SumSysinstall int    `json:"sum_sysinstall"`
}

type CensusSummary struct {
	TotalEvents      int               `json:"total_events"`
	UniqueMachines   int               `json:"unique_machines"`
	TotalCountries   int               `json:"total_countries"`
	TotalDistros     int               `json:"total_distros"`
	CountryCount     int               `json:"country,omitempty"`
	DistroCount      int               `json:"distro,omitempty"`
	CloneCount       int               `json:"clone_count"`
	CoexistCount     int               `json:"coexist_count"`
	RecentEvents     []RecentEvent     `json:"recent_events"`
	TopInstallations []TopInstallation `json:"top_installations"`
	ByAction         map[string]int    `json:"by_action"`
	ByDistro         map[string]int    `json:"by_distro"`
	ByArch           map[string]int    `json:"by_arch"`
	ByDesktop        map[string]int    `json:"by_desktop"`
	ByVirt           map[string]int    `json:"by_virt"`
	ByInit           map[string]int    `json:"by_init"`
	ByCompression    map[string]int    `json:"by_compression"`
	ByCountry        map[string]int    `json:"by_country"`
	ByVersion        map[string]int    `json:"by_version"`
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
	mu       sync.Mutex
	dataPath string
	mmdbPath string
	mmdb     *maxminddb.Reader
}

func NewCensusServer(dataPath, mmdbPath string) *CensusServer {
	s := &CensusServer{
		dataPath: dataPath,
		mmdbPath: mmdbPath,
	}
	if mmdbPath != "" {
		if reader, err := maxminddb.Open(mmdbPath); err == nil {
			s.mmdb = reader
		}
	}
	return s
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
	if r.URL.Path != "/api/census" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodGet:
		s.handleGet(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
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

func loadCensusSummary(dataPath string) (CensusSummary, error) {
	summary := CensusSummary{
		RecentEvents:     make([]RecentEvent, 0),
		TopInstallations: make([]TopInstallation, 0),
		ByAction:         make(map[string]int),
		ByDistro:         make(map[string]int),
		ByArch:           make(map[string]int),
		ByDesktop:        make(map[string]int),
		ByVirt:           make(map[string]int),
		ByInit:           make(map[string]int),
		ByCompression:    make(map[string]int),
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
		if len(lastRecords) > 3 {
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

		virt := strings.TrimSpace(rec.System.Virt)
		if virt == "" {
			virt = "physical/none"
		}
		summary.ByVirt[virt]++

		if rec.System.Init != "" {
			summary.ByInit[rec.System.Init]++
		}
		if rec.Options.Compression != "" {
			summary.ByCompression[rec.Options.Compression]++
		}
		if rec.Options.Clone {
			summary.CloneCount++
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
		summary.RecentEvents = append(summary.RecentEvents, RecentEvent{
			Country:    country,
			Distro:     distro,
			Desktop:    desktop,
			Remaster:   r.Action == "remaster",
			Sysinstall: r.Action == "sysinstall",
		})
	}

	summary.UniqueMachines = len(uniqueUUIDs)
	summary.TotalCountries = len(summary.ByCountry)
	summary.TotalDistros = len(summary.ByDistro)
	summary.CountryCount = summary.TotalCountries
	summary.DistroCount = summary.TotalDistros

	// Aggregate unique sysinstall installations
	type groupKey struct {
		country     string
		distro      string
		desktop     string
		arch        string
		eggsVersion string
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
		arch := strings.TrimSpace(r.System.Arch)
		if arch == "" {
			arch = "unknown"
		}
		ver := strings.TrimSpace(r.EggsVersion)
		if ver == "" {
			ver = "unknown"
		}
		key := groupKey{
			country:     country,
			distro:      distro,
			desktop:     desktop,
			arch:        arch,
			eggsVersion: ver,
		}
		groupCounts[key]++
	}

	topList := make([]TopInstallation, 0, len(groupCounts))
	for k, count := range groupCounts {
		topList = append(topList, TopInstallation{
			Country:       k.country,
			Distro:        k.distro,
			Desktop:       k.desktop,
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
		if topList[i].Arch != topList[j].Arch {
			return topList[i].Arch < topList[j].Arch
		}
		return topList[i].EggsVersion < topList[j].EggsVersion
	})

	if len(topList) > 10 {
		topList = topList[:10]
	}
	summary.TopInstallations = topList

	return summary, nil
}

func (s *CensusServer) handleGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	summary, err := loadCensusSummary(s.dataPath)
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
	fmt.Fprintf(&buf, "- Total Events:    %d\n", s.TotalEvents)
	fmt.Fprintf(&buf, "- Unique Machines: %d\n", s.UniqueMachines)
	fmt.Fprintf(&buf, "- Remasters:       %d\n", s.ByAction["remaster"])
	fmt.Fprintf(&buf, "- Installations:   %d\n", s.ByAction["sysinstall"])
	fmt.Fprintf(&buf, "- Clone Count:     %d\n\n", s.CloneCount)

	writeMarkdownTable(&buf, "Country", "Country", s.ByCountry, s.TotalEvents)
	writeMarkdownTable(&buf, "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeMarkdownTable(&buf, "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeMarkdownTable(&buf, "Virt", "Virt", s.ByVirt, s.TotalEvents)
	writeMarkdownTable(&buf, "Init", "Init", s.ByInit, s.TotalEvents)
	writeMarkdownTable(&buf, "Compression", "Compression", s.ByCompression, s.TotalEvents)
	writeMarkdownTable(&buf, "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeMarkdownTable(&buf, "Version", "Version", s.ByVersion, s.TotalEvents)

	writeMarkdownTopInstallations(&buf, s.TopInstallations)
	writeMarkdownRecentEvents(&buf, s.RecentEvents)

	return buf.String()
}

func writeMarkdownTopInstallations(buf *bytes.Buffer, list []TopInstallation) {
	fmt.Fprintf(buf, "### Top 10 Installations (sysinstall)\n\n")

	countryW := len("Country")
	distroW := len("Distro")
	desktopW := len("Desktop")
	archW := len("Arch")
	verW := len("Eggs Version")
	sumW := len("sum_sysinstall")

	if len(list) == 0 {
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %*s |\n",
			countryW, "Country",
			distroW, "Distro",
			desktopW, "Desktop",
			archW, "Arch",
			verW, "Eggs Version",
			sumW, "sum_sysinstall")
		fmt.Fprintf(buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s:|\n",
			strings.Repeat("-", countryW),
			strings.Repeat("-", distroW),
			strings.Repeat("-", desktopW),
			strings.Repeat("-", archW),
			strings.Repeat("-", verW),
			strings.Repeat("-", sumW-1))
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %*d |\n\n",
			countryW, "(no data)",
			distroW, "-",
			desktopW, "-",
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
		if len(item.Arch) > archW {
			archW = len(item.Arch)
		}
		if len(item.EggsVersion) > verW {
			verW = len(item.EggsVersion)
		}
	}

	fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %*s |\n",
		countryW, "Country",
		distroW, "Distro",
		desktopW, "Desktop",
		archW, "Arch",
		verW, "Eggs Version",
		sumW, "sum_sysinstall")
	fmt.Fprintf(buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s:|\n",
		strings.Repeat("-", countryW),
		strings.Repeat("-", distroW),
		strings.Repeat("-", desktopW),
		strings.Repeat("-", archW),
		strings.Repeat("-", verW),
		strings.Repeat("-", sumW-1))

	for _, item := range list {
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %*d |\n",
			countryW, item.Country,
			distroW, item.Distro,
			desktopW, item.Desktop,
			archW, item.Arch,
			verW, item.EggsVersion,
			sumW, item.SumSysinstall)
	}
	buf.WriteString("\n")
}

func writeMarkdownRecentEvents(buf *bytes.Buffer, events []RecentEvent) {
	fmt.Fprintf(buf, "### Recent Events\n\n")
	fmt.Fprintf(buf, "| Country | Distro | Desktop | Remaster | Sysinstall |\n")
	fmt.Fprintf(buf, "|---------|--------|---------|:--------:|:----------:|\n")
	if len(events) == 0 {
		fmt.Fprintf(buf, "| (no data) | - | - | - | - |\n\n")
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
		fmt.Fprintf(buf, "| %-7s | %-6s | %-7s | %-8s | %-10s |\n", ev.Country, ev.Distro, ev.Desktop, rem, sys)
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
          <th>Country</th>
          <th>Distro</th>
          <th>Desktop</th>
          <th class="center">Remaster</th>
          <th class="center">Sysinstall</th>
        </tr>
      </thead>
      <tbody>
`)
	if len(events) == 0 {
		buf.WriteString(`        <tr><td colspan="5" class="empty">No events recorded yet</td></tr>
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
			fmt.Fprintf(buf, `        <tr><td>%s</td><td>%s</td><td>%s</td><td class="center">%s</td><td class="center">%s</td></tr>
`, html.EscapeString(e.Country), html.EscapeString(e.Distro), html.EscapeString(e.Desktop), rem, sys)
		}
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
</style>
</head>
<body>
<div class="container">
  <header>
    <div>
      <h1>Penguins Eggs Worldwide Usage</h1>
      <div class="subtitle">Since 19 October 2026 &bull; Privacy-respecting aggregated telemetry (no IP addresses stored)</div>
    </div>
    <div class="links">
      <a class="btn" href="https://penguins-eggs.net" target="_blank" rel="noopener">Home</a>
      <a class="btn" href="https://penguins-eggs.net/access/" target="_blank" rel="noopener">Site</a>
      <a class="btn" href="https://github.com/pieroproietti/penguins-eggs" target="_blank" rel="noopener">Repository</a>
      <a class="btn" href="?format=txt">Plain Text / Markdown</a>
      <a class="btn" href="?format=json">Raw JSON</a>
    </div>
  </header>
`)

	fmt.Fprintf(&buf, `  <div class="cards" id="overview">
    <div class="card" id="total-events"><div class="card-label">Total Events</div><div class="card-value">%d</div></div>
    <div class="card" id="unique-machines"><div class="card-label">Unique Machines</div><div class="card-value">%d</div></div>
    <div class="card" id="remasters"><span id="remaster" class="anchor-alias"></span><div class="card-label">Remasters</div><div class="card-value">%d</div></div>
    <div class="card" id="installations"><span id="sysinstall" class="anchor-alias"></span><span id="installation" class="anchor-alias"></span><div class="card-label">Installations</div><div class="card-value">%d</div></div>
    <div class="card" id="clones"><span id="clone" class="anchor-alias"></span><div class="card-label">Clones</div><div class="card-value">%d</div></div>
  </div>
  <div class="grid">
`, s.TotalEvents, s.UniqueMachines, s.ByAction["remaster"], s.ByAction["sysinstall"], s.CloneCount)

	writeHTMLTableCard(&buf, "country", "Country", "Country", s.ByCountry, s.TotalEvents)
	writeHTMLTableCard(&buf, "distro", "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeHTMLTableCard(&buf, "desktop", "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeHTMLTableCard(&buf, "virtualization", "Virtualization", "Virt", s.ByVirt, s.TotalEvents, "virt")
	writeHTMLTableCard(&buf, "init-system", "Init System", "Init", s.ByInit, s.TotalEvents, "init")
	writeHTMLTableCard(&buf, "compression", "Compression", "Compression", s.ByCompression, s.TotalEvents)
	writeHTMLTableCard(&buf, "arch", "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeHTMLTableCard(&buf, "version", "Eggs Version", "Version", s.ByVersion, s.TotalEvents, "eggs-version")

	buf.WriteString(`  </div>
`)

	writeHTMLTopInstallations(&buf, s.TopInstallations)
	writeHTMLRecentEvents(&buf, s.RecentEvents)

	buf.WriteString(`</div>
</body>
</html>
`)

	return buf.String()
}

func writeHTMLTopInstallations(buf *bytes.Buffer, list []TopInstallation) {
	buf.WriteString(`  <section class="panel" id="top-installations">
    <h2><a href="#top-installations" class="anchor-link">Top 10 Installations (sysinstall)</a></h2>
    <table>
      <thead>
        <tr>
          <th>Country</th>
          <th>Distro</th>
          <th>Desktop</th>
          <th>Arch</th>
          <th>Eggs Version</th>
          <th class="num">sum_sysinstall</th>
        </tr>
      </thead>
      <tbody>
`)
	if len(list) == 0 {
		buf.WriteString(`        <tr><td colspan="6" class="empty">No installations recorded yet</td></tr>
`)
	} else {
		for _, item := range list {
			fmt.Fprintf(buf, `        <tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="num">%d</td></tr>
`,
				html.EscapeString(item.Country),
				html.EscapeString(item.Distro),
				html.EscapeString(item.Desktop),
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

	addr := flag.String("addr", defaultAddr, "HTTP listen address (env CENSUS_ADDR)")
	dataPath := flag.String("data", "/var/lib/penguins-census/census.jsonl", "Path to census JSONL file or directory")
	mmdbPath := flag.String("mmdb", "/var/lib/penguins-census/dbip-country.mmdb", "Path to GeoIP MMDB file")
	reset := flag.Bool("reset", false, "Reset (truncate) all census counts and exit")
	top := flag.Bool("top", false, "Print top 10 unique sysinstall rankings and exit")
	report := flag.Bool("report", false, "Print full census markdown report to terminal and exit")
	flag.Parse()

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
	defer srv.Close()

	log.Printf("penguins-census listening on %s (data=%s, mmdb=%s)", *addr, *dataPath, *mmdbPath)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

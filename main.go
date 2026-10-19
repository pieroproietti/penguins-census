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

type CensusSummary struct {
	TotalEvents    int            `json:"total_events"`
	UniqueMachines int            `json:"unique_machines"`
	CloneCount     int            `json:"clone_count"`
	CoexistCount   int            `json:"coexist_count"`
	ByAction       map[string]int `json:"by_action"`
	ByDistro       map[string]int `json:"by_distro"`
	ByArch         map[string]int `json:"by_arch"`
	ByDesktop      map[string]int `json:"by_desktop"`
	ByVirt         map[string]int `json:"by_virt"`
	ByInit         map[string]int `json:"by_init"`
	ByCompression  map[string]int `json:"by_compression"`
	ByCountry      map[string]int `json:"by_country"`
	ByVersion      map[string]int `json:"by_version"`
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
	dir := filepath.Dir(s.dataPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(s.dataPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(line)
	return err
}

func (s *CensusServer) handleGet(w http.ResponseWriter, r *http.Request) {
	summary := CensusSummary{
		ByAction:      make(map[string]int),
		ByDistro:      make(map[string]int),
		ByArch:        make(map[string]int),
		ByDesktop:     make(map[string]int),
		ByVirt:        make(map[string]int),
		ByInit:        make(map[string]int),
		ByCompression: make(map[string]int),
		ByCountry:     make(map[string]int),
		ByVersion:     make(map[string]int),
	}
	uniqueUUIDs := make(map[string]struct{})

	s.mu.Lock()
	f, err := os.Open(s.dataPath)
	if err != nil {
		s.mu.Unlock()
		if !os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"failed to read census data"}`))
			return
		}
		respondSummary(w, r, summary)
		return
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var rec CensusRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		summary.TotalEvents++
		if rec.UUID != "" {
			uniqueUUIDs[rec.UUID] = struct{}{}
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
	}
	_ = f.Close()
	s.mu.Unlock()

	summary.UniqueMachines = len(uniqueUUIDs)
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
	fmt.Fprintf(&buf, "- Clone Count:     %d\n", s.CloneCount)
	fmt.Fprintf(&buf, "- Coexist Count:   %d\n\n", s.CoexistCount)

	writeMarkdownTable(&buf, "Action", "Action", s.ByAction, s.TotalEvents)
	writeMarkdownTable(&buf, "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeMarkdownTable(&buf, "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeMarkdownTable(&buf, "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeMarkdownTable(&buf, "Virt", "Virt", s.ByVirt, s.TotalEvents)
	writeMarkdownTable(&buf, "Init", "Init", s.ByInit, s.TotalEvents)
	writeMarkdownTable(&buf, "Compression", "Compression", s.ByCompression, s.TotalEvents)
	writeMarkdownTable(&buf, "Country", "Country", s.ByCountry, s.TotalEvents)
	writeMarkdownTable(&buf, "Version", "Version", s.ByVersion, s.TotalEvents)

	return buf.String()
}

func writeHTMLTableCard(buf *bytes.Buffer, title, colName string, m map[string]int, total int) {
	rows := sortedRows(m, total)
	fmt.Fprintf(buf, `<section class="panel"><h2>%s</h2><table><thead><tr><th>%s</th><th class="num">Count</th><th class="num">Share</th></tr></thead><tbody>`,
		html.EscapeString(title), html.EscapeString(colName))
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

func renderHTMLSummary(s CensusSummary) string {
	var buf bytes.Buffer
	mdReport := renderTextSummary(s)

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
}
.panel h2 {
  margin: 0 0 0.75rem 0;
  font-size: 1.05rem;
  color: var(--text);
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
.ai-section {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1rem 1.25rem;
}
.ai-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
}
pre {
  margin: 0;
  padding: 1rem;
  background: #0b1120;
  border: 1px solid var(--border);
  border-radius: 6px;
  overflow-x: auto;
  font-size: 0.82rem;
  color: #e2e8f0;
}
</style>
</head>
<body>
<div class="container">
  <header>
    <div>
      <h1>Penguins Eggs Anonymous Census</h1>
      <div class="subtitle">Privacy-respecting aggregated telemetry (no IP addresses stored)</div>
    </div>
    <div class="links">
      <a class="btn" href="?format=txt">Plain Text / Markdown</a>
      <a class="btn" href="?format=json">Raw JSON</a>
      <button class="btn" id="copy-ai-btn" type="button" onclick="copyForAI()">Copy for AI Analysis</button>
    </div>
  </header>
`)

	fmt.Fprintf(&buf, `  <div class="cards">
    <div class="card"><div class="card-label">Total Events</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Unique Machines</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Remasters</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Installations</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Clones</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Coexist</div><div class="card-value">%d</div></div>
  </div>
  <div class="grid">
`, s.TotalEvents, s.UniqueMachines, s.ByAction["remaster"], s.ByAction["sysinstall"], s.CloneCount, s.CoexistCount)

	writeHTMLTableCard(&buf, "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeHTMLTableCard(&buf, "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeHTMLTableCard(&buf, "Country", "Country", s.ByCountry, s.TotalEvents)
	writeHTMLTableCard(&buf, "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeHTMLTableCard(&buf, "Virtualization", "Virt", s.ByVirt, s.TotalEvents)
	writeHTMLTableCard(&buf, "Eggs Version", "Version", s.ByVersion, s.TotalEvents)
	writeHTMLTableCard(&buf, "Action", "Action", s.ByAction, s.TotalEvents)
	writeHTMLTableCard(&buf, "Init System", "Init", s.ByInit, s.TotalEvents)
	writeHTMLTableCard(&buf, "Compression", "Compression", s.ByCompression, s.TotalEvents)

	fmt.Fprintf(&buf, `  </div>
  <section class="ai-section">
    <div class="ai-header">
      <strong>Markdown / Text Report (AI Analysis Ready)</strong>
      <button class="btn" type="button" onclick="copyForAI()">Copy for AI Analysis</button>
    </div>
    <pre id="ai-report">%s</pre>
  </section>
</div>
<script>
function copyForAI() {
  var text = document.getElementById('ai-report').innerText;
  var btn = document.getElementById('copy-ai-btn');
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(function() {
      btn.textContent = 'Copied!';
      setTimeout(function() { btn.textContent = 'Copy for AI Analysis'; }, 2000);
    });
  }
}
</script>
</body>
</html>
`, html.EscapeString(mdReport))

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

func resetCensusData(dataPath string) error {
	if err := os.Truncate(dataPath, 0); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func main() {
	defaultAddr := os.Getenv("CENSUS_ADDR")
	if defaultAddr == "" {
		defaultAddr = "127.0.0.1:8080"
	}

	addr := flag.String("addr", defaultAddr, "HTTP listen address (env CENSUS_ADDR)")
	dataPath := flag.String("data", "/var/lib/penguins-census/census.jsonl", "Path to census JSONL file")
	mmdbPath := flag.String("mmdb", "/var/lib/penguins-census/dbip-country.mmdb", "Path to GeoIP MMDB file")
	reset := flag.Bool("reset", false, "Reset (truncate) all census counts and exit")
	flag.Parse()

	if *reset {
		if err := resetCensusData(*dataPath); err != nil {
			log.Fatalf("failed to reset census data (%s): %v", *dataPath, err)
		}
		fmt.Printf("Census data reset: %s\n", *dataPath)
		return
	}

	srv := NewCensusServer(*dataPath, *mmdbPath)
	defer srv.Close()

	log.Printf("penguins-census listening on %s (data=%s, mmdb=%s)", *addr, *dataPath, *mmdbPath)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

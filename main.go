package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	ByAction       map[string]int `json:"by_action"`
	ByDistro       map[string]int `json:"by_distro"`
	ByArch         map[string]int `json:"by_arch"`
	ByCountry      map[string]int `json:"by_country"`
	ByVersion      map[string]int `json:"by_version"`
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

func (s *CensusServer) handleGet(w http.ResponseWriter, _ *http.Request) {
	summary := CensusSummary{
		ByAction:  make(map[string]int),
		ByDistro:  make(map[string]int),
		ByArch:    make(map[string]int),
		ByCountry: make(map[string]int),
		ByVersion: make(map[string]int),
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
		writePrettyJSON(w, summary)
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
	writePrettyJSON(w, summary)
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

func main() {
	defaultAddr := os.Getenv("CENSUS_ADDR")
	if defaultAddr == "" {
		defaultAddr = "127.0.0.1:8080"
	}

	addr := flag.String("addr", defaultAddr, "HTTP listen address (env CENSUS_ADDR)")
	dataPath := flag.String("data", "/var/lib/penguins-census/census.jsonl", "Path to census JSONL file")
	mmdbPath := flag.String("mmdb", "/var/lib/penguins-census/dbip-country.mmdb", "Path to GeoIP MMDB file")
	flag.Parse()

	srv := NewCensusServer(*dataPath, *mmdbPath)
	defer srv.Close()

	log.Printf("penguins-census listening on %s (data=%s, mmdb=%s)", *addr, *dataPath, *mmdbPath)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

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
	defaultCommDir := os.Getenv("CENSUS_COMMUNITY_DIR")

	addr := flag.String("addr", defaultAddr, "HTTP listen address (env CENSUS_ADDR)")
	dataPath := flag.String("data", "/var/lib/penguins-census/census.jsonl", "Path to census JSONL file or directory")
	mmdbPath := flag.String("mmdb", "/var/lib/penguins-census/dbip-country.mmdb", "Path to GeoIP MMDB file")
	communityDir := flag.String("community-dir", defaultCommDir, "Directory to store community profiles (default: <dataDir>/community)")
	reset := flag.Bool("reset", false, "Reset (truncate) all census counts and exit")
	top := flag.Bool("top", false, "Print top 10 unique sysinstall rankings and exit")
	report := flag.Bool("report", false, "Print full census markdown report to terminal and exit")
	analyzeAccess := flag.String("analyze-access", "", "Path to Apache access log (or '-' for stdin) to estimate active repo machines and exit")
	saveFlag := flag.Bool("save", false, "When used with -analyze-access, save results to repo-clients.json in census data directory")
	outPath := flag.String("out", "", "Custom path to save repo analysis JSON")
	flag.Parse()

	if *analyzeAccess != "" {
		paths := append([]string{*analyzeAccess}, flag.Args()...)
		savePath := *outPath
		if savePath == "" && *saveFlag {
			savePath = filepath.Join(filepath.Dir(*dataPath), "repo-clients.json")
			if fi, err := os.Stat(*dataPath); err == nil && fi.IsDir() {
				savePath = filepath.Join(*dataPath, "repo-clients.json")
			}
		}
		if err := runAnalyzeAccessLogs(paths, *mmdbPath, *dataPath, savePath); err != nil {
			log.Fatalf("failed to analyze access logs: %v", err)
		}
		return
	}

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
	if *communityDir != "" {
		srv.SetCommunityDir(*communityDir)
	}
	defer srv.Close()

	log.Printf("penguins-census listening on %s (data=%s, mmdb=%s, community=%s)", *addr, *dataPath, *mmdbPath, srv.getCommunityDir())
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

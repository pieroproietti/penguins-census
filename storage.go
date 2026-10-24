package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *CensusServer) saveCommunityProfile(profile CommunityProfile) error {
	dir := s.getCommunityDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	target := filepath.Join(dir, fmt.Sprintf("%s.json", profile.UUID))
	tmpFile := target + ".tmp"

	if err := os.WriteFile(tmpFile, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmpFile, target)
}

func (s *CensusServer) getCommunityProfile(uuid string) (*CommunityProfile, error) {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", uuid))

	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}

	var p CommunityProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *CensusServer) listCommunityProfiles() ([]CommunityProfile, error) {
	dir := s.getCommunityDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CommunityProfile{}, nil
		}
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	profiles := make([]CommunityProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err == nil && p.UUID != "" {
			profiles = append(profiles, p)
		}
	}
	return profiles, nil
}

func (s *CensusServer) deleteCommunityProfile(uuid string) error {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", uuid))
	return os.Remove(target)
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


func isCommunityProfileEnabled(p CommunityProfile) bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}


func parseFlexibleTimestamp(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

func getArtisanActivityTime(a *ArtisanProfile) time.Time {
	tUp := parseFlexibleTimestamp(a.UpdatedAt)
	tRem := parseFlexibleTimestamp(a.LastRemasterAt)
	if tRem.After(tUp) {
		return tRem
	}
	return tUp
}

func sortArtisansByActivity(artisans []ArtisanProfile) {
	sort.Slice(artisans, func(i, j int) bool {
		ti := getArtisanActivityTime(&artisans[i])
		tj := getArtisanActivityTime(&artisans[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		if artisans[i].Nickname != artisans[j].Nickname {
			return artisans[i].Nickname < artisans[j].Nickname
		}
		return artisans[i].UUID < artisans[j].UUID
	})
}

func (s *CensusServer) enrichSingleProfile(profile CommunityProfile, dataPath string) ArtisanProfile {
	artisan := ArtisanProfile{
		UUID:      profile.UUID,
		Nickname:  profile.Nickname,
		Location:  profile.Location,
		Bio:       profile.Bio,
		Contact:   profile.Contact,
		Socials:   profile.Socials,
		SourceURL: profile.SourceURL,
		Enabled:   profile.Enabled,
		UpdatedAt: profile.UpdatedAt,
	}

	if dataPath == "" {
		return artisan
	}

	_ = forEachLogLine(dataPath, func(line []byte) {
		var rec CensusRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return
		}
		if strings.TrimSpace(rec.UUID) != profile.UUID {
			return
		}

		if rec.Country != "" {
			artisan.Country = strings.TrimSpace(rec.Country)
		}

		if rec.Action == "remaster" {
			artisan.RemasterCount++
			tRec := parseFlexibleTimestamp(rec.Timestamp)
			tOld := parseFlexibleTimestamp(artisan.LastRemasterAt)
			isNewer := false
			if artisan.LastRemasterAt == "" {
				isNewer = true
			} else if !tRec.IsZero() && !tOld.IsZero() {
				isNewer = tRec.After(tOld) || tRec.Equal(tOld)
			} else {
				isNewer = rec.Timestamp >= artisan.LastRemasterAt
			}

			if isNewer {
				artisan.LastRemasterAt = rec.Timestamp
				if rec.Country != "" {
					artisan.Country = strings.TrimSpace(rec.Country)
				}
				artisan.LatestRemaster = &ArtisanRemasterInfo{
					Distro:         rec.Distro,
					System:         rec.System,
					Country:        artisan.Country,
					Timestamp:      rec.Timestamp,
					LastRemasterAt: rec.Timestamp,
					Count:          artisan.RemasterCount,
				}
			}
		}
	})

	if artisan.LatestRemaster != nil {
		artisan.LatestRemaster.Count = artisan.RemasterCount
	}

	return artisan
}

func loadEnrichedArtisans(dataPath, commDir string) ([]ArtisanProfile, error) {
	if commDir == "" {
		return []ArtisanProfile{}, nil
	}

	entries, err := os.ReadDir(commDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ArtisanProfile{}, nil
		}
		return nil, err
	}

	artisanMap := make(map[string]*ArtisanProfile)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(commDir, entry.Name()))
		if err != nil {
			continue
		}
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err != nil || p.UUID == "" {
			continue
		}
		if !isCommunityProfileEnabled(p) {
			continue
		}

		artisanMap[p.UUID] = &ArtisanProfile{
			UUID:      p.UUID,
			Nickname:  p.Nickname,
			Location:  p.Location,
			Bio:       p.Bio,
			Contact:   p.Contact,
			Socials:   p.Socials,
			SourceURL: p.SourceURL,
			Enabled:   p.Enabled,
			UpdatedAt: p.UpdatedAt,
		}
	}

	if len(artisanMap) == 0 {
		return []ArtisanProfile{}, nil
	}

	if dataPath != "" {
		_ = forEachLogLine(dataPath, func(line []byte) {
			var rec CensusRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return
			}
			uuid := strings.TrimSpace(rec.UUID)
			if uuid == "" {
				return
			}
			artisan, exists := artisanMap[uuid]
			if !exists {
				return
			}

			if rec.Country != "" {
				artisan.Country = strings.TrimSpace(rec.Country)
			}

			if rec.Action == "remaster" {
				artisan.RemasterCount++
				tRec := parseFlexibleTimestamp(rec.Timestamp)
				tOld := parseFlexibleTimestamp(artisan.LastRemasterAt)
				isNewer := false
				if artisan.LastRemasterAt == "" {
					isNewer = true
				} else if !tRec.IsZero() && !tOld.IsZero() {
					isNewer = tRec.After(tOld) || tRec.Equal(tOld)
				} else {
					isNewer = rec.Timestamp >= artisan.LastRemasterAt
				}

				if isNewer {
					artisan.LastRemasterAt = rec.Timestamp
					if rec.Country != "" {
						artisan.Country = strings.TrimSpace(rec.Country)
					}
					artisan.LatestRemaster = &ArtisanRemasterInfo{
						Distro:         rec.Distro,
						System:         rec.System,
						Country:        artisan.Country,
						Timestamp:      rec.Timestamp,
						LastRemasterAt: rec.Timestamp,
						Count:          artisan.RemasterCount,
					}
				}
			}
		})

		for _, a := range artisanMap {
			if a.LatestRemaster != nil {
				a.LatestRemaster.Count = a.RemasterCount
			}
		}
	}

	artisans := make([]ArtisanProfile, 0, len(artisanMap))
	for _, a := range artisanMap {
		artisans = append(artisans, *a)
	}

	sortArtisansByActivity(artisans)
	return artisans, nil
}

func loadCensusSummary(dataPath string, commDirs ...string) (CensusSummary, error) {
	var commDir string
	if len(commDirs) > 0 && commDirs[0] != "" {
		commDir = commDirs[0]
	} else if dataPath != "" {
		fi, err := os.Stat(dataPath)
		if err == nil && fi.IsDir() {
			commDir = filepath.Join(dataPath, "community")
		} else {
			commDir = filepath.Join(filepath.Dir(dataPath), "community")
		}
	} else {
		commDir = "/var/lib/penguins-census/community"
	}

	activeArtisansMap := make(map[string]CommunityProfile)
	if commDir != "" {
		if entries, err := os.ReadDir(commDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(commDir, entry.Name()))
				if err != nil {
					continue
				}
				var p CommunityProfile
				if err := json.Unmarshal(data, &p); err == nil && p.UUID != "" && isCommunityProfileEnabled(p) {
					activeArtisansMap[p.UUID] = p
				}
			}
		}
	}

	summary := CensusSummary{
		RecentEvents:     make([]RecentEvent, 0),
		TopInstallations: make([]TopInstallation, 0),
		ByAction:         make(map[string]int),
		ByDistro:         make(map[string]int),
		ByArch:           make(map[string]int),
		ByDesktop:        make(map[string]int),
		ByDisplayServer:  make(map[string]int),
		ByVirt:           make(map[string]int),
		ByInit:           make(map[string]int),
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
		if len(lastRecords) > 10 {
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

		displayServer := strings.TrimSpace(rec.System.DisplayServer)
		if displayServer == "" {
			displayServer = "headless/cli"
		}
		summary.ByDisplayServer[displayServer]++

		virt := strings.TrimSpace(rec.System.Virt)
		if virt == "" || virt == "none" || virt == "physical/none" {
			virt = "bare-metal"
		}
		summary.ByVirt[virt]++

		if rec.System.Init != "" {
			summary.ByInit[rec.System.Init]++
		}
		if rec.Options.Clone {
			if rec.Action == "remaster" {
				summary.RemasterClones++
			} else if rec.Action == "sysinstall" {
				summary.SysinstallClones++
			}
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
		displayServer := strings.TrimSpace(r.System.DisplayServer)
		if displayServer == "" {
			displayServer = "headless/cli"
		}
		ev := RecentEvent{
			Date:          formatEventDate(r.Timestamp),
			Country:       country,
			Distro:        distro,
			Desktop:       desktop,
			DisplayServer: displayServer,
			Remaster:      r.Action == "remaster",
			Sysinstall:    r.Action == "sysinstall",
		}
		recUUID := strings.TrimSpace(r.UUID)
		if artisan, ok := activeArtisansMap[recUUID]; ok {
			ev.ArtisanNickname = artisan.Nickname
			if isSafeURL(artisan.SourceURL) {
				ev.ArtisanURL = artisan.SourceURL
			} else {
				ev.ArtisanURL = "/api/community"
			}
		}
		summary.RecentEvents = append(summary.RecentEvents, ev)
	}

	summary.UniqueMachines = len(uniqueUUIDs)
	summary.TotalCountries = len(summary.ByCountry)
	summary.TotalDistros = len(summary.ByDistro)
	summary.CountryCount = summary.TotalCountries
	summary.DistroCount = summary.TotalDistros

	// Aggregate unique sysinstall installations
	type groupKey struct {
		country       string
		distro        string
		desktop       string
		displayServer string
		arch          string
		eggsVersion   string
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
		displayServer := strings.TrimSpace(r.System.DisplayServer)
		if displayServer == "" {
			displayServer = "headless/cli"
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
			country:       country,
			distro:        distro,
			desktop:       desktop,
			displayServer: displayServer,
			arch:          arch,
			eggsVersion:   ver,
		}
		groupCounts[key]++
	}

	topList := make([]TopInstallation, 0, len(groupCounts))
	for k, count := range groupCounts {
		topList = append(topList, TopInstallation{
			Country:       k.country,
			Distro:        k.distro,
			Desktop:       k.desktop,
			DisplayServer: k.displayServer,
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
		if topList[i].DisplayServer != topList[j].DisplayServer {
			return topList[i].DisplayServer < topList[j].DisplayServer
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

	repoStatsPath := filepath.Join(filepath.Dir(dataPath), "repo-clients.json")
	if fi, err := os.Stat(dataPath); err == nil && fi.IsDir() {
		repoStatsPath = filepath.Join(dataPath, "repo-clients.json")
	}
	if rStats, err := loadRepoAnalysisSummary(repoStatsPath); err == nil {
		summary.RepoClients = rStats
	}

	if commDir != "" {
		if artisans, err := loadEnrichedArtisans(dataPath, commDir); err == nil {
			summary.Artisans = artisans
		}
	}

	return summary, nil
}


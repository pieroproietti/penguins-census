package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func GenerateArtisanToken() (rawToken, tokenHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	rawToken = "egg_pat_" + hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash = hex.EncodeToString(hash[:])
	return rawToken, tokenHash, nil
}

func HashArtisanToken(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}

func (s *CensusServer) findProfileByToken(rawToken string) (*CommunityProfile, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, fmt.Errorf("token is empty")
	}
	targetHash := HashArtisanToken(rawToken)

	profiles, err := s.listCommunityProfiles()
	if err != nil {
		return nil, err
	}

	for _, p := range profiles {
		if p.TokenHash != "" && subtle.ConstantTimeCompare([]byte(p.TokenHash), []byte(targetHash)) == 1 {
			if isCommunityProfileEnabled(p) {
				return &p, nil
			}
		}
	}
	return nil, fmt.Errorf("profile not found or disabled")
}

func (s *CensusServer) IssueArtisanToken(artisanID string) (string, error) {
	artisanID = strings.TrimSpace(artisanID)
	if artisanID == "" {
		return "", fmt.Errorf("artisan id cannot be empty")
	}

	rawToken, tokenHash, err := GenerateArtisanToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, _ := s.getCommunityProfile(artisanID)
	if p == nil {
		enabled := true
		p = &CommunityProfile{
			ID:        artisanID,
			UUID:      artisanID,
			Nickname:  artisanID,
			TokenHash: tokenHash,
			Enabled:   &enabled,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
	} else {
		p.TokenHash = tokenHash
		p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if err := s.saveCommunityProfile(*p); err != nil {
		return "", err
	}
	return rawToken, nil
}

func (s *CensusServer) saveCommunityProfile(profile CommunityProfile) error {
	dir := s.getCommunityDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	id := profile.GetID()
	if profile.ID == "" {
		profile.ID = id
	}
	if profile.UUID == "" {
		profile.UUID = id
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	target := filepath.Join(dir, fmt.Sprintf("%s.json", id))
	tmpFile := target + ".tmp"

	if err := os.WriteFile(tmpFile, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmpFile, target)
}

func (s *CensusServer) getCommunityProfile(idOrUUID string) (*CommunityProfile, error) {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", idOrUUID))

	data, err := os.ReadFile(target)
	if err == nil {
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err == nil {
			if p.ID == "" {
				p.ID = p.UUID
			}
			if p.UUID == "" {
				p.UUID = p.ID
			}
			return &p, nil
		}
	}

	// Fallback: search directory for profile where p.GetID() == idOrUUID or p.UUID == idOrUUID
	profiles, errList := s.listCommunityProfiles()
	if errList == nil {
		for _, p := range profiles {
			if p.GetID() == idOrUUID || p.UUID == idOrUUID || p.ID == idOrUUID {
				return &p, nil
			}
		}
	}

	return nil, os.ErrNotExist
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
		if err := json.Unmarshal(data, &p); err == nil {
			if p.ID == "" {
				p.ID = p.UUID
			}
			if p.UUID == "" {
				p.UUID = p.ID
			}
			if p.GetID() != "" {
				profiles = append(profiles, p)
			}
		}
	}
	return profiles, nil
}

func (s *CensusServer) deleteCommunityProfile(idOrUUID string) error {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", idOrUUID))
	err := os.Remove(target)
	if err == nil {
		return nil
	}

	// Fallback: search directory for matching profile
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readDataErr := os.ReadFile(path)
		if readDataErr != nil {
			continue
		}
		var p CommunityProfile
		if errUnmarshal := json.Unmarshal(data, &p); errUnmarshal == nil {
			if p.GetID() == idOrUUID || p.UUID == idOrUUID || p.ID == idOrUUID {
				return os.Remove(path)
			}
		}
	}
	return err
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
		return artisans[i].GetID() < artisans[j].GetID()
	})
}

type artisanCollector struct {
	profile        CommunityProfile
	country        string
	lastRemasterAt string
	remasterCount  int
	installsCount  int
	latestRemaster *ArtisanRemasterInfo
	liveDistrosMap map[string]*ArtisanLiveSummary
}

func resolveArtisan(rec CensusRecord, directMachines map[string]map[string]bool, parentMap map[string]string, activeProfiles map[string]CommunityProfile) (directArtisan, derivedArtisan string) {
	uuid := strings.TrimSpace(rec.UUID)
	recArtisanID := strings.TrimSpace(rec.ArtisanID)

	// 1. Direct artisan by authenticated ArtisanID
	if recArtisanID != "" {
		if _, ok := activeProfiles[recArtisanID]; ok {
			directArtisan = recArtisanID
		}
	}
	// 2. Direct artisan by machine UUID
	if directArtisan == "" && uuid != "" {
		for aID, machines := range directMachines {
			if machines[uuid] {
				directArtisan = aID
				break
			}
		}
	}

	// 3. Lineage via ParentID (direct or transitive)
	pID := strings.TrimSpace(rec.ParentID)
	if pID != "" {
		visited := make(map[string]bool)
		curr := pID
		for curr != "" && !visited[curr] {
			visited[curr] = true
			for aID, machines := range directMachines {
				if machines[curr] || curr == aID || (activeProfiles[aID].UUID != "" && curr == activeProfiles[aID].UUID) {
					derivedArtisan = aID
					break
				}
			}
			if derivedArtisan != "" {
				break
			}
			curr = parentMap[curr]
		}
	}

	return directArtisan, derivedArtisan
}

func enrichArtisans(dataPath string, profiles []CommunityProfile) []ArtisanProfile {
	if len(profiles) == 0 {
		return []ArtisanProfile{}
	}

	collectors := make(map[string]*artisanCollector, len(profiles))
	activeMap := make(map[string]CommunityProfile, len(profiles))
	directMachines := make(map[string]map[string]bool, len(profiles))
	parentMap := make(map[string]string)

	for _, p := range profiles {
		id := p.GetID()
		activeMap[id] = p
		collectors[id] = &artisanCollector{
			profile:        p,
			liveDistrosMap: make(map[string]*ArtisanLiveSummary),
		}
		directMachines[id] = make(map[string]bool)
	}

	if dataPath != "" {
		// Pass 1: Build direct machines & parent lineage
		_ = forEachLogLine(dataPath, func(line []byte) {
			var rec CensusRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return
			}
			uuid := strings.TrimSpace(rec.UUID)
			if uuid == "" {
				return
			}

			// Direct authenticated artisan
			artisanID := strings.TrimSpace(rec.ArtisanID)
			if artisanID != "" {
				if _, ok := activeMap[artisanID]; ok {
					directMachines[artisanID][uuid] = true
				}
			}

			// Legacy fallback: match UUID or ID
			for pID, p := range activeMap {
				if uuid == pID || (p.UUID != "" && uuid == p.UUID) {
					directMachines[pID][uuid] = true
				}
			}

			parentID := strings.TrimSpace(rec.ParentID)
			if parentID != "" {
				parentMap[uuid] = parentID
			}
		})

		// Pass 2: Aggregate stats & lineage
		_ = forEachLogLine(dataPath, func(line []byte) {
			var rec CensusRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return
			}

			directArtisan, derivedArtisan := resolveArtisan(rec, directMachines, parentMap, activeMap)

			if directArtisan != "" {
				col := collectors[directArtisan]
				if rec.Country != "" {
					col.country = strings.TrimSpace(rec.Country)
				}
				if rec.Action == "remaster" {
					col.remasterCount++

					// Update LatestRemaster
					tRec := parseFlexibleTimestamp(rec.Timestamp)
					tOld := parseFlexibleTimestamp(col.lastRemasterAt)
					isNewer := false
					if col.lastRemasterAt == "" {
						isNewer = true
					} else if !tRec.IsZero() && !tOld.IsZero() {
						isNewer = tRec.After(tOld) || tRec.Equal(tOld)
					} else {
						isNewer = rec.Timestamp >= col.lastRemasterAt
					}

					if isNewer {
						col.lastRemasterAt = rec.Timestamp
						if rec.Country != "" {
							col.country = strings.TrimSpace(rec.Country)
						}
						col.latestRemaster = &ArtisanRemasterInfo{
							Distro:         rec.Distro,
							System:         rec.System,
							Country:        col.country,
							Timestamp:      rec.Timestamp,
							LastRemasterAt: rec.Timestamp,
							Count:          col.remasterCount,
						}
					}

					// Update LiveDistros catalog
					distroName := strings.TrimSpace(rec.Distro.ID)
					if distroName == "" {
						distroName = "linux"
					}
					version := strings.TrimSpace(rec.Distro.Release)
					if version == "" {
						version = strings.TrimSpace(rec.Distro.Codename)
					}
					desktop := strings.TrimSpace(rec.System.Desktop)
					if desktop == "" {
						desktop = "headless/cli"
					}
					arch := strings.TrimSpace(rec.System.Arch)
					key := fmt.Sprintf("%s|%s|%s|%s", distroName, version, desktop, arch)
					if existing, exists := col.liveDistrosMap[key]; exists {
						existing.Count++
						tRec := parseFlexibleTimestamp(rec.Timestamp)
						tOld := parseFlexibleTimestamp(existing.LastSeen)
						if existing.LastSeen == "" || tRec.After(tOld) || rec.Timestamp >= existing.LastSeen {
							existing.LastSeen = rec.Timestamp
						}
					} else {
						col.liveDistrosMap[key] = &ArtisanLiveSummary{
							DistroName: distroName,
							Version:    version,
							Desktop:    desktop,
							Arch:       arch,
							Count:      1,
							LastSeen:   rec.Timestamp,
						}
					}
				}
			}

			// Derived sysinstall installs via ParentID
			if rec.Action == "sysinstall" && derivedArtisan != "" {
				col := collectors[derivedArtisan]
				col.installsCount++
			}
		})
	}

	artisans := make([]ArtisanProfile, 0, len(collectors))
	for id, col := range collectors {
		p := col.profile
		if col.latestRemaster != nil {
			col.latestRemaster.Count = col.remasterCount
		}

		liveDistros := make([]ArtisanLiveSummary, 0, len(col.liveDistrosMap))
		for _, ld := range col.liveDistrosMap {
			liveDistros = append(liveDistros, *ld)
		}
		sort.Slice(liveDistros, func(i, j int) bool {
			if liveDistros[i].Count != liveDistros[j].Count {
				return liveDistros[i].Count > liveDistros[j].Count
			}
			if liveDistros[i].LastSeen != liveDistros[j].LastSeen {
				return liveDistros[i].LastSeen > liveDistros[j].LastSeen
			}
			return liveDistros[i].DistroName < liveDistros[j].DistroName
		})

		machinesCount := len(directMachines[id])

		artisan := ArtisanProfile{
			ID:             id,
			UUID:           p.UUID,
			AuthType:       p.AuthType,
			Nickname:       p.Nickname,
			Location:       p.Location,
			Bio:            p.Bio,
			Contact:        p.Contact,
			Socials:        p.Socials,
			SourceURL:      p.SourceURL,
			Enabled:        p.Enabled,
			UpdatedAt:      p.UpdatedAt,
			Country:        col.country,
			LastRemasterAt: col.lastRemasterAt,
			MachinesCount:  machinesCount,
			RemasterCount:  col.remasterCount,
			InstallsCount:  col.installsCount,
			LiveDistros:    liveDistros,
			LatestRemaster: col.latestRemaster,
		}
		artisans = append(artisans, artisan)
	}

	sortArtisansByActivity(artisans)
	return artisans
}

func (s *CensusServer) enrichSingleProfile(profile CommunityProfile, dataPath string) ArtisanProfile {
	if profile.ID == "" {
		profile.ID = profile.UUID
	}
	if profile.UUID == "" {
		profile.UUID = profile.ID
	}
	res := enrichArtisans(dataPath, []CommunityProfile{profile})
	if len(res) > 0 {
		return res[0]
	}
	return ArtisanProfile{
		ID:        profile.GetID(),
		UUID:      profile.UUID,
		AuthType:  profile.AuthType,
		Nickname:  profile.Nickname,
		Location:  profile.Location,
		Bio:       profile.Bio,
		Contact:   profile.Contact,
		Socials:   profile.Socials,
		SourceURL: profile.SourceURL,
		Enabled:   profile.Enabled,
		UpdatedAt: profile.UpdatedAt,
	}
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

	profiles := make([]CommunityProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(commDir, entry.Name()))
		if err != nil {
			continue
		}
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		if p.ID == "" {
			p.ID = p.UUID
		}
		if p.UUID == "" {
			p.UUID = p.ID
		}
		if p.GetID() == "" || !isCommunityProfileEnabled(p) {
			continue
		}
		profiles = append(profiles, p)
	}

	if len(profiles) == 0 {
		return []ArtisanProfile{}, nil
	}

	return enrichArtisans(dataPath, profiles), nil
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
				if err := json.Unmarshal(data, &p); err == nil && isCommunityProfileEnabled(p) {
					if p.ID == "" {
						p.ID = p.UUID
					}
					if p.UUID == "" {
						p.UUID = p.ID
					}
					if p.GetID() != "" {
						activeArtisansMap[p.GetID()] = p
					}
				}
			}
		}
	}

	directMachines := make(map[string]map[string]bool)
	for aID := range activeArtisansMap {
		directMachines[aID] = make(map[string]bool)
	}
	parentMap := make(map[string]string)

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

	// Pass 1: Build direct machines & parent lineage
	_ = forEachLogLine(dataPath, func(line []byte) {
		var rec CensusRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return
		}
		uuid := strings.TrimSpace(rec.UUID)
		if uuid == "" {
			return
		}
		artisanID := strings.TrimSpace(rec.ArtisanID)
		if artisanID != "" {
			if _, ok := activeArtisansMap[artisanID]; ok {
				directMachines[artisanID][uuid] = true
			}
		}
		for pID, p := range activeArtisansMap {
			if uuid == pID || (p.UUID != "" && uuid == p.UUID) {
				directMachines[pID][uuid] = true
			}
		}
		parentID := strings.TrimSpace(rec.ParentID)
		if parentID != "" {
			parentMap[uuid] = parentID
		}
	})

	// Pass 2: Aggregate events
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
			EggsVersion:   strings.TrimSpace(r.EggsVersion),
			Remaster:      r.Action == "remaster",
			Sysinstall:    r.Action == "sysinstall",
		}

		directArtisan, derivedArtisan := resolveArtisan(r, directMachines, parentMap, activeArtisansMap)
		artisanID := directArtisan
		if artisanID == "" {
			artisanID = derivedArtisan
		}
		if artisanID != "" {
			if artisan, ok := activeArtisansMap[artisanID]; ok {
				ev.ArtisanNickname = artisan.Nickname
				if isSafeURL(artisan.SourceURL) {
					ev.ArtisanURL = artisan.SourceURL
				} else {
					ev.ArtisanURL = "/api/community"
				}
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
		country         string
		distro          string
		desktop         string
		displayServer   string
		arch            string
		eggsVersion     string
		artisanNickname string
		artisanURL      string
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

		directArtisan, derivedArtisan := resolveArtisan(r, directMachines, parentMap, activeArtisansMap)
		artisanID := directArtisan
		if artisanID == "" {
			artisanID = derivedArtisan
		}
		artisanNick := ""
		artisanURL := ""
		if artisanID != "" {
			if artisan, ok := activeArtisansMap[artisanID]; ok {
				artisanNick = artisan.Nickname
				if isSafeURL(artisan.SourceURL) {
					artisanURL = artisan.SourceURL
				} else {
					artisanURL = "/api/community"
				}
			}
		}

		key := groupKey{
			country:         country,
			distro:          distro,
			desktop:         desktop,
			displayServer:   displayServer,
			arch:            arch,
			eggsVersion:     ver,
			artisanNickname: artisanNick,
			artisanURL:      artisanURL,
		}
		groupCounts[key]++
	}

	topList := make([]TopInstallation, 0, len(groupCounts))
	for k, count := range groupCounts {
		topList = append(topList, TopInstallation{
			Country:         k.country,
			Distro:          k.distro,
			Desktop:         k.desktop,
			DisplayServer:   k.displayServer,
			Arch:            k.arch,
			EggsVersion:     k.eggsVersion,
			SumSysinstall:   count,
			ArtisanNickname: k.artisanNickname,
			ArtisanURL:      k.artisanURL,
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
		if topList[i].EggsVersion != topList[j].EggsVersion {
			return topList[i].EggsVersion < topList[j].EggsVersion
		}
		return topList[i].ArtisanNickname < topList[j].ArtisanNickname
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


/*
 * penguins-census - Copyright (C) 2026-2026 Piero Proietti
 * Author: Piero Proietti <piero.proietti@gmail.com>
 * https://penguins-eggs.net
 *
 * This file is part of penguins-census.
 * Released under the GNU GPL v3 license.
 */

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

func GenerateAuthorToken() (rawToken, tokenHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	rawToken = "egg_pat_" + hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash = hex.EncodeToString(hash[:])
	return rawToken, tokenHash, nil
}

// Deprecated alias
func GenerateArtisanToken() (rawToken, tokenHash string, err error) {
	return GenerateAuthorToken()
}

func HashAuthorToken(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}

// Deprecated alias
func HashArtisanToken(rawToken string) string {
	return HashAuthorToken(rawToken)
}

func (s *CensusServer) findProfileByToken(rawToken string) (*CommunityProfile, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, fmt.Errorf("token is empty")
	}
	targetHash := HashAuthorToken(rawToken)

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

func (s *CensusServer) IssueAuthorToken(authorID string) (string, error) {
	authorID = strings.TrimSpace(authorID)
	if authorID == "" {
		return "", fmt.Errorf("author id cannot be empty")
	}

	rawToken, tokenHash, err := GenerateAuthorToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, _ := s.getCommunityProfile(authorID)
	now := time.Now().UTC().Format(time.RFC3339)
	if p == nil {
		p = &CommunityProfile{
			AuthorID:     authorID,
			TokenHash:    tokenHash,
			ClaimedUUIDs: []string{},
			Enabled:      true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
	} else {
		p.TokenHash = tokenHash
		p.UpdatedAt = now
	}

	if err := s.saveCommunityProfile(*p); err != nil {
		return "", err
	}
	return rawToken, nil
}

// Deprecated alias
func (s *CensusServer) IssueArtisanToken(artisanID string) (string, error) {
	return s.IssueAuthorToken(artisanID)
}

func (s *CensusServer) saveCommunityProfile(profile CommunityProfile) error {
	dir := s.getCommunityDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	authorID := profile.GetID()
	if authorID == "" {
		return fmt.Errorf("author_id cannot be empty")
	}

	claimed := profile.ClaimedUUIDs
	if claimed == nil {
		claimed = []string{}
	}

	createdAt := profile.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}
	updatedAt := profile.UpdatedAt
	if updatedAt == "" {
		updatedAt = createdAt
	}

	rec := CommunityProfile{
		AuthorID:     authorID,
		TokenHash:    profile.TokenHash,
		ClaimedUUIDs: claimed,
		Enabled:      profile.Enabled,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	target := filepath.Join(dir, fmt.Sprintf("%s.json", authorID))
	tmpFile := target + ".tmp"

	if err := os.WriteFile(tmpFile, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmpFile, target)
}

func (s *CensusServer) claimUUIDForAuthor(authorID string, uuid string) error {
	dir := s.getCommunityDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), ".json")
		if strings.EqualFold(base, authorID) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		if strings.EqualFold(p.GetID(), authorID) {
			continue
		}

		changed := false
		newClaimed := make([]string, 0, len(p.ClaimedUUIDs))
		for _, u := range p.ClaimedUUIDs {
			if strings.EqualFold(u, uuid) {
				changed = true
			} else {
				newClaimed = append(newClaimed, u)
			}
		}
		if changed {
			p.ClaimedUUIDs = newClaimed
			p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			_ = s.saveCommunityProfile(p)
		}
	}
	return nil
}

func (s *CensusServer) getCommunityProfile(idOrAuthorID string) (*CommunityProfile, error) {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", idOrAuthorID))

	data, err := os.ReadFile(target)
	if err == nil {
		var p CommunityProfile
		if err := json.Unmarshal(data, &p); err == nil {
			if p.AuthorID == "" {
				p.AuthorID = idOrAuthorID
			}
			if p.ClaimedUUIDs == nil {
				p.ClaimedUUIDs = []string{}
			}
			return &p, nil
		}
	}

	// Fallback: search directory for profile where AuthorID == idOrAuthorID or ClaimedUUIDs contains it
	profiles, errList := s.listCommunityProfiles()
	if errList == nil {
		for _, p := range profiles {
			if strings.EqualFold(p.AuthorID, idOrAuthorID) {
				return &p, nil
			}
			for _, u := range p.ClaimedUUIDs {
				if strings.EqualFold(u, idOrAuthorID) {
					return &p, nil
				}
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
			if p.AuthorID == "" {
				p.AuthorID = strings.TrimSuffix(entry.Name(), ".json")
			}
			if p.ClaimedUUIDs == nil {
				p.ClaimedUUIDs = []string{}
			}
			if p.GetID() != "" {
				profiles = append(profiles, p)
			}
		}
	}
	return profiles, nil
}

func (s *CensusServer) deleteCommunityProfile(authorID string) error {
	dir := s.getCommunityDir()
	target := filepath.Join(dir, fmt.Sprintf("%s.json", authorID))
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
			if strings.EqualFold(p.GetID(), authorID) {
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
	return p.Enabled
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

func getAuthorActivityTime(a *AuthorProfile) time.Time {
	tUp := parseFlexibleTimestamp(a.UpdatedAt)
	tRem := parseFlexibleTimestamp(a.LastRemasterAt)
	if tRem.After(tUp) {
		return tRem
	}
	return tUp
}

func sortAuthorsByActivity(authors []AuthorProfile) {
	sort.Slice(authors, func(i, j int) bool {
		ti := getAuthorActivityTime(&authors[i])
		tj := getAuthorActivityTime(&authors[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		if authors[i].DisplayName != authors[j].DisplayName {
			return authors[i].DisplayName < authors[j].DisplayName
		}
		return authors[i].AuthorID < authors[j].AuthorID
	})
}

// Deprecated alias
func sortArtisansByActivity(artisans []AuthorProfile) {
	sortAuthorsByActivity(artisans)
}

type authorCollector struct {
	profile        CommunityProfile
	country        string
	lastRemasterAt string
	remasterCount  int
	installsCount  int
	latestRemaster *AuthorRemasterInfo
	liveDistrosMap map[string]*AuthorLiveSummary
}

func resolveAuthor(rec CensusRecord, directMachines map[string]map[string]bool, parentMap map[string]string, activeProfiles map[string]CommunityProfile) (directAuthor, derivedAuthor string) {
	uuid := strings.TrimSpace(rec.UUID)
	recAuthorID := strings.TrimSpace(rec.AuthorID)
	if recAuthorID == "" {
		recAuthorID = strings.TrimSpace(rec.ArtisanID) // fallback
	}

	// 1. Direct author by authenticated AuthorID
	if recAuthorID != "" {
		if _, ok := activeProfiles[recAuthorID]; ok {
			directAuthor = recAuthorID
		}
	}
	// 2. Direct author by machine UUID
	if directAuthor == "" && uuid != "" {
		for aID, machines := range directMachines {
			if machines[uuid] {
				directAuthor = aID
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
				if machines[curr] || curr == aID {
					derivedAuthor = aID
					break
				}
			}
			if derivedAuthor != "" {
				break
			}
			curr = parentMap[curr]
		}
	}

	return directAuthor, derivedAuthor
}

// Deprecated alias
func resolveArtisan(rec CensusRecord, directMachines map[string]map[string]bool, parentMap map[string]string, activeProfiles map[string]CommunityProfile) (directArtisan, derivedArtisan string) {
	return resolveAuthor(rec, directMachines, parentMap, activeProfiles)
}

var defaultGravatarClient = NewGravatarClient(6 * time.Hour)

func enrichAuthors(dataPath string, profiles []CommunityProfile, gClients ...*GravatarClient) []AuthorProfile {
	if len(profiles) == 0 {
		return []AuthorProfile{}
	}

	gClient := defaultGravatarClient
	if len(gClients) > 0 && gClients[0] != nil {
		gClient = gClients[0]
	}

	collectors := make(map[string]*authorCollector, len(profiles))
	activeMap := make(map[string]CommunityProfile, len(profiles))
	directMachines := make(map[string]map[string]bool, len(profiles))
	parentMap := make(map[string]string)

	for _, p := range profiles {
		id := p.GetID()
		activeMap[id] = p
		collectors[id] = &authorCollector{
			profile:        p,
			liveDistrosMap: make(map[string]*AuthorLiveSummary),
		}
		directMachines[id] = make(map[string]bool)
		for _, u := range p.ClaimedUUIDs {
			directMachines[id][u] = true
		}
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

			// Direct authenticated author
			authorID := strings.TrimSpace(rec.AuthorID)
			if authorID == "" {
				authorID = strings.TrimSpace(rec.ArtisanID)
			}
			if authorID != "" {
				if _, ok := activeMap[authorID]; ok {
					directMachines[authorID][uuid] = true
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

			directAuthor, derivedAuthor := resolveAuthor(rec, directMachines, parentMap, activeMap)

			if directAuthor != "" {
				col := collectors[directAuthor]
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
						col.latestRemaster = &AuthorRemasterInfo{
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
						col.liveDistrosMap[key] = &AuthorLiveSummary{
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
			if rec.Action == "sysinstall" && derivedAuthor != "" {
				col := collectors[derivedAuthor]
				col.installsCount++
			}
		})
	}

	authors := make([]AuthorProfile, 0, len(collectors))
	for id, col := range collectors {
		p := col.profile
		if col.latestRemaster != nil {
			col.latestRemaster.Count = col.remasterCount
		}

		liveDistros := make([]AuthorLiveSummary, 0, len(col.liveDistrosMap))
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

		machinesCount := len(p.ClaimedUUIDs)
		if len(directMachines[id]) > machinesCount {
			machinesCount = len(directMachines[id])
		}

		dispName, avatarURL, profileURL := gClient.Resolve(id)

		author := AuthorProfile{
			AuthorID:       id,
			DisplayName:    dispName,
			AvatarURL:      avatarURL,
			ProfileURL:     profileURL,
			ClaimedUUIDs:   p.ClaimedUUIDs,
			Enabled:        p.Enabled,
			CreatedAt:      p.CreatedAt,
			UpdatedAt:      p.UpdatedAt,
			Country:        col.country,
			LastRemasterAt: col.lastRemasterAt,
			MachinesCount:  machinesCount,
			RemasterCount:  col.remasterCount,
			InstallsCount:  col.installsCount,
			LiveDistros:    liveDistros,
			LatestRemaster: col.latestRemaster,
		}
		authors = append(authors, author)
	}

	sortAuthorsByActivity(authors)
	return authors
}

// Deprecated alias
func enrichArtisans(dataPath string, profiles []CommunityProfile, gClients ...*GravatarClient) []AuthorProfile {
	return enrichAuthors(dataPath, profiles, gClients...)
}

func (s *CensusServer) enrichSingleProfile(profile CommunityProfile, dataPath string) AuthorProfile {
	res := enrichAuthors(dataPath, []CommunityProfile{profile}, s.gravatarClient)
	if len(res) > 0 {
		return res[0]
	}
	dispName, avatarURL, profileURL := s.gravatarClient.Resolve(profile.GetID())
	return AuthorProfile{
		AuthorID:      profile.GetID(),
		DisplayName:   dispName,
		AvatarURL:     avatarURL,
		ProfileURL:    profileURL,
		ClaimedUUIDs:  profile.ClaimedUUIDs,
		Enabled:       profile.Enabled,
		CreatedAt:     profile.CreatedAt,
		UpdatedAt:     profile.UpdatedAt,
		MachinesCount: len(profile.ClaimedUUIDs),
	}
}

func loadEnrichedAuthors(dataPath, commDir string, gClients ...*GravatarClient) ([]AuthorProfile, error) {
	if commDir == "" {
		return []AuthorProfile{}, nil
	}

	entries, err := os.ReadDir(commDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []AuthorProfile{}, nil
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
		if p.AuthorID == "" {
			p.AuthorID = strings.TrimSuffix(entry.Name(), ".json")
		}
		if p.ClaimedUUIDs == nil {
			p.ClaimedUUIDs = []string{}
		}
		if p.GetID() == "" || !isCommunityProfileEnabled(p) {
			continue
		}
		profiles = append(profiles, p)
	}

	if len(profiles) == 0 {
		return []AuthorProfile{}, nil
	}

	return enrichAuthors(dataPath, profiles, gClients...), nil
}

// Deprecated alias
func loadEnrichedArtisans(dataPath, commDir string, gClients ...*GravatarClient) ([]AuthorProfile, error) {
	return loadEnrichedAuthors(dataPath, commDir, gClients...)
}

func loadCensusSummary(dataPath string, opts ...interface{}) (CensusSummary, error) {
	var commDir string
	var gClient *GravatarClient
	for _, opt := range opts {
		switch v := opt.(type) {
		case string:
			if commDir == "" {
				commDir = v
			}
		case *GravatarClient:
			gClient = v
		}
	}
	if commDir == "" {
		if dataPath != "" {
			fi, err := os.Stat(dataPath)
			if err == nil && fi.IsDir() {
				commDir = filepath.Join(dataPath, "community")
			} else {
				commDir = filepath.Join(filepath.Dir(dataPath), "community")
			}
		} else {
			commDir = "/var/lib/penguins-census/community"
		}
	}
	if gClient == nil {
		gClient = defaultGravatarClient
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
					if p.AuthorID == "" {
						p.AuthorID = strings.TrimSuffix(entry.Name(), ".json")
					}
					if p.ClaimedUUIDs == nil {
						p.ClaimedUUIDs = []string{}
					}
					if p.GetID() != "" {
						activeArtisansMap[p.GetID()] = p
					}
				}
			}
		}
	}

	directMachines := make(map[string]map[string]bool)
	for aID, p := range activeArtisansMap {
		directMachines[aID] = make(map[string]bool)
		for _, u := range p.ClaimedUUIDs {
			directMachines[aID][u] = true
		}
	}
	parentMap := make(map[string]string)

	summary := CensusSummary{
		RecentEvents:      make([]RecentEvent, 0),
		RecentSysinstalls: make([]RecentEvent, 0),
		RecentRemasters:   make([]RecentEvent, 0),
		TopInstallations:  make([]TopInstallation, 0),
		ByAction:          make(map[string]int),
		ByDistro:          make(map[string]int),
		ByArch:            make(map[string]int),
		ByDesktop:         make(map[string]int),
		ByDisplayServer:   make(map[string]int),
		ByVirt:            make(map[string]int),
		ByInit:            make(map[string]int),
		ByCountry:         make(map[string]int),
		ByVersion:         make(map[string]int),
	}
	uniqueUUIDs := make(map[string]struct{})
	uniqueSysinstalls := make(map[string]CensusRecord)
	var lastRecords []CensusRecord
	var lastSysinstalls []CensusRecord
	var lastRemasters []CensusRecord

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
		authorID := strings.TrimSpace(rec.AuthorID)
		if authorID == "" {
			authorID = strings.TrimSpace(rec.ArtisanID)
		}
		if authorID != "" {
			if _, ok := activeArtisansMap[authorID]; ok {
				directMachines[authorID][uuid] = true
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
		if rec.Action == "sysinstall" {
			lastSysinstalls = append(lastSysinstalls, rec)
			if len(lastSysinstalls) > 10 {
				lastSysinstalls = lastSysinstalls[1:]
			}
		} else if rec.Action == "remaster" {
			lastRemasters = append(lastRemasters, rec)
			if len(lastRemasters) > 10 {
				lastRemasters = lastRemasters[1:]
			}
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

	toRecentEvent := func(r CensusRecord) RecentEvent {
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
			Clone:         r.Options.Clone,
			Remaster:      r.Action == "remaster",
			Sysinstall:    r.Action == "sysinstall",
		}

		directAuthor, derivedAuthor := resolveAuthor(r, directMachines, parentMap, activeArtisansMap)
		authorID := directAuthor
		if authorID == "" {
			authorID = derivedAuthor
		}
		if authorID != "" {
			dispName, _, profURL := gClient.Resolve(authorID)
			ev.AuthorNickname = dispName
			ev.AuthorURL = profURL
			ev.ArtisanNickname = dispName
			ev.ArtisanURL = profURL
		}
		return ev
	}

	for i := len(lastRecords) - 1; i >= 0; i-- {
		summary.RecentEvents = append(summary.RecentEvents, toRecentEvent(lastRecords[i]))
	}
	for i := len(lastSysinstalls) - 1; i >= 0; i-- {
		summary.RecentSysinstalls = append(summary.RecentSysinstalls, toRecentEvent(lastSysinstalls[i]))
	}
	for i := len(lastRemasters) - 1; i >= 0; i-- {
		summary.RecentRemasters = append(summary.RecentRemasters, toRecentEvent(lastRemasters[i]))
	}

	summary.UniqueMachines = len(uniqueUUIDs)
	summary.TotalCountries = len(summary.ByCountry)
	summary.TotalDistros = len(summary.ByDistro)
	summary.CountryCount = summary.TotalCountries
	summary.DistroCount = summary.TotalDistros

	// Aggregate unique sysinstall installations
	type groupKey struct {
		country        string
		distro         string
		desktop        string
		displayServer  string
		arch           string
		eggsVersion    string
		authorNickname string
		authorURL      string
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

		directAuthor, derivedAuthor := resolveAuthor(r, directMachines, parentMap, activeArtisansMap)
		authorID := directAuthor
		if authorID == "" {
			authorID = derivedAuthor
		}
		authorNick := ""
		authorURL := ""
		if authorID != "" {
			dispName, _, profURL := gClient.Resolve(authorID)
			authorNick = dispName
			authorURL = profURL
		}

		key := groupKey{
			country:        country,
			distro:         distro,
			desktop:        desktop,
			displayServer:  displayServer,
			arch:           arch,
			eggsVersion:    ver,
			authorNickname: authorNick,
			authorURL:      authorURL,
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
			AuthorNickname:  k.authorNickname,
			AuthorURL:       k.authorURL,
			ArtisanNickname: k.authorNickname,
			ArtisanURL:      k.authorURL,
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
		return topList[i].AuthorNickname < topList[j].AuthorNickname
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
		if authors, err := loadEnrichedAuthors(dataPath, commDir); err == nil {
			summary.Authors = authors
			summary.Artisans = authors
		}
	}

	return summary, nil
}


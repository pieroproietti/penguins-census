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
	"regexp"
)

const maxBodyBytes = 16 * 1024 // 16KB

type DistroInfo struct {
	ID       string `json:"id"`
	Like     string `json:"like"`
	Codename string `json:"codename"`
	Release  string `json:"release"`
}

type SystemInfo struct {
	Arch          string `json:"arch"`
	Desktop       string `json:"desktop"`
	DisplayServer string `json:"display_server"`
	Virt          string `json:"virt"`
	Init          string `json:"init"`
}

type OptionsInfo struct {
	Clone   bool `json:"clone"`
	Coexist bool `json:"coexist"`
}

var (
	emailForbiddenPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	uuidSafePattern       = regexp.MustCompile(`^[a-zA-Z0-9_\-]{3,64}$`)
)

type CommunityProfile struct {
	AuthorID     string   `json:"author_id"`
	TokenHash    string   `json:"token_hash"`
	ClaimedUUIDs []string `json:"claimed_uuids"`
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

func (p CommunityProfile) GetID() string {
	return p.AuthorID
}

type AuthorLiveSummary struct {
	DistroName string `json:"distro_name"`
	Version    string `json:"version,omitempty"`
	Desktop    string `json:"desktop,omitempty"`
	Arch       string `json:"arch,omitempty"`
	Count      int    `json:"count"`
	LastSeen   string `json:"last_seen,omitempty"`
}

type AuthorRemasterInfo struct {
	Distro         DistroInfo `json:"distro"`
	System         SystemInfo `json:"system"`
	Country        string     `json:"country,omitempty"`
	Timestamp      string     `json:"timestamp,omitempty"`
	LastRemasterAt string     `json:"last_remaster_at,omitempty"`
	Count          int        `json:"count"`
}

type AuthorProfile struct {
	AuthorID       string              `json:"author_id"`
	DisplayName    string              `json:"display_name"`
	AvatarURL      string              `json:"avatar_url"`
	ProfileURL     string              `json:"profile_url"`
	ClaimedUUIDs   []string            `json:"claimed_uuids,omitempty"`
	Enabled        bool                `json:"enabled"`
	CreatedAt      string              `json:"created_at,omitempty"`
	UpdatedAt      string              `json:"updated_at,omitempty"`
	Country        string              `json:"country,omitempty"`
	LastRemasterAt string              `json:"last_remaster_at,omitempty"`
	MachinesCount  int                 `json:"machines_count"`
	RemasterCount  int                 `json:"remaster_count"`
	InstallsCount  int                 `json:"installs_count"`
	LiveDistros    []AuthorLiveSummary `json:"live_distros,omitempty"`
	LatestRemaster *AuthorRemasterInfo `json:"latest_remaster,omitempty"`
}

func (a AuthorProfile) GetID() string {
	return a.AuthorID
}

type ArtisanLiveSummary = AuthorLiveSummary
type ArtisanRemasterInfo = AuthorRemasterInfo
type ArtisanProfile = AuthorProfile

type rawCommunityPayload struct {
	AuthorID string `json:"author_id"`
	UUID     string `json:"uuid"`
	Email    string `json:"email"`
	Enabled  *bool  `json:"enabled"`
}

type CensusRecord struct {
	Timestamp   string      `json:"timestamp"`
	Country     string      `json:"country"`
	UUID        string      `json:"uuid"`
	ParentID    string      `json:"parent_id,omitempty"`
	AuthorID    string      `json:"author_id,omitempty"`
	ArtisanID   string      `json:"artisan_id,omitempty"` // Backwards compatibility
	EggsVersion string      `json:"eggs_version"`
	Action      string      `json:"action"`
	Distro      DistroInfo  `json:"distro"`
	System      SystemInfo  `json:"system"`
	Options     OptionsInfo `json:"options"`
}

type RecentEvent struct {
	Date            string `json:"date"`
	Country         string `json:"country"`
	Distro          string `json:"distro"`
	Desktop         string `json:"desktop"`
	DisplayServer   string `json:"display_server"`
	EggsVersion     string `json:"eggs_version,omitempty"`
	Clone           bool   `json:"clone"`
	Remaster        bool   `json:"remaster"`
	Sysinstall      bool   `json:"sysinstall"`
	AuthorNickname  string `json:"author_nickname,omitempty"`
	AuthorURL       string `json:"author_url,omitempty"`
	ArtisanNickname string `json:"artisan_nickname,omitempty"`
	ArtisanURL      string `json:"artisan_url,omitempty"`
}

type TopInstallation struct {
	Country         string `json:"country"`
	Distro          string `json:"distro"`
	Desktop         string `json:"desktop"`
	DisplayServer   string `json:"display_server"`
	Arch            string `json:"arch"`
	EggsVersion     string `json:"eggs_version"`
	SumSysinstall   int    `json:"sum_sysinstall"`
	AuthorNickname  string `json:"author_nickname,omitempty"`
	AuthorURL       string `json:"author_url,omitempty"`
	ArtisanNickname string `json:"artisan_nickname,omitempty"`
	ArtisanURL      string `json:"artisan_url,omitempty"`
}

type RepoClientStats struct {
	Hits       int
	Families   map[string]bool
	HasIndex   bool
	HasPackage bool
}

type RepoAnalysisSummary struct {
	GeneratedAt   string         `json:"generated_at,omitempty"`
	TotalLogLines int            `json:"total_log_lines"`
	ValidHits     int            `json:"valid_hits"`
	IgnoredBots   int            `json:"ignored_bots"`
	UniqueIPs     int            `json:"unique_ips"`
	ByFamily      map[string]int `json:"by_family"`
	ByCountry     map[string]int `json:"by_country"`
}

type CensusSummary struct {
	TotalEvents       int                  `json:"total_events"`
	UniqueMachines    int                  `json:"unique_machines"`
	TotalCountries    int                  `json:"total_countries"`
	TotalDistros      int                  `json:"total_distros"`
	CountryCount      int                  `json:"country,omitempty"`
	DistroCount       int                  `json:"distro,omitempty"`
	RemasterClones    int                  `json:"remaster_clones"`
	SysinstallClones  int                  `json:"sysinstall_clones"`
	CoexistCount      int                  `json:"coexist_count"`
	RecentEvents      []RecentEvent        `json:"recent_events"`
	RecentSysinstalls []RecentEvent        `json:"recent_sysinstalls"`
	RecentRemasters   []RecentEvent        `json:"recent_remasters"`
	TopInstallations  []TopInstallation    `json:"top_installations"`
	Authors           []AuthorProfile      `json:"authors,omitempty"`
	Artisans          []AuthorProfile      `json:"artisans,omitempty"` // Compatibility alias
	RepoClients       *RepoAnalysisSummary `json:"repo_clients,omitempty"`
	ByAction          map[string]int       `json:"by_action"`
	ByDistro          map[string]int       `json:"by_distro"`
	ByArch            map[string]int       `json:"by_arch"`
	ByDesktop         map[string]int       `json:"by_desktop"`
	ByDisplayServer   map[string]int       `json:"by_display_server"`
	ByVirt            map[string]int       `json:"by_virt"`
	ByInit           map[string]int       `json:"by_init"`
	ByCountry        map[string]int       `json:"by_country"`
	ByVersion        map[string]int       `json:"by_version"`
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


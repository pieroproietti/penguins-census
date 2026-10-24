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
	ID        string            `json:"id,omitempty"`
	UUID      string            `json:"uuid"`
	AuthType  string            `json:"auth_type,omitempty"`
	AuthID    string            `json:"auth_id,omitempty"`
	TokenHash string            `json:"token_hash,omitempty"`
	Nickname  string            `json:"nickname,omitempty"`
	Location  string            `json:"location,omitempty"`
	Bio       string            `json:"bio,omitempty"`
	Contact   string            `json:"contact,omitempty"`
	Socials   map[string]string `json:"socials,omitempty"`
	SourceURL string            `json:"source_url,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
}

func (p CommunityProfile) GetID() string {
	if p.ID != "" {
		return p.ID
	}
	return p.UUID
}

type ArtisanLiveSummary struct {
	DistroName string `json:"distro_name"`
	Version    string `json:"version,omitempty"`
	Desktop    string `json:"desktop,omitempty"`
	Arch       string `json:"arch,omitempty"`
	Count      int    `json:"count"`
	LastSeen   string `json:"last_seen,omitempty"`
}

type ArtisanRemasterInfo struct {
	Distro         DistroInfo `json:"distro"`
	System         SystemInfo `json:"system"`
	Country        string     `json:"country,omitempty"`
	Timestamp      string     `json:"timestamp,omitempty"`
	LastRemasterAt string     `json:"last_remaster_at,omitempty"`
	Count          int        `json:"count"`
}

type ArtisanProfile struct {
	ID             string               `json:"id,omitempty"`
	UUID           string               `json:"uuid"`
	AuthType       string               `json:"auth_type,omitempty"`
	Nickname       string               `json:"nickname,omitempty"`
	Location       string               `json:"location,omitempty"`
	Bio            string               `json:"bio,omitempty"`
	Contact        string               `json:"contact,omitempty"`
	Socials        map[string]string    `json:"socials,omitempty"`
	SourceURL      string               `json:"source_url,omitempty"`
	Enabled        *bool                `json:"enabled,omitempty"`
	UpdatedAt      string               `json:"updated_at,omitempty"`
	Country        string               `json:"country,omitempty"`
	LastRemasterAt string               `json:"last_remaster_at,omitempty"`
	MachinesCount  int                  `json:"machines_count"`
	RemasterCount  int                  `json:"remaster_count"`
	InstallsCount  int                  `json:"installs_count"`
	LiveDistros    []ArtisanLiveSummary `json:"live_distros,omitempty"`
	LatestRemaster *ArtisanRemasterInfo `json:"latest_remaster,omitempty"`
}

func (a ArtisanProfile) GetID() string {
	if a.ID != "" {
		return a.ID
	}
	return a.UUID
}

type SocialLink struct {
	Platform string
	Label    string
	URL      string
}

type rawCommunityPayload struct {
	ID            string               `json:"id"`
	UUID          string               `json:"uuid"`
	AuthType      string               `json:"auth_type"`
	AuthID        string               `json:"auth_id"`
	TokenHash     string               `json:"token_hash"`
	Token         string               `json:"token"`
	Nickname      string               `json:"nickname"`
	Location      string               `json:"location"`
	Bio           string               `json:"bio"`
	Contact       string               `json:"contact"`
	Social        interface{}          `json:"social"`
	Socials       map[string]string    `json:"socials"`
	SourceURL     string               `json:"source_url"`
	URL           string               `json:"url"`
	Website       string               `json:"website"`
	RepositoryURL string               `json:"repository_url"`
	RepoURL       string               `json:"repo_url"`
	Enabled       *bool                `json:"enabled"`
	Community     *rawCommunityPayload `json:"community"`
}

type CensusRecord struct {
	Timestamp   string      `json:"timestamp"`
	Country     string      `json:"country"`
	UUID        string      `json:"uuid"`
	ParentID    string      `json:"parent_id,omitempty"`
	ArtisanID   string      `json:"artisan_id,omitempty"`
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
	Remaster        bool   `json:"remaster"`
	Sysinstall      bool   `json:"sysinstall"`
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
	TotalEvents      int                  `json:"total_events"`
	UniqueMachines   int                  `json:"unique_machines"`
	TotalCountries   int                  `json:"total_countries"`
	TotalDistros     int                  `json:"total_distros"`
	CountryCount     int                  `json:"country,omitempty"`
	DistroCount      int                  `json:"distro,omitempty"`
	RemasterClones   int                  `json:"remaster_clones"`
	SysinstallClones int                  `json:"sysinstall_clones"`
	CoexistCount     int                  `json:"coexist_count"`
	RecentEvents     []RecentEvent        `json:"recent_events"`
	TopInstallations []TopInstallation    `json:"top_installations"`
	Artisans         []ArtisanProfile     `json:"artisans,omitempty"`
	RepoClients      *RepoAnalysisSummary `json:"repo_clients,omitempty"`
	ByAction         map[string]int       `json:"by_action"`
	ByDistro         map[string]int       `json:"by_distro"`
	ByArch           map[string]int       `json:"by_arch"`
	ByDesktop        map[string]int       `json:"by_desktop"`
	ByDisplayServer  map[string]int       `json:"by_display_server"`
	ByVirt           map[string]int       `json:"by_virt"`
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


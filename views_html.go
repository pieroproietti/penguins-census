package main

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed templates/style.css
var embeddedCSS string

var (
	summaryTmpl   = template.Must(template.ParseFS(templateFS, "templates/summary.html"))
	communityTmpl = template.Must(template.ParseFS(templateFS, "templates/community.html"))
)

func countryToFlag(countryCode string) string {
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	if countryCode == "XX" || countryCode == "" {
		return ""
	}
	if len(countryCode) == 2 && countryCode[0] >= 'A' && countryCode[0] <= 'Z' && countryCode[1] >= 'A' && countryCode[1] <= 'Z' {
		r1 := rune(0x1F1E6 + int(countryCode[0]-'A'))
		r2 := rune(0x1F1E6 + int(countryCode[1]-'A'))
		return string([]rune{r1, r2})
	}
	return ""
}

func isSafeURL(u string) bool {
	u = strings.TrimSpace(u)
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func formatDistroBadge(distro DistroInfo, sys SystemInfo) string {
	id := strings.TrimSpace(distro.ID)
	codename := strings.TrimSpace(distro.Codename)
	release := strings.TrimSpace(distro.Release)
	arch := strings.TrimSpace(sys.Arch)
	desktop := strings.TrimSpace(sys.Desktop)

	if id == "" {
		id = "linux"
	}

	var versionParts []string
	if codename != "" && release != "" {
		if strings.EqualFold(codename, release) {
			versionParts = append(versionParts, codename)
		} else {
			versionParts = append(versionParts, fmt.Sprintf("%s (%s)", codename, release))
		}
	} else if codename != "" {
		versionParts = append(versionParts, codename)
	} else if release != "" {
		versionParts = append(versionParts, release)
	}

	distroStr := id
	if len(versionParts) > 0 {
		distroStr += " " + strings.Join(versionParts, " ")
	}

	var parts []string
	parts = append(parts, distroStr)

	if arch != "" {
		parts = append(parts, arch)
	}
	if desktop != "" {
		parts = append(parts, desktop)
	} else {
		parts = append(parts, "headless/cli")
	}

	return strings.Join(parts, " • ")
}

func buildSocialLinks(socials map[string]string, contact string) []SocialLink {
	var links []SocialLink
	seenURLs := make(map[string]bool)

	keys := make([]string, 0, len(socials))
	for k := range socials {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		val := strings.TrimSpace(socials[k])
		if val == "" {
			continue
		}
		platform := strings.ToLower(strings.TrimSpace(k))
		var url string
		var label string

		switch platform {
		case "github":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://github.com/" + handle
			}
			label = "GitHub"
		case "gitlab":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://gitlab.com/" + handle
			}
			label = "GitLab"
		case "mastodon":
			if isSafeURL(val) {
				url = val
				parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(val, "https://"), "http://"), "/")
				if len(parts) >= 2 {
					label = parts[len(parts)-1] + "@" + parts[0]
				} else {
					label = "Mastodon"
				}
			} else {
				handle := val
				parts := strings.Split(strings.TrimPrefix(handle, "@"), "@")
				if len(parts) == 2 {
					url = fmt.Sprintf("https://%s/@%s", parts[1], parts[0])
					label = "@" + parts[0] + "@" + parts[1]
				} else {
					url = "https://mastodon.social/@" + strings.TrimPrefix(handle, "@")
					label = "Mastodon"
				}
			}
		case "telegram":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://t.me/" + handle
			}
			label = "Telegram"
		case "matrix":
			if isSafeURL(val) {
				url = val
			} else {
				url = "https://matrix.to/#/" + val
			}
			label = "Matrix"
		case "twitter", "x":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://x.com/" + handle
			}
			label = "X / Twitter"
		case "website", "web", "site", "blog":
			if isSafeURL(val) {
				url = val
				label = "Website"
			}
		default:
			if isSafeURL(val) {
				url = val
				label = strings.Title(platform)
			}
		}

		if url != "" && !seenURLs[url] {
			seenURLs[url] = true
			links = append(links, SocialLink{
				Platform: platform,
				Label:    label,
				URL:      url,
			})
		}
	}

	contact = strings.TrimSpace(contact)
	if contact != "" {
		if strings.HasPrefix(contact, "http://") || strings.HasPrefix(contact, "https://") {
			if !seenURLs[contact] {
				seenURLs[contact] = true
				links = append(links, SocialLink{
					Platform: "contact",
					Label:    "Contact",
					URL:      contact,
				})
			}
		} else if strings.HasPrefix(contact, "mailto:") {
			// skip mailto
		} else if strings.Contains(contact, "@") && !strings.Contains(contact, "/") {
			// email address, omit from clickable social links
		} else if !seenURLs[contact] && isSafeURL(contact) {
			seenURLs[contact] = true
			links = append(links, SocialLink{
				Platform: "contact",
				Label:    "Contact",
				URL:      contact,
			})
		}
	}

	return links
}

func sortedRows(m map[string]int, total int) []TableRow {
	rows := make([]TableRow, 0, len(m))
	for k, count := range m {
		var pct float64
		if total > 0 {
			pct = (float64(count) / float64(total)) * 100.0
		}
		rows = append(rows, TableRow{
			Key:     k,
			Count:   count,
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

func tokenizeVersion(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	var tokens []string
	var cur strings.Builder
	lastDigit := false
	for i, r := range v {
		if r == '.' || r == '-' || r == '_' || r == '+' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		isDigit := r >= '0' && r <= '9'
		if i > 0 && cur.Len() > 0 && isDigit != lastDigit {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
		lastDigit = isDigit
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func compareVersions(a, b string) int {
	ta := tokenizeVersion(a)
	tb := tokenizeVersion(b)
	maxLen := len(ta)
	if len(tb) > maxLen {
		maxLen = len(tb)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(ta) {
			return -1
		}
		if i >= len(tb) {
			return 1
		}
		na, errA := strconv.Atoi(ta[i])
		nb, errB := strconv.Atoi(tb[i])
		if errA == nil && errB == nil {
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
		} else {
			if ta[i] != tb[i] {
				if ta[i] < tb[i] {
					return -1
				}
				return 1
			}
		}
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func sortedVersionRows(m map[string]int, total int) []TableRow {
	rows := make([]TableRow, 0, len(m))
	for k, count := range m {
		var pct float64
		if total > 0 {
			pct = (float64(count) / float64(total)) * 100.0
		}
		rows = append(rows, TableRow{
			Key:     k,
			Count:   count,
			Percent: pct,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return compareVersions(rows[i].Key, rows[j].Key) > 0
	})
	return rows
}

func formatEventDate(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return "-"
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return ts
}

func formatTimeAgo(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" || ts == "-" {
		return "-"
	}
	t := parseFlexibleTimestamp(ts)
	if t.IsZero() {
		return ts
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		secs := int(d.Seconds())
		if secs <= 5 {
			return "just now"
		}
		return fmt.Sprintf("%ds ago", secs)
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	if d < 30*24*time.Hour {
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	if d < 365*24*time.Hour {
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	}
	return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
}

func pluralRemasters(count int) string {
	if count == 1 {
		return "build"
	}
	return "builds"
}

func locDisplayHTML(locDisplay string) string {
	if locDisplay == "" {
		return ""
	}
	return fmt.Sprintf(`<div class="artisan-loc">%s</div>`, locDisplay)
}

type HTMLTableCard struct {
	ID      string
	Title   string
	ColName string
	Aliases []string
	Rows    []TableRow
}

type HTMLArtisanSummaryItem struct {
	Nickname           string
	Link               template.URL
	LocationDisplay    string
	ISOBadge           string
	MachinesCount      int
	RemasterCount      int
	InstallsCount      int
	LiveDistrosSummary string
	SourceURL          string
}

type HTMLRecentEventItem struct {
	Date            string
	Country         string
	Distro          string
	ArtisanNickname string
	ArtisanURL      template.URL
	Desktop         string
	DisplayServer   string
	EggsVersion     string
	Remaster        bool
	Sysinstall      bool
}

type HTMLTopInstallationItem struct {
	Country         string
	Distro          string
	ArtisanNickname string
	ArtisanURL      template.URL
	Desktop         string
	DisplayServer   string
	Arch            string
	EggsVersion     string
	SumSysinstall   int
}

type HTMLRepoClientsView struct {
	GeneratedAt    string
	UniqueIPs      int
	CensusMachines int
	CompletedOps   int
	Tables         []HTMLTableCard
}

type HTMLSummaryView struct {
	CSS              template.CSS
	TotalEvents      int
	UniqueMachines   int
	RemasterCount    int
	RemasterClones   int
	SysinstallCount  int
	SysinstallClones int
	Tables           []HTMLTableCard
	Artisans         []HTMLArtisanSummaryItem
	TopInstallations []HTMLTopInstallationItem
	RecentEvents     []HTMLRecentEventItem
	RepoClients      *HTMLRepoClientsView
}

type HTMLLiveDistroItem struct {
	Badge    string
	Count    int
	LastSeen string
}

type HTMLArtisanCard struct {
	ID                string
	UUID              string
	AuthType          string
	IsGitHub          bool
	Nickname          string
	CountryBadge      string
	LocationDisplay   string
	Bio               string
	HasLatestRemaster bool
	MachinesCount     int
	RemasterCount     int
	InstallsCount     int
	PluralBuilds      string
	ISOBadge          string
	LastBuildDate     string
	LiveDistros       []HTMLLiveDistroItem
	SourceURL         template.URL
	Socials           []SocialLink
}

type HTMLCommunityView struct {
	CSS               template.CSS
	GitHubAuthEnabled bool
	TotalArtisans     int
	TotalRemasters    int
	TotalInstalls     int
	TotalCountries    int
	Artisans          []HTMLArtisanCard
}

func renderHTMLSummary(s CensusSummary) string {
	tables := []HTMLTableCard{
		{ID: "country", Title: "Country", ColName: "Country", Rows: sortedRows(s.ByCountry, s.TotalEvents)},
		{ID: "distro", Title: "Distro", ColName: "Distro", Rows: sortedRows(s.ByDistro, s.TotalEvents)},
		{ID: "desktop", Title: "Desktop", ColName: "Desktop", Rows: sortedRows(s.ByDesktop, s.TotalEvents)},
		{ID: "display-server", Title: "Display Server", ColName: "Display Server", Aliases: []string{"display_server"}, Rows: sortedRows(s.ByDisplayServer, s.TotalEvents)},
		{ID: "virtualization", Title: "Virtualization", ColName: "Virt", Aliases: []string{"virt"}, Rows: sortedRows(s.ByVirt, s.TotalEvents)},
		{ID: "init-system", Title: "Init System", ColName: "Init", Aliases: []string{"init"}, Rows: sortedRows(s.ByInit, s.TotalEvents)},
		{ID: "arch", Title: "Arch", ColName: "Arch", Rows: sortedRows(s.ByArch, s.TotalEvents)},
		{ID: "version", Title: "Version", ColName: "Version", Aliases: []string{"eggs-version"}, Rows: sortedVersionRows(s.ByVersion, s.TotalEvents)},
	}

	artisanItems := make([]HTMLArtisanSummaryItem, 0, len(s.Artisans))
	for _, a := range s.Artisans {
		nick := strings.TrimSpace(a.Nickname)
		if nick == "" {
			nick = "artisan"
		}
		if !strings.HasPrefix(nick, "@") {
			nick = "@" + nick
		}

		country := strings.ToUpper(strings.TrimSpace(a.Country))
		flag := countryToFlag(country)
		loc := strings.TrimSpace(a.Location)
		locDisplay := ""
		if loc != "" && flag != "" {
			locDisplay = fmt.Sprintf("%s %s", loc, flag)
		} else if loc != "" {
			locDisplay = loc
		} else if flag != "" {
			locDisplay = fmt.Sprintf("%s %s", flag, country)
		} else if country != "" {
			locDisplay = country
		}

		isoBadge := ""
		if a.LatestRemaster != nil {
			isoBadge = formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
		}

		sourceLink := ""
		if isSafeURL(a.SourceURL) {
			sourceLink = a.SourceURL
		}

		artisanLink := "/api/community"
		if isSafeURL(a.SourceURL) {
			artisanLink = a.SourceURL
		}

		liveDistrosSummary := ""
		if len(a.LiveDistros) > 1 {
			var distros []string
			for _, ld := range a.LiveDistros {
				distros = append(distros, fmt.Sprintf("%s (%d)", ld.DistroName, ld.Count))
			}
			liveDistrosSummary = strings.Join(distros, ", ")
		}

		artisanItems = append(artisanItems, HTMLArtisanSummaryItem{
			Nickname:           nick,
			Link:               template.URL(artisanLink),
			LocationDisplay:    locDisplay,
			ISOBadge:           isoBadge,
			MachinesCount:      a.MachinesCount,
			RemasterCount:      a.RemasterCount,
			InstallsCount:      a.InstallsCount,
			LiveDistrosSummary: liveDistrosSummary,
			SourceURL:          sourceLink,
		})
	}

	recentItems := make([]HTMLRecentEventItem, 0, len(s.RecentEvents))
	for _, e := range s.RecentEvents {
		artisanNick := ""
		artisanURL := ""
		if e.ArtisanNickname != "" {
			artisanNick = e.ArtisanNickname
			if !strings.HasPrefix(artisanNick, "@") {
				artisanNick = "@" + artisanNick
			}
			artisanURL = e.ArtisanURL
			if !isSafeURL(artisanURL) {
				artisanURL = "/api/community"
			}
		}
		date := formatTimeAgo(e.Date)
		recentItems = append(recentItems, HTMLRecentEventItem{
			Date:            date,
			Country:         e.Country,
			Distro:          e.Distro,
			ArtisanNickname: artisanNick,
			ArtisanURL:      template.URL(artisanURL),
			Desktop:         e.Desktop,
			DisplayServer:   e.DisplayServer,
			EggsVersion:     e.EggsVersion,
			Remaster:        e.Remaster,
			Sysinstall:      e.Sysinstall,
		})
	}

	topItems := make([]HTMLTopInstallationItem, 0, len(s.TopInstallations))
	for _, ti := range s.TopInstallations {
		artisanNick := ""
		artisanURL := ""
		if ti.ArtisanNickname != "" {
			artisanNick = ti.ArtisanNickname
			if !strings.HasPrefix(artisanNick, "@") {
				artisanNick = "@" + artisanNick
			}
			artisanURL = ti.ArtisanURL
			if !isSafeURL(artisanURL) {
				artisanURL = "/api/community"
			}
		}
		topItems = append(topItems, HTMLTopInstallationItem{
			Country:         ti.Country,
			Distro:          ti.Distro,
			ArtisanNickname: artisanNick,
			ArtisanURL:      template.URL(artisanURL),
			Desktop:         ti.Desktop,
			DisplayServer:   ti.DisplayServer,
			Arch:            ti.Arch,
			EggsVersion:     ti.EggsVersion,
			SumSysinstall:   ti.SumSysinstall,
		})
	}

	var repoView *HTMLRepoClientsView
	if s.RepoClients != nil && s.RepoClients.UniqueIPs > 0 {
		totalOps := s.ByAction["remaster"] + s.ByAction["sysinstall"]
		repoView = &HTMLRepoClientsView{
			GeneratedAt:    s.RepoClients.GeneratedAt,
			UniqueIPs:      s.RepoClients.UniqueIPs,
			CensusMachines: s.UniqueMachines,
			CompletedOps:   totalOps,
			Tables: []HTMLTableCard{
				{ID: "repo-families", Title: "Package Managers", ColName: "Family", Rows: sortedRows(s.RepoClients.ByFamily, s.RepoClients.UniqueIPs)},
				{ID: "repo-countries", Title: "Repo Clients by Country", ColName: "Country", Rows: sortedRows(s.RepoClients.ByCountry, s.RepoClients.UniqueIPs)},
			},
		}
	}

	data := HTMLSummaryView{
		CSS:              template.CSS(embeddedCSS),
		TotalEvents:      s.TotalEvents,
		UniqueMachines:   s.UniqueMachines,
		RemasterCount:    s.ByAction["remaster"],
		RemasterClones:   s.RemasterClones,
		SysinstallCount:  s.ByAction["sysinstall"],
		SysinstallClones: s.SysinstallClones,
		Tables:           tables,
		Artisans:         artisanItems,
		TopInstallations: topItems,
		RecentEvents:     recentItems,
		RepoClients:      repoView,
	}

	var buf bytes.Buffer
	if err := summaryTmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("<!-- template error: %v -->", err)
	}
	return buf.String()
}

func renderCommunityHTML(artisans []ArtisanProfile, ghAuthEnabled ...bool) string {
	ghEnabled := false
	if len(ghAuthEnabled) > 0 {
		ghEnabled = ghAuthEnabled[0]
	}

	totalRemasters := 0
	totalInstalls := 0
	countriesMap := make(map[string]bool)
	cardItems := make([]HTMLArtisanCard, 0, len(artisans))

	for _, a := range artisans {
		totalRemasters += a.RemasterCount
		totalInstalls += a.InstallsCount
		country := strings.ToUpper(strings.TrimSpace(a.Country))
		if country != "" && country != "XX" {
			countriesMap[country] = true
		}

		nick := strings.TrimSpace(a.Nickname)
		if nick == "" {
			nick = a.GetID()
			if len(nick) > 8 {
				nick = nick[:8]
			}
		}
		if !strings.HasPrefix(nick, "@") {
			nick = "@" + nick
		}

		flag := countryToFlag(country)
		countryBadge := ""
		if country != "" && country != "XX" {
			if flag != "" {
				countryBadge = fmt.Sprintf("%s %s", flag, country)
			} else {
				countryBadge = country
			}
		}

		loc := strings.TrimSpace(a.Location)
		locDisplay := ""
		if loc != "" && flag != "" {
			locDisplay = fmt.Sprintf("📍 %s %s", loc, flag)
		} else if loc != "" {
			locDisplay = fmt.Sprintf("📍 %s", loc)
		} else if flag != "" {
			locDisplay = fmt.Sprintf("📍 %s %s", flag, country)
		}

		hasLatest := a.LatestRemaster != nil
		isoBadge := ""
		lastBuildDate := ""
		if hasLatest {
			isoBadge = formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
			lastBuildDate = formatEventDate(a.LastRemasterAt)
		}

		sourceURL := ""
		if isSafeURL(a.SourceURL) {
			sourceURL = a.SourceURL
		}

		var safeSocials []SocialLink
		for _, sl := range buildSocialLinks(a.Socials, a.Contact) {
			if isSafeURL(sl.URL) {
				safeSocials = append(safeSocials, sl)
			}
		}

		liveItems := make([]HTMLLiveDistroItem, 0, len(a.LiveDistros))
		for _, ld := range a.LiveDistros {
			badge := formatDistroBadge(DistroInfo{ID: ld.DistroName, Release: ld.Version}, SystemInfo{Arch: ld.Arch, Desktop: ld.Desktop})
			liveItems = append(liveItems, HTMLLiveDistroItem{
				Badge:    badge,
				Count:    ld.Count,
				LastSeen: formatEventDate(ld.LastSeen),
			})
		}

		cardItems = append(cardItems, HTMLArtisanCard{
			ID:                a.GetID(),
			UUID:              a.UUID,
			AuthType:          a.AuthType,
			IsGitHub:          strings.EqualFold(a.AuthType, "github"),
			Nickname:          nick,
			CountryBadge:      countryBadge,
			LocationDisplay:   locDisplay,
			Bio:               strings.TrimSpace(a.Bio),
			HasLatestRemaster: hasLatest,
			MachinesCount:     a.MachinesCount,
			RemasterCount:     a.RemasterCount,
			InstallsCount:     a.InstallsCount,
			PluralBuilds:      pluralRemasters(a.RemasterCount),
			ISOBadge:          isoBadge,
			LastBuildDate:     lastBuildDate,
			LiveDistros:       liveItems,
			SourceURL:         template.URL(sourceURL),
			Socials:           safeSocials,
		})
	}

	data := HTMLCommunityView{
		CSS:               template.CSS(embeddedCSS),
		GitHubAuthEnabled: ghEnabled,
		TotalArtisans:     len(artisans),
		TotalRemasters:    totalRemasters,
		TotalInstalls:     totalInstalls,
		TotalCountries:    len(countriesMap),
		Artisans:          cardItems,
	}

	var buf bytes.Buffer
	if err := communityTmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("<!-- template error: %v -->", err)
	}
	return buf.String()
}

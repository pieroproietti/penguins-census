package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"time"
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
		case "telegram", "tg":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://t.me/" + handle
			}
			label = "Telegram"
		case "mastodon":
			if isSafeURL(val) {
				url = val
			} else if strings.HasPrefix(val, "@") && strings.Contains(val[1:], "@") {
				parts := strings.Split(val[1:], "@")
				url = "https://" + parts[1] + "/@" + parts[0]
			}
			label = "Mastodon"
		case "twitter", "x":
			if isSafeURL(val) {
				url = val
			} else {
				handle := strings.TrimPrefix(val, "@")
				url = "https://x.com/" + handle
			}
			label = "X (Twitter)"
		case "matrix":
			if isSafeURL(val) {
				url = val
			} else {
				url = "https://matrix.to/#/" + val
			}
			label = "Matrix"
		case "sourceforge":
			if isSafeURL(val) {
				url = val
			} else {
				url = "https://sourceforge.net/projects/" + val
			}
			label = "SourceForge"
		default:
			if isSafeURL(val) {
				url = val
				label = strings.Title(platform)
			}
		}

		if url != "" && isSafeURL(url) && !seenURLs[url] {
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
		if isSafeURL(contact) && !seenURLs[contact] {
			seenURLs[contact] = true
			links = append(links, SocialLink{
				Platform: "contact",
				Label:    "Contact",
				URL:      contact,
			})
		} else if strings.HasPrefix(contact, "@") && !strings.Contains(contact[1:], "@") {
			tgURL := "https://t.me/" + strings.TrimPrefix(contact, "@")
			if !seenURLs[tgURL] {
				seenURLs[tgURL] = true
				links = append(links, SocialLink{
					Platform: "telegram",
					Label:    "Telegram (" + contact + ")",
					URL:      tgURL,
				})
			}
		}
	}

	return links
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
	fmt.Fprintf(&buf, "- Total Events:        %d\n", s.TotalEvents)
	fmt.Fprintf(&buf, "- Unique Machines:     %d\n", s.UniqueMachines)
	fmt.Fprintf(&buf, "- Remasters/Clones:    %d/%d\n", s.ByAction["remaster"], s.RemasterClones)
	fmt.Fprintf(&buf, "- Sysinstalls/Clones:  %d/%d\n\n", s.ByAction["sysinstall"], s.SysinstallClones)

	writeMarkdownTable(&buf, "Country", "Country", s.ByCountry, s.TotalEvents)
	writeMarkdownTable(&buf, "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeMarkdownTable(&buf, "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeMarkdownTable(&buf, "Display Server", "Display Server", s.ByDisplayServer, s.TotalEvents)
	writeMarkdownTable(&buf, "Virt", "Virt", s.ByVirt, s.TotalEvents)
	writeMarkdownTable(&buf, "Init", "Init", s.ByInit, s.TotalEvents)
	writeMarkdownTable(&buf, "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeMarkdownTable(&buf, "Version", "Version", s.ByVersion, s.TotalEvents)

	writeMarkdownTopInstallations(&buf, s.TopInstallations)
	writeMarkdownRecentEvents(&buf, s.RecentEvents)

	if s.RepoClients != nil && s.RepoClients.UniqueIPs > 0 {
		totalOps := s.ByAction["remaster"] + s.ByAction["sysinstall"]
		writeMarkdownRepoClients(&buf, *s.RepoClients, s.UniqueMachines, totalOps)
	}

	return buf.String()
}

func writeMarkdownRepoClients(buf *bytes.Buffer, r RepoAnalysisSummary, censusMachines, completedOps int) {
	fmt.Fprintf(buf, "### Unique IP last 15 days\n\n")
	if r.GeneratedAt != "" {
		fmt.Fprintf(buf, "- Updated:                 %s\n", r.GeneratedAt)
	}
	fmt.Fprintf(buf, "- Unique IPs (15 days):    %d\n", r.UniqueIPs)
	fmt.Fprintf(buf, "- Census Active Machines:  %d\n", censusMachines)
	fmt.Fprintf(buf, "- Completed Operations:    %d\n\n", completedOps)

	writeMarkdownTable(buf, "Package Managers", "Family", r.ByFamily, r.UniqueIPs)
	writeMarkdownTable(buf, "Repo Clients by Country", "Country", r.ByCountry, r.UniqueIPs)
}

func writeMarkdownTopInstallations(buf *bytes.Buffer, list []TopInstallation) {
	fmt.Fprintf(buf, "### Top 10 Sysinstalls\n\n")

	countryW := len("Country")
	distroW := len("Distro")
	desktopW := len("Desktop")
	dispW := len("Display Server")
	archW := len("Arch")
	verW := len("Version")
	sumW := len("Sysinstalls")

	if len(list) == 0 {
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*s |\n",
			countryW, "Country",
			distroW, "Distro",
			desktopW, "Desktop",
			dispW, "Display Server",
			archW, "Arch",
			verW, "Version",
			sumW, "Sysinstalls")
		fmt.Fprintf(buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s:|\n",
			strings.Repeat("-", countryW),
			strings.Repeat("-", distroW),
			strings.Repeat("-", desktopW),
			strings.Repeat("-", dispW),
			strings.Repeat("-", archW),
			strings.Repeat("-", verW),
			strings.Repeat("-", sumW-1))
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*d |\n\n",
			countryW, "(no data)",
			distroW, "-",
			desktopW, "-",
			dispW, "-",
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
		if len(item.DisplayServer) > dispW {
			dispW = len(item.DisplayServer)
		}
		if len(item.Arch) > archW {
			archW = len(item.Arch)
		}
		if len(item.EggsVersion) > verW {
			verW = len(item.EggsVersion)
		}
	}

	fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*s |\n",
		countryW, "Country",
		distroW, "Distro",
		desktopW, "Desktop",
		dispW, "Display Server",
		archW, "Arch",
		verW, "Version",
		sumW, "Sysinstalls")
	fmt.Fprintf(buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s:|\n",
		strings.Repeat("-", countryW),
		strings.Repeat("-", distroW),
		strings.Repeat("-", desktopW),
		strings.Repeat("-", dispW),
		strings.Repeat("-", archW),
		strings.Repeat("-", verW),
		strings.Repeat("-", sumW-1))

	for _, item := range list {
		fmt.Fprintf(buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %*d |\n",
			countryW, item.Country,
			distroW, item.Distro,
			desktopW, item.Desktop,
			dispW, item.DisplayServer,
			archW, item.Arch,
			verW, item.EggsVersion,
			sumW, item.SumSysinstall)
	}
	buf.WriteString("\n")
}

func formatEventDate(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return "-"
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return ts
}

func writeMarkdownRecentEvents(buf *bytes.Buffer, events []RecentEvent) {
	fmt.Fprintf(buf, "### Recent Events\n\n")
	fmt.Fprintf(buf, "| Date                | Country | Distro | Desktop | Display Server | Remaster | Sysinstall |\n")
	fmt.Fprintf(buf, "|---------------------|---------|--------|---------|----------------|:--------:|:----------:|\n")
	if len(events) == 0 {
		fmt.Fprintf(buf, "| (no data)           | -       | -      | -       | -              | -        | -          |\n\n")
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
		date := ev.Date
		if date == "" {
			date = "-"
		}
		fmt.Fprintf(buf, "| %-19s | %-7s | %-6s | %-7s | %-14s | %-8s | %-10s |\n", date, ev.Country, ev.Distro, ev.Desktop, ev.DisplayServer, rem, sys)
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
          <th>Date</th>
          <th>Country</th>
          <th>Distro</th>
          <th>Desktop</th>
          <th>Display Server</th>
          <th class="center">Remaster</th>
          <th class="center">Sysinstall</th>
        </tr>
      </thead>
      <tbody>
`)
	if len(events) == 0 {
		buf.WriteString(`        <tr><td colspan="7" class="empty">No events recorded yet</td></tr>
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
			date := html.EscapeString(e.Date)
			if date == "" {
				date = `<span class="muted">&mdash;</span>`
			}
			distroCell := html.EscapeString(e.Distro)
			if e.ArtisanNickname != "" {
				nick := e.ArtisanNickname
				if !strings.HasPrefix(nick, "@") {
					nick = "@" + nick
				}
				artisanURL := e.ArtisanURL
				if !isSafeURL(artisanURL) {
					artisanURL = "/api/community"
				}
				distroCell += fmt.Sprintf(` <a href="%s" class="artisan-badge" target="_blank" rel="noopener noreferrer" title="Distro Artisan">%s</a>`, html.EscapeString(artisanURL), html.EscapeString(nick))
			}
			fmt.Fprintf(buf, `        <tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="center">%s</td><td class="center">%s</td></tr>
`, date, html.EscapeString(e.Country), distroCell, html.EscapeString(e.Desktop), html.EscapeString(e.DisplayServer), rem, sys)
		}
	}
	buf.WriteString(`      </tbody>
    </table>
  </section>
`)
}

func writeHTMLCommunityArtisans(buf *bytes.Buffer, artisans []ArtisanProfile) {
	buf.WriteString(`  <section class="panel" id="community-artisans">
    <span id="community" class="anchor-alias"></span>
    <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:1rem; flex-wrap:wrap; gap:0.5rem;">
      <h2 style="margin:0;"><a href="/api/community" class="anchor-link">🐧 Penguins Community Artisans</a></h2>
      <a class="btn" href="/api/community">View Showcase &rarr;</a>
    </div>
`)
	if len(artisans) == 0 {
		buf.WriteString(`    <div class="empty">No community artisans registered yet &bull; <a href="/api/community" style="color:var(--accent);">Join Penguins Community</a></div>
  </section>
`)
		return
	}

	buf.WriteString(`    <table>
      <thead>
        <tr>
          <th>Artisan</th>
          <th>Location</th>
          <th>Latest ISO</th>
          <th class="num">Remasters</th>
          <th>Source</th>
        </tr>
      </thead>
      <tbody>
`)
	for _, a := range artisans {
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
			locDisplay = fmt.Sprintf("%s %s", html.EscapeString(loc), flag)
		} else if loc != "" {
			locDisplay = html.EscapeString(loc)
		} else if flag != "" {
			locDisplay = fmt.Sprintf("%s %s", flag, html.EscapeString(country))
		} else if country != "" {
			locDisplay = html.EscapeString(country)
		} else {
			locDisplay = `<span class="muted">&mdash;</span>`
		}

		isoBadge := `<span class="muted">&mdash;</span>`
		if a.LatestRemaster != nil {
			badgeText := formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
			isoBadge = fmt.Sprintf(`<span class="iso-badge" style="font-size:0.8rem; padding:0.15rem 0.45rem;">%s</span>`, html.EscapeString(badgeText))
		}

		sourceLink := `<span class="muted">&mdash;</span>`
		if isSafeURL(a.SourceURL) {
			sourceLink = fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer" class="btn" style="padding:0.2rem 0.5rem; font-size:0.75rem;">Source &rarr;</a>`, html.EscapeString(a.SourceURL))
		}

		artisanLink := "/api/community"
		if isSafeURL(a.SourceURL) {
			artisanLink = a.SourceURL
		}

		fmt.Fprintf(buf, `        <tr>
          <td><a href="%s" target="_blank" rel="noopener noreferrer" style="color:var(--accent); font-weight:600; text-decoration:none;">%s</a></td>
          <td>%s</td>
          <td>%s</td>
          <td class="num">%d</td>
          <td>%s</td>
        </tr>
`, html.EscapeString(artisanLink), html.EscapeString(nick), locDisplay, isoBadge, a.RemasterCount, sourceLink)
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
.artisan-badge {
  display: inline-block;
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.4);
  padding: 0.15rem 0.45rem;
  border-radius: 9999px;
  font-size: 0.75rem;
  font-weight: 600;
  text-decoration: none;
  margin-left: 0.4rem;
  transition: all 0.2s;
  vertical-align: middle;
}
.artisan-badge:hover {
  background: var(--accent);
  color: #0f172a;
}
.iso-badge {
  display: inline-block;
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.4);
  padding: 0.2rem 0.45rem;
  border-radius: 6px;
  font-size: 0.8rem;
  font-family: monospace;
  font-weight: 600;
}
</style>
</head>
<body>
<div class="container">
  <header>
    <div>
      <h1>Penguins Eggs Worldwide Usage</h1>
      <div class="subtitle">Since 22 October 2026 &bull; Privacy-respecting aggregated telemetry (no IP addresses stored)</div>
    </div>
    <div class="links">
      <a class="btn" href="/api/community">🐧 Distro Artisans</a>
      <a class="btn" href="https://penguins-eggs.net" target="_blank" rel="noopener">Home</a>
      <a class="btn" href="https://penguins-eggs.net/access/" target="_blank" rel="noopener">Site</a>
      <a class="btn" href="https://github.com/pieroproietti/penguins-eggs" target="_blank" rel="noopener">Repository</a>
`)
	if s.RepoClients != nil && s.RepoClients.UniqueIPs > 0 {
		buf.WriteString(`      <a class="btn" href="#unique-ips">Unique IPs</a>
`)
	}
	buf.WriteString(`      <a class="btn" href="?format=txt">Plain Text / Markdown</a>
      <a class="btn" href="?format=json">Raw JSON</a>
    </div>
  </header>
`)

	fmt.Fprintf(&buf, `  <div class="cards" id="overview">
    <div class="card" id="total-events"><div class="card-label">Total Events</div><div class="card-value">%d</div></div>
    <div class="card" id="unique-machines"><div class="card-label">Unique Machines</div><div class="card-value">%d</div></div>
    <div class="card" id="remasters-clones"><span id="remasters" class="anchor-alias"></span><span id="remaster" class="anchor-alias"></span><span id="remaster-clone" class="anchor-alias"></span><div class="card-label">Remasters/Clones</div><div class="card-value">%d/%d</div></div>
    <div class="card" id="sysinstalls-clones"><span id="sysinstalls" class="anchor-alias"></span><span id="installations" class="anchor-alias"></span><span id="sysinstall" class="anchor-alias"></span><span id="installation" class="anchor-alias"></span><span id="sysinstall-clone" class="anchor-alias"></span><span id="clones" class="anchor-alias"></span><span id="clone" class="anchor-alias"></span><div class="card-label">Sysinstalls/Clones</div><div class="card-value">%d/%d</div></div>
  </div>
  <div class="grid">
`, s.TotalEvents, s.UniqueMachines, s.ByAction["remaster"], s.RemasterClones, s.ByAction["sysinstall"], s.SysinstallClones)

	writeHTMLTableCard(&buf, "country", "Country", "Country", s.ByCountry, s.TotalEvents)
	writeHTMLTableCard(&buf, "distro", "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeHTMLTableCard(&buf, "desktop", "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeHTMLTableCard(&buf, "display-server", "Display Server", "Display Server", s.ByDisplayServer, s.TotalEvents, "display_server")
	writeHTMLTableCard(&buf, "virtualization", "Virtualization", "Virt", s.ByVirt, s.TotalEvents, "virt")
	writeHTMLTableCard(&buf, "init-system", "Init System", "Init", s.ByInit, s.TotalEvents, "init")
	writeHTMLTableCard(&buf, "arch", "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeHTMLTableCard(&buf, "version", "Version", "Version", s.ByVersion, s.TotalEvents, "eggs-version")

	buf.WriteString(`  </div>
`)

	writeHTMLCommunityArtisans(&buf, s.Artisans)
	writeHTMLTopInstallations(&buf, s.TopInstallations)
	writeHTMLRecentEvents(&buf, s.RecentEvents)

	if s.RepoClients != nil && s.RepoClients.UniqueIPs > 0 {
		totalOps := s.ByAction["remaster"] + s.ByAction["sysinstall"]
		writeHTMLRepoClients(&buf, *s.RepoClients, s.UniqueMachines, totalOps)
	}

	buf.WriteString(`</div>
</body>
</html>
`)

	return buf.String()
}

func writeHTMLRepoClients(buf *bytes.Buffer, r RepoAnalysisSummary, censusMachines, completedOps int) {
	fmt.Fprintf(buf, `  <section class="panel" id="unique-ips">
    <span id="repo-clients" class="anchor-alias"></span>
    <span id="unique-ips-last-15-days" class="anchor-alias"></span>
    <h2><a href="#unique-ips" class="anchor-link">Unique IP last 15 days</a></h2>
    <div class="subtitle" style="margin-bottom: 1rem; color: var(--muted); font-size: 0.85rem;">Estimated from package manager access logs (Debian/APT, Arch/ALPM, RPM/DNF, Alpine/APK) &bull; Updated: %s</div>

    <div class="cards" style="margin-bottom: 1.5rem;">
      <div class="card"><div class="card-label">Unique IPs (15 days)</div><div class="card-value">%d</div></div>
      <div class="card"><div class="card-label">Census Active Machines</div><div class="card-value">%d</div></div>
      <div class="card"><div class="card-label">Completed Operations</div><div class="card-value">%d</div></div>
    </div>

    <div class="grid">
`, html.EscapeString(r.GeneratedAt), r.UniqueIPs, censusMachines, completedOps)

	writeHTMLTableCard(buf, "repo-families", "Package Managers", "Family", r.ByFamily, r.UniqueIPs)
	writeHTMLTableCard(buf, "repo-countries", "Repo Clients by Country", "Country", r.ByCountry, r.UniqueIPs)

	buf.WriteString(`    </div>
  </section>
`)
}

func writeHTMLTopInstallations(buf *bytes.Buffer, list []TopInstallation) {
	buf.WriteString(`  <section class="panel" id="top-sysinstalls">
    <span id="top-installations" class="anchor-alias"></span>
    <h2><a href="#top-sysinstalls" class="anchor-link">Top 10 Sysinstalls</a></h2>
    <table>
      <thead>
        <tr>
          <th>Country</th>
          <th>Distro</th>
          <th>Desktop</th>
          <th>Display Server</th>
          <th>Arch</th>
          <th>Version</th>
          <th class="num">Sysinstalls</th>
        </tr>
      </thead>
      <tbody>
`)
	if len(list) == 0 {
		buf.WriteString(`        <tr><td colspan="7" class="empty">No sysinstalls recorded yet</td></tr>
`)
	} else {
		for _, item := range list {
			fmt.Fprintf(buf, `        <tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="num">%d</td></tr>
`,
				html.EscapeString(item.Country),
				html.EscapeString(item.Distro),
				html.EscapeString(item.Desktop),
				html.EscapeString(item.DisplayServer),
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

func renderCommunityHTML(artisans []ArtisanProfile) string {
	var buf bytes.Buffer

	totalRemasters := 0
	countriesMap := make(map[string]bool)
	for _, a := range artisans {
		totalRemasters += a.RemasterCount
		c := strings.ToUpper(strings.TrimSpace(a.Country))
		if c != "" && c != "XX" {
			countriesMap[c] = true
		}
	}

	buf.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>🐧 Penguins Community &amp; Distro Artisans</title>
<style>
:root {
  --bg: #0f172a;
  --panel: #1e293b;
  --border: #334155;
  --text: #f8fafc;
  --muted: #94a3b8;
  --accent: #38bdf8;
  --accent-bg: rgba(56, 189, 248, 0.16);
  --tag-bg: #334155;
}
* { box-sizing: border-box; }
html { scroll-behavior: smooth; }
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
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  background: var(--panel);
  color: var(--text);
  border: 1px solid var(--border);
  padding: 0.45rem 0.85rem;
  border-radius: 6px;
  text-decoration: none;
  font-size: 0.85rem;
  cursor: pointer;
  transition: all 0.2s;
}
.btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.btn-primary {
  background: var(--accent);
  color: #0f172a;
  font-weight: 600;
  border-color: var(--accent);
}
.btn-primary:hover {
  background: #7dd3fc;
  color: #0f172a;
  border-color: #7dd3fc;
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
  transition: border-color 0.2s, box-shadow 0.2s;
}
.card:hover {
  border-color: var(--accent);
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
.artisans-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 1.25rem;
  margin-bottom: 2rem;
}
.artisan-card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  transition: border-color 0.2s, box-shadow 0.2s, transform 0.15s ease-out;
}
.artisan-card:hover {
  border-color: var(--accent);
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.35);
}
.card-top {
  margin-bottom: 0.75rem;
}
.card-header-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 0.5rem;
  margin-bottom: 0.35rem;
}
.artisan-nick {
  font-size: 1.2rem;
  font-weight: 700;
  color: var(--accent);
  margin: 0;
  word-break: break-all;
}
.country-badge {
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.3);
  padding: 0.15rem 0.45rem;
  border-radius: 4px;
  font-size: 0.75rem;
  font-weight: 600;
  white-space: nowrap;
}
.artisan-loc {
  font-size: 0.85rem;
  color: var(--muted);
  margin-bottom: 0.6rem;
}
.bio {
  color: #cbd5e1;
  font-size: 0.9rem;
  margin: 0.5rem 0 1rem 0;
  line-height: 1.5;
  word-break: break-word;
}
.iso-box {
  background: rgba(15, 23, 42, 0.65);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0.75rem;
  margin-bottom: 1rem;
}
.iso-label {
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--muted);
  margin-bottom: 0.35rem;
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.iso-badge {
  display: inline-block;
  background: var(--accent-bg);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.4);
  padding: 0.25rem 0.55rem;
  border-radius: 6px;
  font-size: 0.85rem;
  font-family: monospace;
  font-weight: 600;
  word-break: break-all;
}
.iso-meta {
  font-size: 0.75rem;
  color: var(--muted);
  margin-top: 0.4rem;
}
.card-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-top: 0.5rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--border);
}
.social-badge {
  display: inline-flex;
  align-items: center;
  background: var(--tag-bg);
  color: var(--text);
  border: 1px solid var(--border);
  padding: 0.25rem 0.5rem;
  border-radius: 6px;
  font-size: 0.8rem;
  text-decoration: none;
  transition: all 0.2s;
}
.social-badge:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.empty {
  color: var(--muted);
  text-align: center;
  padding: 3rem 1rem;
  font-size: 1.05rem;
  grid-column: 1 / -1;
}
</style>
</head>
<body>
<div class="container">
  <header>
    <div>
      <h1>🐧 Penguins Community &amp; Distro Artisans</h1>
      <div class="subtitle">Showcase of independent Linux distributions and distro artisans building with Penguins Eggs</div>
    </div>
    <div class="links">
      <a class="btn" href="/api/census">📊 Telemetry Dashboard</a>
      <a class="btn" href="?format=txt">Terminal / Plain Text</a>
      <a class="btn" href="?format=json">Raw JSON</a>
      <a class="btn" href="https://penguins-eggs.net" target="_blank" rel="noopener noreferrer">Eggs Home</a>
      <a class="btn" href="https://github.com/pieroproietti/penguins-eggs" target="_blank" rel="noopener noreferrer">Eggs Repo</a>
    </div>
  </header>
`)

	fmt.Fprintf(&buf, `  <div class="cards">
    <div class="card"><div class="card-label">Distro Artisans</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Remaster ISOs Built</div><div class="card-value">%d</div></div>
    <div class="card"><div class="card-label">Countries Represented</div><div class="card-value">%d</div></div>
  </div>

  <div class="artisans-grid">
`, len(artisans), totalRemasters, len(countriesMap))

	if len(artisans) == 0 {
		buf.WriteString(`    <div class="empty">No community artisans registered yet &bull; Register your profile to join the showcase!</div>
`)
	} else {
		for _, a := range artisans {
			nick := strings.TrimSpace(a.Nickname)
			if nick == "" {
				nick = a.UUID
				if len(nick) > 8 {
					nick = nick[:8]
				}
			}
			if !strings.HasPrefix(nick, "@") {
				nick = "@" + nick
			}

			country := strings.ToUpper(strings.TrimSpace(a.Country))
			flag := countryToFlag(country)

			loc := strings.TrimSpace(a.Location)
			locDisplay := ""
			if loc != "" && flag != "" {
				locDisplay = fmt.Sprintf("📍 %s %s", html.EscapeString(loc), flag)
			} else if loc != "" {
				locDisplay = fmt.Sprintf("📍 %s", html.EscapeString(loc))
			} else if flag != "" {
				locDisplay = fmt.Sprintf("📍 %s %s", flag, html.EscapeString(country))
			}

			countryBadge := ""
			if country != "" && country != "XX" {
				if flag != "" {
					countryBadge = fmt.Sprintf(`<span class="country-badge">%s %s</span>`, flag, html.EscapeString(country))
				} else {
					countryBadge = fmt.Sprintf(`<span class="country-badge">%s</span>`, html.EscapeString(country))
				}
			}

			bio := strings.TrimSpace(a.Bio)
			bioHTML := ""
			if bio != "" {
				bioHTML = fmt.Sprintf(`<p class="bio">%s</p>`, html.EscapeString(bio))
			} else {
				bioHTML = `<p class="bio" style="color:var(--muted); font-style:italic;">No bio provided.</p>`
			}

			isoBox := ""
			if a.LatestRemaster != nil {
				badgeText := formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
				dateStr := formatEventDate(a.LastRemasterAt)
				isoBox = fmt.Sprintf(`      <div class="iso-box">
        <div class="iso-label">
          <span>Ultima ISO sfornata</span>
          <span>%d %s</span>
        </div>
        <div class="iso-badge">%s</div>
        <div class="iso-meta">Last build: %s</div>
      </div>`, a.RemasterCount, pluralRemasters(a.RemasterCount), html.EscapeString(badgeText), html.EscapeString(dateStr))
			} else {
				isoBox = `      <div class="iso-box">
        <div class="iso-label"><span>Ultima ISO sfornata</span></div>
        <div class="iso-badge" style="opacity:0.6; font-size:0.8rem;">No ISO recorded yet</div>
      </div>`
			}

			var actionBtns []string
			if isSafeURL(a.SourceURL) {
				actionBtns = append(actionBtns, fmt.Sprintf(`<a href="%s" class="btn btn-primary" target="_blank" rel="noopener noreferrer">🌐 Source / Project &rarr;</a>`, html.EscapeString(a.SourceURL)))
			}

			socialLinks := buildSocialLinks(a.Socials, a.Contact)
			for _, sl := range socialLinks {
				if isSafeURL(sl.URL) {
					actionBtns = append(actionBtns, fmt.Sprintf(`<a href="%s" class="social-badge" target="_blank" rel="noopener noreferrer">%s</a>`, html.EscapeString(sl.URL), html.EscapeString(sl.Label)))
				}
			}

			actionsHTML := ""
			if len(actionBtns) > 0 {
				actionsHTML = fmt.Sprintf(`    <div class="card-actions">
      %s
    </div>`, strings.Join(actionBtns, "\n      "))
			}

			fmt.Fprintf(&buf, `    <div class="artisan-card" id="artisan-%s">
      <div class="card-top">
        <div class="card-header-row">
          <h2 class="artisan-nick">%s</h2>
          %s
        </div>
        %s
        %s
      </div>
%s
%s
    </div>
`, html.EscapeString(a.UUID), html.EscapeString(nick), countryBadge, locDisplayHTML(locDisplay), bioHTML, isoBox, actionsHTML)
		}
	}

	buf.WriteString(`  </div>
</div>
</body>
</html>
`)

	return buf.String()
}

func renderCommunityText(artisans []ArtisanProfile) string {
	var buf bytes.Buffer
	buf.WriteString("# 🐧 Penguins Community & Distro Artisans\n\n")
	fmt.Fprintf(&buf, "- Total Artisans: %d\n\n", len(artisans))

	type tableRow struct {
		Nickname string
		Location string
		Distro   string
		Source   string
		Contacts string
	}

	rows := make([]tableRow, 0, len(artisans))
	for _, a := range artisans {
		nick := strings.TrimSpace(a.Nickname)
		if nick == "" {
			nick = a.UUID
			if len(nick) > 8 {
				nick = nick[:8]
			}
		}
		if !strings.HasPrefix(nick, "@") {
			nick = "@" + nick
		}

		loc := strings.TrimSpace(a.Location)
		country := strings.ToUpper(strings.TrimSpace(a.Country))
		if loc != "" && country != "" && !strings.Contains(strings.ToUpper(loc), country) {
			loc = loc + " [" + country + "]"
		} else if loc == "" && country != "" {
			loc = country
		}
		if loc == "" {
			loc = "-"
		}

		distro := "-"
		if a.LatestRemaster != nil {
			distro = formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
		}

		source := strings.TrimSpace(a.SourceURL)
		if source == "" {
			source = "-"
		}

		var contactParts []string
		links := buildSocialLinks(a.Socials, a.Contact)
		for _, l := range links {
			contactParts = append(contactParts, fmt.Sprintf("%s:%s", l.Platform, l.URL))
		}
		contactStr := "-"
		if len(contactParts) > 0 {
			contactStr = strings.Join(contactParts, ", ")
		} else if a.Contact != "" {
			contactStr = a.Contact
		}

		rows = append(rows, tableRow{
			Nickname: nick,
			Location: loc,
			Distro:   distro,
			Source:   source,
			Contacts: contactStr,
		})
	}

	nickW := len("Nickname")
	locW := len("Location")
	distroW := len("Ultima Distro")
	sourceW := len("Source URL")
	contactW := len("Contatti")

	if len(rows) == 0 {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n", nickW, "Nickname", locW, "Location", distroW, "Ultima Distro", sourceW, "Source URL", contactW, "Contatti")
		fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|\n", strings.Repeat("-", nickW), strings.Repeat("-", locW), strings.Repeat("-", distroW), strings.Repeat("-", sourceW), strings.Repeat("-", contactW))
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n\n", nickW, "(no data)", locW, "-", distroW, "-", sourceW, "-", contactW, "-")
		return buf.String()
	}

	for _, r := range rows {
		if len(r.Nickname) > nickW {
			nickW = len(r.Nickname)
		}
		if len(r.Location) > locW {
			locW = len(r.Location)
		}
		if len(r.Distro) > distroW {
			distroW = len(r.Distro)
		}
		if len(r.Source) > sourceW {
			sourceW = len(r.Source)
		}
		if len(r.Contacts) > contactW {
			contactW = len(r.Contacts)
		}
	}

	fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n", nickW, "Nickname", locW, "Location", distroW, "Ultima Distro", sourceW, "Source URL", contactW, "Contatti")
	fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|\n", strings.Repeat("-", nickW), strings.Repeat("-", locW), strings.Repeat("-", distroW), strings.Repeat("-", sourceW), strings.Repeat("-", contactW))

	for _, r := range rows {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s |\n", nickW, r.Nickname, locW, r.Location, distroW, r.Distro, sourceW, r.Source, contactW, r.Contacts)
	}
	buf.WriteString("\n")

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

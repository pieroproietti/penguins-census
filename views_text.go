package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

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

	type renderedRow struct {
		Country       string
		Distro        string
		Desktop       string
		DisplayServer string
		Arch          string
		EggsVersion   string
		SumSysinstall int
	}

	rows := make([]renderedRow, 0, len(list))
	for _, item := range list {
		distroDisplay := item.Distro
		if item.ArtisanNickname != "" {
			nick := item.ArtisanNickname
			if !strings.HasPrefix(nick, "@") {
				nick = "@" + nick
			}
			distroDisplay = fmt.Sprintf("%s (%s)", item.Distro, nick)
		}
		rows = append(rows, renderedRow{
			Country:       item.Country,
			Distro:        distroDisplay,
			Desktop:       item.Desktop,
			DisplayServer: item.DisplayServer,
			Arch:          item.Arch,
			EggsVersion:   item.EggsVersion,
			SumSysinstall: item.SumSysinstall,
		})
	}

	if len(rows) == 0 {
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

	for _, item := range rows {
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

	for _, item := range rows {
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
		distroDisplay := ev.Distro
		if ev.ArtisanNickname != "" {
			nick := ev.ArtisanNickname
			if !strings.HasPrefix(nick, "@") {
				nick = "@" + nick
			}
			distroDisplay = fmt.Sprintf("%s (%s)", ev.Distro, nick)
		}
		fmt.Fprintf(buf, "| %-19s | %-7s | %-6s | %-7s | %-14s | %-8s | %-10s |\n", date, ev.Country, distroDisplay, ev.Desktop, ev.DisplayServer, rem, sys)
	}
	buf.WriteString("\n")
}

func renderCommunityText(artisans []ArtisanProfile) string {
	var buf bytes.Buffer
	buf.WriteString("# 🐧 Penguins Community & Distro Artisans\n\n")
	fmt.Fprintf(&buf, "- Total Artisans: %d\n\n", len(artisans))

	type tableRow struct {
		Nickname  string
		Location  string
		Distro    string
		Machines  string
		Remasters string
		Installs  string
		Source    string
		Contacts  string
	}

	rows := make([]tableRow, 0, len(artisans))
	for _, a := range artisans {
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
			Nickname:  nick,
			Location:  loc,
			Distro:    distro,
			Machines:  fmt.Sprintf("%d", a.MachinesCount),
			Remasters: fmt.Sprintf("%d", a.RemasterCount),
			Installs:  fmt.Sprintf("%d", a.InstallsCount),
			Source:    source,
			Contacts:  contactStr,
		})
	}

	nickW := len("Nickname")
	locW := len("Location")
	distroW := len("Ultima Distro")
	machW := len("Machines")
	remW := len("Remasters")
	instW := len("Installs")
	sourceW := len("Source URL")
	contactW := len("Contatti")

	if len(rows) == 0 {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %-*s |\n",
			nickW, "Nickname", locW, "Location", distroW, "Ultima Distro",
			machW, "Machines", remW, "Remasters", instW, "Installs",
			sourceW, "Source URL", contactW, "Contatti")
		fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|\n",
			strings.Repeat("-", nickW), strings.Repeat("-", locW), strings.Repeat("-", distroW),
			strings.Repeat("-", machW), strings.Repeat("-", remW), strings.Repeat("-", instW),
			strings.Repeat("-", sourceW), strings.Repeat("-", contactW))
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %-*s |\n\n",
			nickW, "(no data)", locW, "-", distroW, "-",
			machW, "0", remW, "0", instW, "0",
			sourceW, "-", contactW, "-")
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
		if len(r.Machines) > machW {
			machW = len(r.Machines)
		}
		if len(r.Remasters) > remW {
			remW = len(r.Remasters)
		}
		if len(r.Installs) > instW {
			instW = len(r.Installs)
		}
		if len(r.Source) > sourceW {
			sourceW = len(r.Source)
		}
		if len(r.Contacts) > contactW {
			contactW = len(r.Contacts)
		}
	}

	fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %-*s | %-*s |\n",
		nickW, "Nickname", locW, "Location", distroW, "Ultima Distro",
		machW, "Machines", remW, "Remasters", instW, "Installs",
		sourceW, "Source URL", contactW, "Contatti")
	fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|\n",
		strings.Repeat("-", nickW), strings.Repeat("-", locW), strings.Repeat("-", distroW),
		strings.Repeat("-", machW), strings.Repeat("-", remW), strings.Repeat("-", instW),
		strings.Repeat("-", sourceW), strings.Repeat("-", contactW))

	for _, r := range rows {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %*s | %*s | %*s | %-*s | %-*s |\n",
			nickW, r.Nickname, locW, r.Location, distroW, r.Distro,
			machW, r.Machines, remW, r.Remasters, instW, r.Installs,
			sourceW, r.Source, contactW, r.Contacts)
	}
	buf.WriteString("\n")

	hasCatalog := false
	for _, a := range artisans {
		if len(a.LiveDistros) > 0 {
			hasCatalog = true
			break
		}
	}
	if hasCatalog {
		buf.WriteString("### Live Distro Catalogs\n\n")
		for _, a := range artisans {
			if len(a.LiveDistros) == 0 {
				continue
			}
			nick := strings.TrimSpace(a.Nickname)
			if nick == "" {
				nick = a.GetID()
			}
			if !strings.HasPrefix(nick, "@") {
				nick = "@" + nick
			}
			fmt.Fprintf(&buf, "#### %s\n\n", nick)
			for _, ld := range a.LiveDistros {
				badge := formatDistroBadge(DistroInfo{ID: ld.DistroName, Release: ld.Version}, SystemInfo{Arch: ld.Arch, Desktop: ld.Desktop})
				fmt.Fprintf(&buf, "- %s: %d builds (last seen: %s)\n", badge, ld.Count, formatEventDate(ld.LastSeen))
			}
			buf.WriteString("\n")
		}
	}

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

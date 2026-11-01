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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func writeMarkdownTable(buf *bytes.Buffer, title, colName string, m map[string]int, total int) {
	var rows []TableRow
	if title == "Version" {
		rows = sortedVersionRows(m, total)
	} else {
		rows = sortedRows(m, total)
	}
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

	recentSys := s.RecentSysinstalls
	recentRem := s.RecentRemasters
	if len(recentSys) == 0 && len(recentRem) == 0 && len(s.RecentEvents) > 0 {
		for _, ev := range s.RecentEvents {
			if ev.Sysinstall {
				recentSys = append(recentSys, ev)
			} else if ev.Remaster {
				recentRem = append(recentRem, ev)
			}
		}
	}
	writeMarkdownRecentEvents(&buf, "Recent Sysinstalls", recentSys)
	writeMarkdownRecentEvents(&buf, "Recent Remasters", recentRem)

	writeMarkdownTable(&buf, "Country", "Country", s.ByCountry, s.TotalEvents)
	writeMarkdownTable(&buf, "Distro", "Distro", s.ByDistro, s.TotalEvents)
	writeMarkdownTable(&buf, "Desktop", "Desktop", s.ByDesktop, s.TotalEvents)
	writeMarkdownTable(&buf, "Display Server", "Display Server", s.ByDisplayServer, s.TotalEvents)
	writeMarkdownTable(&buf, "Virt", "Virt", s.ByVirt, s.TotalEvents)
	writeMarkdownTable(&buf, "Init", "Init", s.ByInit, s.TotalEvents)
	writeMarkdownTable(&buf, "Arch", "Arch", s.ByArch, s.TotalEvents)
	writeMarkdownTable(&buf, "Version", "Version", s.ByVersion, s.TotalEvents)

	writeMarkdownTopInstallations(&buf, s.TopInstallations)

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
		if item.AuthorNickname != "" {
			nick := item.AuthorNickname
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

func writeMarkdownRecentEvents(buf *bytes.Buffer, title string, events []RecentEvent) {
	fmt.Fprintf(buf, "### %s\n\n", title)
	fmt.Fprintf(buf, "| Date                | Country | Distro | Desktop | Display Server | Version | Clone |\n")
	fmt.Fprintf(buf, "|---------------------|---------|--------|---------|----------------|---------|:-----:|\n")
	if len(events) == 0 {
		fmt.Fprintf(buf, "| (no data)           | -       | -      | -       | -              | -       | -     |\n\n")
		return
	}
	for _, ev := range events {
		clone := "-"
		if ev.Clone {
			clone = "✓"
		}
		date := formatTimeAgo(ev.Date)
		if date == "" {
			date = "-"
		}
		ver := ev.EggsVersion
		if ver == "" {
			ver = "-"
		}
		distroDisplay := ev.Distro
		if ev.AuthorNickname != "" {
			nick := ev.AuthorNickname
			if !strings.HasPrefix(nick, "@") {
				nick = "@" + nick
			}
			distroDisplay = fmt.Sprintf("%s (%s)", ev.Distro, nick)
		}
		fmt.Fprintf(buf, "| %-19s | %-7s | %-6s | %-7s | %-14s | %-7s | %-5s |\n", date, ev.Country, distroDisplay, ev.Desktop, ev.DisplayServer, ver, clone)
	}
	buf.WriteString("\n")
}

func renderCommunityText(authors []AuthorProfile) string {
	var buf bytes.Buffer
	buf.WriteString("# 🐧 Penguins Community & Authors\n\n")

	totalAuthors := len(authors)
	totalRemasters := 0
	totalInstalls := 0
	for _, a := range authors {
		totalRemasters += a.RemasterCount
		totalInstalls += a.InstallsCount
	}

	fmt.Fprintf(&buf, "- Total Authors:    %d\n", totalAuthors)
	fmt.Fprintf(&buf, "- Total Remasters:  %d\n", totalRemasters)
	fmt.Fprintf(&buf, "- Total Installs:   %d\n\n", totalInstalls)

	type tableRow struct {
		Nickname  string
		Profile   string
		Distro    string
		Machines  string
		Remasters string
		Installs  string
	}

	rows := make([]tableRow, 0, len(authors))
	for _, a := range authors {
		nick := strings.TrimSpace(a.DisplayName)
		if nick == "" {
			nick = "author-"
			if len(a.AuthorID) >= 8 {
				nick += a.AuthorID[:8]
			} else {
				nick += a.AuthorID
			}
		}
		if !strings.HasPrefix(nick, "@") {
			nick = "@" + nick
		}

		distro := "-"
		if a.LatestRemaster != nil {
			distro = formatDistroBadge(a.LatestRemaster.Distro, a.LatestRemaster.System)
		}

		prof := strings.TrimSpace(a.ProfileURL)
		if prof == "" {
			prof = "-"
		}

		rows = append(rows, tableRow{
			Nickname:  nick,
			Profile:   prof,
			Distro:    distro,
			Machines:  fmt.Sprintf("%d", a.MachinesCount),
			Remasters: fmt.Sprintf("%d", a.RemasterCount),
			Installs:  fmt.Sprintf("%d", a.InstallsCount),
		})
	}

	nickW := len("Nickname")
	profW := len("Gravatar Profile")
	distroW := len("Ultima Distro")
	machW := len("Machines")
	remW := len("Remasters")
	instW := len("Installs")

	if len(rows) == 0 {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s |\n",
			nickW, "Nickname", profW, "Gravatar Profile", distroW, "Ultima Distro",
			machW, "Machines", remW, "Remasters", instW, "Installs")
		fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|\n",
			strings.Repeat("-", nickW), strings.Repeat("-", profW), strings.Repeat("-", distroW),
			strings.Repeat("-", machW), strings.Repeat("-", remW), strings.Repeat("-", instW))
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s |\n\n",
			nickW, "(no data)", profW, "-", distroW, "-",
			machW, "0", remW, "0", instW, "0")
		return buf.String()
	}

	for _, r := range rows {
		if len(r.Nickname) > nickW {
			nickW = len(r.Nickname)
		}
		if len(r.Profile) > profW {
			profW = len(r.Profile)
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
	}

	fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %-*s | %-*s | %-*s |\n",
		nickW, "Nickname", profW, "Gravatar Profile", distroW, "Ultima Distro",
		machW, "Machines", remW, "Remasters", instW, "Installs")
	fmt.Fprintf(&buf, "|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|\n",
		strings.Repeat("-", nickW), strings.Repeat("-", profW), strings.Repeat("-", distroW),
		strings.Repeat("-", machW), strings.Repeat("-", remW), strings.Repeat("-", instW))

	for _, r := range rows {
		fmt.Fprintf(&buf, "| %-*s | %-*s | %-*s | %*s | %*s | %*s |\n",
			nickW, r.Nickname, profW, r.Profile, distroW, r.Distro,
			machW, r.Machines, remW, r.Remasters, instW, r.Installs)
	}
	buf.WriteString("\n")

	hasCatalog := false
	for _, a := range authors {
		if len(a.LiveDistros) > 0 {
			hasCatalog = true
			break
		}
	}
	if hasCatalog {
		buf.WriteString("### Live Distro Catalogs\n\n")
		for _, a := range authors {
			if len(a.LiveDistros) == 0 {
				continue
			}
			nick := strings.TrimSpace(a.DisplayName)
			if nick == "" {
				nick = a.AuthorID
				if len(nick) > 8 {
					nick = nick[:8]
				}
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

#!/usr/bin/env python3
"""
analyze_repo_clients.py - Stima dei client reali della repository penguins-eggs

Filtra i log di Apache per estrarre gli IP unici dei package manager reali
(Debian APT, Arch pacman/ALPM, Fedora DNF, Alpine APK) che scaricano gli indici
della repository, deduplicando gli IP sull'intero periodo ed escludendo bot e crawler.
"""

import sys
import os
import gzip
import re
import json
import urllib.request
from collections import defaultdict

CRAWLER_KEYWORDS = [
    "bot", "crawler", "spider", "ahrefs", "semrush", "petalbot",
    "yandex", "bingbot", "googlebot", "bytespider", "baiduspider",
]

def is_crawler(ua: str) -> bool:
    lua = ua.lower()
    return any(k in lua for k in CRAWLER_KEYWORDS)

def match_repo_request(path: str, ua: str):
    lp = path.lower()
    lua = ua.lower()

    # Debian / APT
    if "apt-http" in lua or "/repos/deb/" in lp or "/dists/" in lp or "inrelease" in lp:
        is_index = any(x in lp for x in ["inrelease", "release", "packages"])
        is_pkg = lp.endswith(".deb")
        if is_index or is_pkg:
            return "Debian/APT", is_index, is_pkg

    # Arch / ALPM
    if "pacman" in lua or "libalpm" in lua or "/repos/arch/" in lp or lp.endswith(".db") or lp.endswith(".db.tar.gz"):
        is_index = any(lp.endswith(x) for x in [".db", ".db.tar.gz", ".files", ".files.tar.gz"])
        is_pkg = lp.endswith(".pkg.tar.zst") or lp.endswith(".pkg.tar.xz")
        if is_index or is_pkg:
            return "Arch/ALPM", is_index, is_pkg

    # RPM / DNF
    if any(x in lua for x in ["dnf", "libdnf", "yum", "zypp"]) or "/repos/rpm/" in lp or "repomd.xml" in lp:
        is_index = any(x in lp for x in ["repomd.xml", "primary.xml", "filelists.xml"])
        is_pkg = lp.endswith(".rpm")
        if is_index or is_pkg:
            return "RPM/DNF", is_index, is_pkg

    # Alpine / APK
    if "apk-tools" in lua or "/repos/alpine/" in lp or "apkindex" in lp:
        is_index = "apkindex" in lp
        is_pkg = lp.endswith(".apk")
        if is_index or is_pkg:
            return "Alpine/APK", is_index, is_pkg

    return None, False, False

LOG_PATTERN = re.compile(r'^(\S+) \S+ \S+ \[([^\]]+)\] "(?:(\S+) (\S+)[^"]*)" (\d+) \S+ "[^"]*" "([^"]*)"')

def parse_stream(stream, clients, stats):
    for line in stream:
        if isinstance(line, bytes):
            line = line.decode("utf-8", errors="replace")
        stats["total_lines"] += 1
        m = LOG_PATTERN.match(line.strip())
        if not m:
            continue
        ip, dt, method, path, status, ua = m.groups()
        if is_crawler(ua):
            stats["ignored_bots"] += 1
            continue
        if status not in ("200", "206", "304"):
            continue

        family, is_index, is_pkg = match_repo_request(path, ua)
        if not family:
            continue

        stats["valid_hits"] += 1
        cl = clients[ip]
        cl["hits"] += 1
        cl["families"].add(family)
        if is_index:
            cl["has_index"] = True
        if is_pkg:
            cl["has_pkg"] = True

def main():
    paths = sys.argv[1:]
    clients = defaultdict(lambda: {"hits": 0, "families": set(), "has_index": False, "has_pkg": False})
    stats = {"total_lines": 0, "valid_hits": 0, "ignored_bots": 0}

    if not paths or (len(paths) == 1 and paths[0] == "-"):
        parse_stream(sys.stdin, clients, stats)
    else:
        for p in paths:
            if not os.path.exists(p):
                continue
            opener = gzip.open if p.endswith(".gz") else open
            with opener(p, "rt", encoding="utf-8", errors="replace") as f:
                parse_stream(f, clients, stats)

    family_counts = defaultdict(int)
    for ip, data in clients.items():
        for f in data["families"]:
            family_counts[f] += 1

    # Try to load local census data or live API
    census = None
    census_file = "/var/lib/penguins-census/census.jsonl"
    if os.path.exists(census_file):
        try:
            total_events = 0
            uuids = set()
            actions = defaultdict(int)
            for l in open(census_file, "r", encoding="utf-8"):
                if l.strip():
                    rec = json.loads(l)
                    total_events += 1
                    if rec.get("uuid"):
                        uuids.add(rec["uuid"])
                    if rec.get("action"):
                        actions[rec["action"]] += 1
            census = {
                "unique_machines": len(uuids),
                "total_events": total_events,
                "remasters": actions["remaster"],
                "sysinstalls": actions["sysinstall"],
            }
        except Exception:
            pass

    if not census:
        try:
            req = urllib.request.Request("https://penguins-eggs.net/api/census?format=json", headers={"User-Agent": "penguins-analyzer/1.0"})
            with urllib.request.urlopen(req, timeout=3) as resp:
                data = json.loads(resp.read().decode())
                census = {
                    "unique_machines": data.get("unique_machines", 0),
                    "total_events": data.get("total_events", 0),
                    "remasters": data.get("by_action", {}).get("remaster", 0),
                    "sysinstalls": data.get("by_action", {}).get("sysinstall", 0),
                }
        except Exception:
            pass

    print("# Stima Macchine Reali Penguins-Eggs (Repository vs Telemetria)\n")
    print("## 🎯 I Tre Livelli di Misurazione\n")
    print("| Livello di Misurazione | Metrica / Significato | Conteggio Rilevato |")
    print("| :--- | :--- | :---: |")
    print(f"| **1. Client Repository Osservati** | IP unici de-duplicati che interrogano i repo (APT, pacman, DNF, APK) | **{len(clients)}** |")
    if census:
        tot_ops = census["remasters"] + census["sysinstalls"]
        print(f"| **2. Macchine Censite da Eggs** | UUID unici hardware (`/etc/machine-id`) con eggs eseguito | **{census['unique_machines']}** |")
        print(f"| **3. Operazioni Riuscite** | Totale rimasterizzazioni (`remaster`) e installazioni (`sysinstall`) | **{tot_ops}** (R:{census['remasters']}, S:{census['sysinstalls']}) |")
    else:
        print("| **2. Macchine Censite da Eggs** | UUID unici hardware (`/etc/machine-id`) | *(non disponibile)* |")
        print("| **3. Operazioni Riuscite** | Remaster e Sysinstall completati | *(non disponibile)* |")
    print()
    print(f"- **Righe totali di log analizzate:** {stats['total_lines']}")
    print(f"- **Richieste repository valide (200/304):** {stats['valid_hits']}")
    print(f"- **Richieste bot/crawler scartate:** {stats['ignored_bots']}\n")

    print("## 📦 Ripartizione Client Repository per Famiglia\n")
    print("| Famiglia / Package Manager | Client IP Unici | Quota % |")
    print("| :--- | :---: | :---: |")
    tot_clients = max(len(clients), 1)
    for fam, cnt in sorted(family_counts.items(), key=lambda x: -x[1]):
        pct = (cnt / tot_clients) * 100.0
        print(f"| {fam:<26} | {cnt:<15} | {pct:5.1f}% |")
    print()

if __name__ == "__main__":
    main()

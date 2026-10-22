#!/usr/bin/python3
import json
import csv

input_file = "census.jsonl"
output_file = "census.csv"

rows = []
for line in open(input_file):
    if line.strip():
        data = json.loads(line)
        flat = {
            "timestamp": data.get("timestamp"),
            "country": data.get("country"),
            "uuid": data.get("uuid"),
            "eggs_version": data.get("eggs_version"),
            "action": data.get("action"),
            "distro_id": data.get("distro", {}).get("id"),
            "distro_release": data.get("distro", {}).get("release"),
            "arch": data.get("system", {}).get("arch"),
            "desktop": data.get("system", {}).get("desktop"),
            "virt": data.get("system", {}).get("virt"),
            "init": data.get("system", {}).get("init"),
            "clone": data.get("options", {}).get("clone")
        }
        rows.append(flat)

if rows:
    with open(output_file, "w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(f, fieldnames=rows[0].keys())
        writer.writeheader()
        writer.writerows(rows)
    print(f"Creato {output_file} con successo!")

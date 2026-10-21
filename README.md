# penguins-census

A lightweight, privacy-first telemetry server that provides a simple visualization of real-world **penguins-eggs** usage (`remaster` and `sysinstall` events).

## Overview

`penguins-census` collects strictly anonymous usage metrics to help understand how `penguins-eggs` is used across different Linux distributions, architectures (`amd64`, `arm64`, `riscv64`), desktop environments, and virtualization platforms.

### Privacy by Design

- **No IP Logging**: Client IP addresses are resolved in memory against a local MMDB database to determine the 2-letter ISO country code and immediately discarded. Apache access logs are configured to exclude `/api/census` requests (`dontlog`).
- **One-Way Anonymous UUID**: Machine uniqueness is derived locally via a salted SHA-256 hash (formatted as UUIDv5) of `/etc/machine-id`. The original machine ID never leaves the user's machine.
- **Full Transparency**: Every payload sent by `penguins-eggs` is saved locally on the user's system at `~/.config/penguins-eggs/census.json` (and `/etc/penguins-eggs/census.json`) for easy inspection.

## Live Usage Visualization

You can view the live aggregated statistics directly in your browser or via the terminal:

- **Web Dashboard (HTML)**: [https://penguins-eggs.net/api/census](https://penguins-eggs.net/api/census)
- **Terminal / Plain Text (Markdown tables)**:
  ```bash
  curl -s "[https://penguins-eggs.net/api/census?format=txt](https://penguins-eggs.net/api/census?format=txt)"
  ```
- **JSON Summary**:
  ```bash
  curl -s "[https://penguins-eggs.net/api/census?format=json](https://penguins-eggs.net/api/census?format=json)"
  ```

## Payload Schema (`POST /api/census`)

```json
{
  "uuid": "01f86488-6b11-59ff-9630-4215b4d2de3a",
  "eggs_version": "v26.10.14-6",
  "action": "remaster",
  "distro": {
    "id": "debian",
    "like": "debian",
    "codename": "trixie",
    "release": "13"
  },
  "system": {
    "arch": "amd64",
    "desktop": "xfce",
    "virt": "kvm",
    "init": "systemd"
  },
  "options": {
    "clone": false,
    "compression": "zstd",
    "coexist": false
  }
}
```

## Building and Installing the Debian Package

To build the static binary and package it as a `.deb` archive:

```bash
make test
make deb
```

Install the generated package on the server and download the GeoIP database:

```bash
sudo apt install ./penguins-census_0.2.2-1_amd64.deb
sudo penguins-census-update-geoip
sudo systemctl status penguins-census
```

To reset all collected census counts to zero (truncating `/var/lib/penguins-census/census.jsonl` in place while preserving file permissions):

```bash
sudo penguins-census -reset
```

## License

MIT — Copyright (c) 2026 Piero Proietti

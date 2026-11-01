# HomeStreaming

A personal media server and streaming app. It scans a movie/TV folder (torrent-style names welcome) and streams it to a browser on your PC, your iPhone (as a home-screen web app) and an LG webOS TV, at home and, through Tailscale, from anywhere.

- **One small program**: a single Go binary with the web client built in. It runs on Windows (as a service), Linux, macOS or in Docker and needs only ffmpeg.
- **Smart playback**: each file plays directly when the device supports it, is repackaged without re-encoding (remux) when only the container or audio is a problem, and is transcoded otherwise. Transcoding uses NVENC/NVDEC on NVIDIA GPUs, with HDR→SDR tone mapping or HEVC HDR passthrough, and falls back to libx264.
- **Built for messy libraries**: scene-name parsing (`Show.S01E02…`, `[07] Title.mkv`, season packs), merging of differently named folders into one show, and skipping of samples, unfinished downloads (`.!qB`, `.part`), game files and DVD folders. A folder watcher picks up finished downloads automatically.
- **Russian/English releases**: audio tracks are labelled even when untagged ("LostFilm" → Russian), a preferred audio language applies automatically (default English), subtitles come from embedded, sidecar and `Subs/` folders (Windows-1251 handled), and DTS/TrueHD audio is converted for TVs that can't decode it.
- **Watching**: continue watching, next up, resume, played status, and up-next autoplay.
- **TV mode**: remote-control (D-pad) navigation, the webOS Back key and media keys, and a 10-foot layout.
- **Secure by default**: single admin account with Argon2id passwords, login rate limiting and lockout, and per-device sessions you can revoke.

| Docs | |
|---|---|
| [deploy/windows.md](deploy/windows.md) | Install and run on the Windows PC (service, firewall, TV, iPhone) |
| [deploy/tailscale.md](deploy/tailscale.md) | Remote access (CGNAT-friendly) |
| [docs/DESIGN.md](docs/DESIGN.md) | Architecture and technical design |
| [docs/API.md](docs/API.md) | HTTP API reference |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Phases and what's next |

## Quick start

### Windows

1. Install ffmpeg:
   ```powershell
   winget install Gyan.FFmpeg
   ```
2. Download `homestreaming-windows-amd64.zip` from the [latest release](https://github.com/wtfits/HomeStreaming/releases/latest) and unzip it.
3. Run `homestreaming.exe`, allow it through the firewall, and open <http://localhost:8420>.

The full guide is [deploy/windows.md](deploy/windows.md).

### Updates

Every push to `main` that passes CI is published as a GitHub release (`v0.1.N`, with `SHA256SUMS`). The server checks for new releases every 6 hours; **Admin → Updates** installs one with a click (Windows service: restart and automatic rollback included). From a terminal: `homestreaming update` (or `update --check`). Docker installs update by pulling a new image.

### Docker (Linux server / NAS)

```bash
docker build -f deploy/Dockerfile -t homestreaming .
```

```bash
docker run -d --name homestreaming -p 8420:8420 -v hs-data:/data -v /srv/media:/media:ro homestreaming
```

See also [deploy/docker-compose.yml](deploy/docker-compose.yml) (with commented GPU passthrough).

### Linux (systemd)

Copy the binary to `/usr/local/bin/homestreaming`, install `ffmpeg`, and use [deploy/homestreaming.service](deploy/homestreaming.service). Alternatively:

```bash
sudo homestreaming service install
```

## Moving to new hardware

1. Stop the server.
2. Copy the data directory to the new machine (on Windows: `C:\ProgramData\HomeStreaming`; Linux service: `/var/lib/homestreaming`; Docker: the `/data` volume). You can skip `transcode/` and `cache/`.
3. Start the server there, then in **Admin → Libraries → Edit** change each folder (e.g. `F:\torrent` → `/media/torrent`) and scan. Items are keyed by their path relative to the library folder, so watch progress and artwork are kept.
4. Set `hwaccel` in `config.toml` for the new GPU (`vaapi`, `qsv`, `nvenc` or `none`).

## Build from source

Requires Go 1.26+ (the `go` command downloads it automatically when `GOTOOLCHAIN=auto`), Node 22 and ffmpeg.

```bash
make web        # build the web client into web/dist (embedded into the binary)
make build      # bin/homestreaming
make test       # Go unit + API tests (generates test media with ffmpeg)
make test-e2e   # Playwright: desktop, iPhone-emulated and webOS-emulated browsers
make dist       # cross-compile Windows/Linux/macOS archives into dist/
```

Run a development server with `./bin/homestreaming --data-dir ./data`. For the web client with hot reload, run `cd web && npm run dev`, which proxies `/api` to `:8420`.

## Project layout

```
cmd/homestreaming/   CLI: serve, doctor, service install|start|stop, version
internal/api/        HTTP API + embedded web client serving
internal/library/    naming parser, scanner, probe, artwork, subtitles, watcher
internal/stream/     device profiles, decision engine, HLS sessions, subtitles
internal/ffmpeg/     locator, hardware self-tests, command builders
internal/db/         SQLite schema, migrations and queries
internal/auth/       passwords, sessions, rate limiting
web/                 Preact + hls.js client (Chrome 79 / Safari 13 target)
e2e/                 Playwright tests
deploy/              Dockerfile, compose, systemd, Windows and Tailscale guides
```

# HomeStreaming — Design

Personal media server + streaming web app. Serves a local media collection to a PC browser, an iPhone (Safari/PWA) and an LG webOS TV, on the LAN and over the internet. Original project; Jellyfin/Plex are only reference points.

This document is the implementation contract for Phase 1 (MVP) plus the design hooks for later phases. The API reference is in [API.md](API.md); phases and acceptance criteria are in [ROADMAP.md](ROADMAP.md). Anything marked **deferred** is explicitly out of Phase 1.

---

## 0. Decisions at a glance

| Area | Decision |
|---|---|
| Server language | **Go 1.24**, single static binary, `CGO_ENABLED=0`, cross-compiled for windows/amd64 (primary), linux/amd64, linux/arm64, darwin/arm64 |
| Database | **SQLite** via `modernc.org/sqlite` (pure Go), WAL mode, one file in the data dir |
| HTTP | `net/http` + `github.com/go-chi/chi/v5`; JSON API under `/api/v1`; web client embedded with `go:embed` |
| Media tooling | External **ffmpeg/ffprobe ≥ 6.1** (on the PC: Gyan full build installed via `winget install Gyan.FFmpeg`; path auto-located or configured; a missing ffmpeg is reported loudly) |
| Client | **One web client** (Preact + TypeScript + Vite, hls.js), served by the server. Same bundle: desktop, iPhone PWA, LG TV browser ("TV mode" with D‑pad navigation). Build target **Chrome 79 / Safari 13** (webOS 6.0) |
| Later clients | Phase 2: webOS IPK wrapping the same bundle; DLNA/UPnP server. Phase 3: native iOS |
| Playback | Direct play (HTTP Range) → direct stream/remux (HLS fMP4, `-c copy`) → transcode (HLS fMP4, H.264 SDR or HEVC Main10 HDR passthrough). 6 s segments |
| Hardware accel | `transcode.hwaccel = auto` → **NVENC/NVDEC** primary (RTX 4090), software libx264 fallback (= what container tests exercise). HDR→SDR tonemap on CPU at output resolution after GPU decode+scale (stock ffmpeg has no CUDA→OpenCL tonemap) |
| Library model | Libraries with one or more root paths; type `mixed` (default; movie vs. episode classified per file from scene-style names), `movies`, `shows`. Items keyed by `rel_path` so roots can move |
| Metadata | Phase 1 fully offline: filename/folder parsing, ffprobe, local artwork, ffmpeg-extracted thumbnails. TMDB/NFO **deferred** (Phase 2) |
| Scanning | Full scan on add/startup/manual + periodic (6 h) + **fsnotify watcher** with settle/stability rules for in-progress torrent files |
| Auth | Argon2id passwords, opaque session tokens (cookie + Bearer), roles admin/user in the data model, single-user UI in Phase 1, login rate limiting + lockout |
| Remote access (default) | **Tailscale** — the only workable option: the ISP uses **CGNAT** (router WAN 100.109.73.201, no global IPv6), so inbound port forwarding is impossible. PC and iPhone are already on the tailnet; the server listens on all interfaces incl. the Tailscale one. LG TV is LAN-only. Cloudflare Tunnel **rejected** (free-plan terms forbid video). Later alternative for non-Tailscale devices: small VPS relay (WireGuard + reverse proxy + Let's Encrypt) |
| Remote bitrate policy | Upload is **10 Mbps** (DOCSIS 3.0, 100/10) and Tailscale DERP relays are throughput-limited → `remote_max_bitrate_kbps` default **6000** (≈5.2 Mbps video + 192 kbps AAC stereo), `lan_max_bitrate_kbps` 0 (unlimited; 1 Gbps wired LAN direct-plays anything). Remote playback above the cap always transcodes |
| Config | `config.toml` (bootstrap: ports, paths, TLS) + env `HS_*` overrides + DB `settings` (editable in admin UI) |
| Data dir | Windows `C:\ProgramData\HomeStreaming`, Linux `/var/lib/homestreaming` (service) or `~/.local/share/homestreaming`, Docker `/data`. Transcode dir configurable (any drive) |
| Packaging | `make dist` cross-compiles; `deploy/Dockerfile` (ubuntu:24.04 + ffmpeg 6.1, multi-arch); Windows service via `homestreaming service install`; systemd unit |
| Port | **8420** (HTTP). HTTPS optional on 8443 |
| Testing | Go unit + API tests (`httptest`, ffmpeg-generated fixtures), Playwright E2E against the real binary with headless Chromium (desktop, iPhone-emulated, webOS-emulated), ffmpeg as "native HLS player" validator |

### 0.1 Known environment facts (design inputs)

| Fact | Consequence |
|---|---|
| Server: Windows 11 Pro 24H2 (26200), Ryzen 9 7950X 16C/32T, 63 GB RAM, RTX 4090 (NVENC/NVDEC H.264/HEVC/AV1), AMD iGPU | Windows is the Phase 1 primary target. NVENC first; AMF/QSV/VAAPI only via config. Plenty of CPU for software fallback and CPU tonemapping |
| ffmpeg: Gyan full build now installed on the PC via winget (nvenc, cuda, libplacebo, zscale, libass included) | Locator finds the winget install even when running as a service (globs every user profile); `ffmpeg.path` configurable; setup wizard + `homestreaming doctor` show what was found |
| Drives: C: 953 GB (257 free), D:, E:, G:; media on F: (Crucial MX500 SATA SSD, 139 GB free) | Data dir on C:; transcode dir default `C:\ProgramData\HomeStreaming\transcode`, configurable |
| Network: 1 Gbps wired LAN 192.168.0.0/24; router 192.168.0.1 TEKNOTEL "Infinity 401" (DHCP pool .10–.254, 7-day leases; UPnP + multicast enabled); TV .10, PC .13, iPhone .16; Windows profile Private | Any file direct-plays on the LAN; SSDP/DLNA and mDNS work on the LAN (Phase 2); recommend a static IP for the PC outside the pool (e.g. **192.168.0.5**) so TV/iPhone bookmarks stay stable |
| ISP: Turksat cable, 100 Mbps down / **10 Mbps up** (DOCSIS 3.0); **CGNAT confirmed** (router WAN 100.109.73.201 in 100.64.0.0/10), no global IPv6 | No port forwarding possible → Tailscale is the default and only built-in remote path; remote cap 6000 kbps; 4K remote remux is unrealistic and is always transcoded |
| Tailscale installed on the server PC and the iPhone | Remote setup is a checklist, not a feature (§8.1); firewall rule must cover the Tailscale interface; "Run unattended" needed on Windows |
| Media `F:\torrent`: 450 files / 327 GB / 10 folders; 94 MKV; a DVD `VIDEO_TS`; game install junk (.dll/.exe/.assets…), .zip, extensionless; 4 external .srt; mostly TV from Russian groups with Rus+Eng audio; 2160p HEVC DV/HDR10 WEB-DL, 1080p BDRemux, 720p HDTV | `mixed` library type; scene-name parser with real fixtures; samples/incomplete files skipped; VIDEO_TS **deferred** (skipped with a log line in Phase 1); preferred audio language setting (default `eng`) in Phase 1; HDR/DV handling in the decision engine |
| iPhone 14 Pro, iOS 27, Safari/PWA: native HLS fMP4, HEVC Main10, HDR10, DV 8.x; no AV1 HW; no MKV | MKV → remux to HLS fMP4 whenever codecs allow; HDR passthrough in remux; HEVC-Main10 HDR transcode target when bitrate must drop |
| LG OLED65A16LA (A1, 2021), webOS 6.0, Chromium ~79; HEVC 4K10 HDR10/HLG/DV, AC3/EAC3; probably no DTS; DLNA 1.50 at 192.168.0.10 | Client build target chrome79; capability detection at runtime; DTS excluded from base webOS profile; DLNA + IPK in Phase 2 |
| PC: Chrome/Edge on Windows 11, HEVC HW decode maybe | Runtime `MediaSource.isTypeSupported`/`canPlayType`/`mediaCapabilities`; fallback H.264 transcode via NVENC |
| One user profile; UI language English, content Rus/Eng | Single-user UI; strings in one `en.ts` module for later i18n |

---

## 1. Tech stack

### 1.1 Server (Go)

| Concern | Choice | Why |
|---|---|---|
| Language | Go 1.24 | One static binary per OS/arch, trivial cross-compile without cgo, excellent stdlib HTTP, easy process control of ffmpeg, easy Windows service |
| Router | `github.com/go-chi/chi/v5` | Small, idiomatic, middleware chain, route groups |
| DB driver | `modernc.org/sqlite` | Pure Go → no cgo, cross-compiles to windows/arm64 |
| Migrations | Own 60-line migrator: embedded `internal/db/migrations/NNNN_name.sql`, `schema_migrations(version)` table | No extra dependency |
| Passwords | `github.com/alexedwards/argon2id` (on `golang.org/x/crypto/argon2`) | Argon2id with sane defaults |
| Config | `github.com/BurntSushi/toml` | Simple, typed |
| Watcher | `github.com/fsnotify/fsnotify` | Windows `ReadDirectoryChangesW` + inotify; non-recursive → we add every directory |
| Images | `golang.org/x/image/draw` (+ `x/image/webp` decode) | Pure Go resize; JPEG/PNG from stdlib |
| Rate limit | `golang.org/x/time/rate` | Token buckets |
| Logs | `log/slog` + `gopkg.in/natefinch/lumberjack.v2` | Structured logs; rotating file |
| Service | `github.com/kardianos/service` | Windows service / systemd / launchd install from the binary |
| Windows syscalls | `golang.org/x/sys/windows` | Drive enumeration, process creation flags |
| Tests | `github.com/stretchr/testify` | Assertions |
| Phase 2 | `github.com/huin/goupnp` (UPnP IGD), `golang.org/x/crypto/acme/autocert` (ACME) | — |

No ORM, no code generation. SQL is hand-written in `internal/db/*.go`.

### 1.2 Client (web)

| Concern | Choice | Why |
|---|---|---|
| Framework | Preact 10 + TypeScript 5 (`@preact/preset-vite`) | Tiny bundle for the TV; React-like ergonomics |
| Build | Vite 7, `build.target: ['chrome79', 'safari13']` | webOS 6.0 is Chromium ~79; esbuild lowers `?.`, `??`, etc. |
| Routing | `wouter-preact` with the hash location hook | Hash routes work identically when served by the server, from `file://` in the Phase 2 webOS IPK, and need no server fallback |
| Player | `<video>` + `hls.js` (MSE) or native HLS (Safari) | iPhone Safari has no MSE for our purposes; everything else uses hls.js |
| State | Preact signals (`@preact/signals`) | Minimal |
| Styling | Plain CSS with custom properties, BEM-ish classes. No Tailwind | Fewer moving parts; must stay Chrome-79-safe (see Appendix D) |
| PWA | Hand-written `manifest.webmanifest` + minimal `sw.js` (app-shell cache only, never media) | iPhone "Add to Home Screen" |
| i18n | `web/src/i18n/en.ts` + `t(key, params)` helper; English only | Ready for Turkish later |
| E2E | `@playwright/test` **1.63.0** (pin to the preinstalled browsers in `/opt/pw-browsers`) | See §10 |

### 1.3 Tooling present in the build container

Go 1.24.7, Node 22 / npm 10, ffmpeg/ffprobe 6.1.1 (libx264, libx265, libvpx, libaom/svt-av1, libass, libzimg/zscale, libplacebo; nvenc/qsv/vaapi encoders compiled in but no GPU), Playwright 1.63.0 with Chromium 153 at `/opt/pw-browsers` (`PLAYWRIGHT_BROWSERS_PATH` is set), Google Chrome for Testing at `/usr/bin/google-chrome`, Docker 29. Verified: the bundled headless Chromium decodes H.264/AAC/VP9/AV1/Opus/FLAC and plays MKV; it does **not** decode HEVC or AC‑3/E‑AC‑3 (useful to force the remux/transcode paths in tests).

---

## 2. Architecture

### 2.1 Components (one process)

```
┌──────────────────────────────── homestreaming (single process) ────────────────────────────────┐
│  HTTP server (chi)                                                                              │
│   ├─ /            embedded web client (SPA, hash routes)                                        │
│   ├─ /api/v1/...  JSON API  ──► handlers ──► services                                           │
│   └─ /api/v1/play/{sid}/...  HLS playlists/segments, direct file (Range), subtitles             │
│  Services                                                                                       │
│   ├─ auth        argon2id, sessions, rate limiter, lockout                                       │
│   ├─ library     scanner (walk+classify+probe+images), watcher (fsnotify), periodic rescan        │
│   ├─ ffmpeg      locate binaries, capability self-test (encoders, NVDEC/NVENC), command builders │
│   ├─ stream      device profiles, decision engine, play sessions, HLS transcoder, playlists      │
│   ├─ images      poster/backdrop/thumb store, resize cache                                      │
│   └─ tasks       background jobs (scan, probe queue, image queue) with progress                  │
│  Storage: SQLite (WAL) + data dir (images, caches, transcode temp, logs)                         │
│  Child processes: ffprobe (scan), ffmpeg (thumbs, subtitle conversion, HLS sessions)            │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘
```

Process model: one Go process; goroutine worker pools (probe pool = 2 workers, image pool = 1, transcode sessions ≤ `transcode_max_sessions`). ffmpeg/ffprobe are children started with `os/exec`; on Windows they are created with `CREATE_NO_WINDOW | BELOW_NORMAL_PRIORITY_CLASS`, on Unix `setpriority(+10)` after start. Graceful shutdown: stop HTTP (10 s drain), kill sessions, delete transcode dirs, `PRAGMA wal_checkpoint(TRUNCATE)`, close DB.

### 2.2 Repository layout

```
HomeStreaming/
├── cmd/homestreaming/main.go          # CLI: serve (default), service {install|uninstall|start|stop|status}, doctor, version
├── internal/
│   ├── app/                           # wiring: config → db → services → http; Run(ctx)
│   ├── config/                        # config.toml + HS_* env + defaults, data dir resolution
│   ├── db/                            # Open(), migrate.go, migrations/*.sql (embedded), queries: users.go items.go libraries.go userdata.go settings.go
│   ├── auth/                          # password.go, sessions.go, middleware.go, ratelimit.go
│   ├── api/                           # router.go, respond.go (JSON/error helpers), handlers_*.go per domain
│   ├── library/                       # scanner.go, classify.go, naming.go (parser), probe.go, artwork.go, watcher.go, ignore.go
│   ├── ffmpeg/                        # locate.go, probe.go (ffprobe JSON types), capabilities.go (self-tests), exec_windows.go/exec_unix.go
│   ├── stream/                        # profile.go, decide.go, session.go, transcoder.go, playlist.go, direct.go, subtitles.go, codecs.go
│   ├── images/                        # store.go, resize.go
│   ├── netutil/                       # clientip.go (trusted proxies), class.go (lan/remote)
│   ├── tasks/                         # task manager with progress
│   └── testutil/                      # test media generation (ffmpeg), temp data dirs
├── web/                               # client
│   ├── embed.go                       # package web; //go:embed all:dist
│   ├── dist/.keep                     # committed so `go build` works before the web build
│   ├── index.html, vite.config.ts, tsconfig.json, package.json
│   ├── public/ (manifest.webmanifest, icons, sw.js)
│   └── src/ (api/, player/, nav/, screens/, components/, i18n/, state/, styles/)
├── clients/webos/                     # Phase 2: appinfo.json, icons, build-ipk.sh (wraps web/dist) — empty in Phase 1
├── e2e/                               # Playwright: package.json, playwright.config.ts, tests/*.spec.ts, fixtures/
├── scripts/                           # make-test-media.sh, e2e.sh, build-dist.sh, check-es-target.mjs
├── deploy/                            # Dockerfile, docker-compose.yml, homestreaming.service, windows.md, tailscale.md
├── docs/                              # DESIGN.md, API.md, ROADMAP.md
├── Makefile, go.mod, go.sum, .gitignore, .nvmrc (22), README.md
```

`web/dist/*` is git-ignored except `.keep`; `make web` writes the Vite build there and `go build` embeds it. `all:dist` fails on an empty dir, which is why `.keep` is committed (verified).

### 2.3 Request flow for playback (summary; details in §6)

1. Client builds a **capability profile** (runtime detection) and calls `POST /api/v1/items/{id}/play-info`.
2. Server runs the **decision engine** → `direct | remux | transcode`, creates a **play session** (always, even for direct) and, for HLS methods, starts ffmpeg immediately.
3. Client plays `mediaUrl` (file URL or `master.m3u8`), pings `POST /play/{sid}/progress` every 10 s (persists resume position, keeps the session alive), `DELETE /play/{sid}` on exit.
4. Seeks: direct → HTTP Range; transcode → predictive playlist, server restarts ffmpeg at the requested segment when needed; remux → EVENT playlist, client opens a new session when seeking beyond the produced range.

---

## 3. Data model, scanning, naming, metadata

### 3.1 Tables (SQLite)

All ids are 32-char lowercase hex (`crypto/rand`, 16 bytes). Timestamps are ISO‑8601 UTC strings (`TEXT`), durations are integer milliseconds. Booleans are `INTEGER 0/1`.

```sql
-- 0001_init.sql
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);

CREATE TABLE users (
  id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL, is_admin INTEGER NOT NULL DEFAULT 0,
  preferences_json TEXT NOT NULL DEFAULT '{}',     -- {audioLanguage, subtitleLanguage, subtitleMode}
  disabled INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);

CREATE TABLE sessions (                            -- auth sessions (devices)
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,                 -- sha256(token) hex
  device_id TEXT NOT NULL, device_name TEXT NOT NULL, client_type TEXT NOT NULL, -- web|webos|ios|other
  user_agent TEXT, ip TEXT, created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, expires_at TEXT NOT NULL);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE libraries (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('mixed','movies','shows')),
  created_at TEXT NOT NULL, last_scan_at TEXT, last_scan_status TEXT, last_scan_error TEXT);

CREATE TABLE library_paths (
  id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path TEXT NOT NULL,                              -- absolute, OS-native (e.g. F:\torrent)
  UNIQUE(library_id, path));

CREATE TABLE items (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK(type IN ('movie','series','season','episode')),
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  parent_id TEXT REFERENCES items(id) ON DELETE CASCADE,   -- episode→season, season→series
  series_id TEXT REFERENCES items(id) ON DELETE CASCADE,   -- denormalized on season+episode
  series_key TEXT,                                         -- series: normalized title key (see 3.4)
  title TEXT NOT NULL, sort_title TEXT NOT NULL, year INTEGER, overview TEXT,
  season_number INTEGER, episode_number INTEGER, episode_end_number INTEGER,
  premiere_date TEXT,
  -- file-backed items (movie, episode)
  library_path_id TEXT REFERENCES library_paths(id) ON DELETE CASCADE,
  rel_path TEXT,                                           -- forward slashes, relative to library_paths.path
  size INTEGER, mtime TEXT, container TEXT, runtime_ms INTEGER, bitrate INTEGER,
  video_codec TEXT, width INTEGER, height INTEGER, hdr TEXT,  -- hdr: NULL|'hdr10'|'hlg'|'dolbyvision'
  audio_langs TEXT,                                        -- comma list for badges, e.g. "rus,eng"
  probe_json TEXT,                                         -- raw ffprobe JSON (for re-deriving streams)
  status TEXT NOT NULL DEFAULT 'ok' CHECK(status IN ('ok','pending','missing')),
  status_reason TEXT, probe_attempts INTEGER NOT NULL DEFAULT 0,
  added_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(library_path_id, rel_path));
CREATE INDEX items_lib_type ON items(library_id, type);
CREATE INDEX items_parent ON items(parent_id);
CREATE INDEX items_series ON items(series_id, season_number, episode_number);
CREATE INDEX items_series_key ON items(library_id, series_key);
CREATE INDEX items_sort ON items(type, sort_title);
CREATE INDEX items_added ON items(added_at);

CREATE TABLE media_streams (
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  idx INTEGER NOT NULL,                                    -- ffprobe stream index
  type TEXT NOT NULL CHECK(type IN ('video','audio','subtitle')),
  codec TEXT NOT NULL, profile TEXT, level INTEGER, codec_tag TEXT,
  width INTEGER, height INTEGER, pix_fmt TEXT, bit_depth INTEGER, frame_rate REAL,
  color_transfer TEXT, color_primaries TEXT, color_space TEXT, dv_profile INTEGER, dv_bl_compat INTEGER,
  field_order TEXT, bitrate INTEGER,
  channels INTEGER, channel_layout TEXT, sample_rate INTEGER,
  language TEXT, title TEXT, is_default INTEGER NOT NULL DEFAULT 0, is_forced INTEGER NOT NULL DEFAULT 0,
  is_text INTEGER NOT NULL DEFAULT 0,                      -- subtitles: text-based (convertible to WebVTT)
  PRIMARY KEY(item_id, idx));

CREATE TABLE external_subtitles (
  id TEXT PRIMARY KEY, item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  rel_path TEXT NOT NULL,                                  -- relative to the item's library path
  format TEXT NOT NULL,                                    -- srt|vtt|ass|ssa
  language TEXT, title TEXT, is_forced INTEGER NOT NULL DEFAULT 0, is_sdh INTEGER NOT NULL DEFAULT 0,
  UNIQUE(item_id, rel_path));

CREATE TABLE images (
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK(kind IN ('poster','backdrop','thumb')),
  source TEXT NOT NULL CHECK(source IN ('local','extracted','provider')),
  path TEXT NOT NULL,                                      -- absolute path (local file) or data-dir relative (extracted)
  width INTEGER, height INTEGER, updated_at TEXT NOT NULL,
  PRIMARY KEY(item_id, kind));

CREATE TABLE chapters (
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  idx INTEGER NOT NULL, start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, title TEXT,
  PRIMARY KEY(item_id, idx));

CREATE TABLE user_item_data (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position_ms INTEGER NOT NULL DEFAULT 0, played INTEGER NOT NULL DEFAULT 0,
  play_count INTEGER NOT NULL DEFAULT 0, last_played_at TEXT,
  audio_language TEXT, subtitle_language TEXT,             -- last used, per item (series-level memory is Phase 2)
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id, item_id));
CREATE INDEX uid_last_played ON user_item_data(user_id, last_played_at);

CREATE TABLE login_attempts (                              -- lockout bookkeeping, pruned hourly
  key TEXT PRIMARY KEY,                                    -- 'u:<username>' or 'ip:<ip>'
  failures INTEGER NOT NULL, locked_until TEXT, updated_at TEXT NOT NULL);
```

Pragmas on open: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`. Two pools: `writeDB` (`SetMaxOpenConns(1)`) and `readDB`. Play sessions and transcoder state are **in memory only**.

Settings keys (DB, editable via `PATCH /api/v1/admin/settings`):

| Key | Default | Meaning |
|---|---|---|
| `server_name` | `HomeStreaming` | Shown in UI |
| `remote_max_bitrate_kbps` | `6000` | Cap for `remote` connections (0 = unlimited). Sized for the 10 Mbps upload and DERP-relayed Tailscale paths |
| `lan_max_bitrate_kbps` | `0` | Cap for `lan` connections |
| `tailnet_is_lan` | `false` | Treat 100.64.0.0/10 as LAN |
| `transcode_max_sessions` | `3` | Concurrent ffmpeg HLS sessions |
| `transcode_preset` | `veryfast` | libx264 preset (`p4` is used for NVENC regardless) |
| `transcode_threads` | `0` | `-threads` for software encode (0 = auto) |
| `hls_segment_seconds` | `6` | Segment target |
| `scan_interval_hours` | `6` | Periodic rescan |
| `played_threshold_percent` | `92` | Position ≥ this % marks played |
| `audio_transcode_codec` | `auto` | `auto` = AC‑3 5.1 when the client supports it, the source has >2 ch **and** the applied cap is 0 or ≥ 10000 kbps; otherwise AAC stereo 192 kbps (remote streams always get AAC stereo; even copyable AC‑3 640k is re-encoded when the cap is < 10000) |
| `profile_overrides_json` | `{}` | Per-platform add/remove codecs (see §6.2) |

### 3.2 Library roots and portability

- A library has one or more absolute root paths (`library_paths`). Items store `library_path_id` + `rel_path` (always `/`‑separated via `filepath.ToSlash`); the absolute path is `filepath.Join(root, filepath.FromSlash(rel_path))`.
- Migration to new hardware = copy the data dir, then edit each library path (`PATCH /libraries/{id}` with the new `paths`). Items keep their ids and user data; the next scan validates files by `rel_path`.
- Windows: paths are stored as given (e.g. `F:\torrent`); comparisons use `filepath.Clean` + `strings.EqualFold`. Go's `os` package handles `\\?\` long paths automatically.

### 3.3 Scanner

Pipeline per library (a `tasks` job with progress `{phase, done, total}`):

1. **Preflight**: each root must exist and be a directory, else the scan is aborted with `last_scan_error` (protects against unmounted drives: nothing is marked missing).
2. **Walk** (`filepath.WalkDir`), applying ignore rules (§3.5). Collect candidate video files with `size`, `mtime`.
3. **Classify + parse** (§3.4) → `movie` or `episode` with title/year/season/episode/episode title; group episodes into series/seasons by `series_key`.
4. **Upsert** items by `(library_path_id, rel_path)`; new items get `status='pending'` until probed; items whose `size`/`mtime` changed are re-queued for probe. Virtual items (`series`, `season`) are created/updated as needed, deleted when they have no children.
5. **Missing**: file-backed items of this library not seen in the walk → `status='missing'` (hidden from listings, user data retained). `POST /libraries/{id}/clean` deletes missing items. Deleting a torrent therefore never loses progress for the rest of the show.
6. **Probe queue** (2 workers): `ffprobe -v error -print_format json -show_format -show_streams -show_chapters -- <file>` → `media_streams`, `chapters`, `runtime_ms`, `container`, `bitrate` (`format.bit_rate` or `size*8/duration`), `hdr` (from `color_transfer`: `smpte2084`→hdr10, `arib-std-b67`→hlg; DOVI side data → dolbyvision with `dv_profile`, `dv_bl_signal_compatibility_id`), `bit_depth` from `pix_fmt` (`p10`→10). Success → `status='ok'`. Failure (exit≠0, no video stream, or no duration) → stays `pending`, `probe_attempts++`, `status_reason` = stderr tail; retried on next scan/watch event (max 10 attempts, then listed as `pending` in admin "Problems").
7. **External subtitles** (§3.6) and **artwork** (§3.7). Image extraction queue (1 worker) runs after probing; thumbnails never block listing.

The UI is usable after step 4 (items appear with placeholder art, "processing" badge while `pending`).

Scheduling: scan on library create, on startup (all libraries, `quick` mode = walk + mtime/size compare, no re-probe of unchanged files), every `scan_interval_hours`, and manual via API. One scan per library at a time; a request during a running scan is coalesced.

### 3.4 Classification and naming parser (`internal/library/naming.go`)

Input per video file: the file name (without extension), the parent folder name(s) up to the library root, and the sibling video count in its folder. Output:

```go
type Parsed struct {
    Kind        string // "movie" | "episode"
    Title       string // movie title or series title
    Year        int    // 0 if unknown
    Season      int    // episodes only; 1 when no season info exists
    Episode     int    // first episode number
    EpisodeEnd  int    // 0 unless multi-episode (S01E01E02 / S01E01-E02)
    EpisodeTitle string
    SeriesKey   string // normalize(Title) — see below
    Confidence  string // "high" | "low" (for admin "Problems" list only)
}
```

**Tokenization**: replace `.`, `_` with spaces (keep apostrophes, hyphens inside words), collapse whitespace, strip bracketed groups `[...]`, `(...)` *after* extracting a year or season from them, strip `by <token>` and a trailing `-GROUP` (hyphen + a single token at the very end, e.g. `-GRP`, `-AKTEP`) and known group/site tokens (Appendix C).

**Episode detection** (first match wins; case-insensitive), run on the file name first, then on the folder name:

| # | Pattern (regex) | Yields |
|---|---|---|
| E1 | `\bS(\d{1,2})[ ._-]*E(\d{1,3})(?:[ ._-]*E(\d{1,3}))?\b` | season, episode, episodeEnd |
| E2 | `\b(\d{1,2})x(\d{1,3})\b` | season, episode |
| E3 | folder matches `\bS(\d{1,2})\b` or `\(?Season (\d{1,2})\)?` or `\bSeason[ ._]?(\d{1,2})\b` **and** file matches `^\[?(\d{1,3})\]?[ ._-]+` or `^E(\d{1,3})\b` or `\bE(\d{1,3})\b` | season (folder), episode (file) |
| E4 | file matches `^\[(\d{1,3})\]\s*(.+)$` or `^(\d{1,3})[ .-]+(.+)$` **and** ≥2 sibling videos in the folder match the same form | season 1, episode, episodeTitle = group 2 |
| E5 | `\b(\d{4})[.-](\d{2})[.-](\d{2})\b` (dated) | **deferred** (Phase 2); Phase 1 → movie with low confidence |

Anything else → `movie`. In a `movies` library E1–E4 are not applied; in a `shows` library non-matching files become `episode` with season 1 and episode = running number (low confidence).

**Series title**: text before the episode marker in the *file name*, after tag stripping; if empty (E3/E4 forms), the folder name before its season marker/tags. **Year**: first `\b(19|20)\d{2}\b` that appears before the marker (file or folder), only if not followed by `p`. **Episode title**: text after the marker up to the first known tag token (Appendix C) or end; empty if it is only tags.

**Movie title**: from the folder name when the folder holds exactly one non-sample video and the folder name parses with a year (or the file name is "junk" like `movie.mkv`, `video.mkv`, `<folder name>.mkv`); otherwise from the file name. Title = tokens before the first stop token (year, resolution/source/codec tag). If no stop token: the whole cleaned name.

**Title casing**: if the raw title is entirely lowercase, apply word title-case (`arrested development` → `Arrested Development`); otherwise keep as is.

**`series_key`** = `title` lowercased, Unicode NFKD, keep only `[a-z0-9]` → `arresteddevelopment`. Episodes from different folders with the same key merge into one series. When candidates disagree on the display title, keep the one with the most uppercase letters (then the first seen). Series year = first non-zero year seen.

**Fixtures (must pass, exact strings):**

| Path (relative to root) | Expect |
|---|---|
| `Widow's Bay (Season 1) WEB-DL 2160p/Widow's.Bay.S01E03.WEB-DL.2160p.RGzsRutracker.mkv` | episode, "Widow's Bay", S1 E3, key `widowsbay` |
| `Over the Garden Wall (2014) BDRemux 1080p/[07] The Ringing of the Bell.mkv` (with ≥2 `[NN]` siblings) | episode, "Over the Garden Wall", year 2014, S1 E7, episodeTitle "The Ringing of the Bell" |
| `Better Call Saul S04 2160p/Better.Call.Saul.S04E02.WEB-DL.2160p.SDR.HEVC.2xRus.Eng.mkv` | episode, "Better Call Saul", S4 E2, episodeTitle "" |
| `arrested.development.s02.hdtv.720.rus.eng.novafilm.tv/arrested.development.s02e02.hdtv.720.rus.eng.novafilm.tv.mkv` | episode, "Arrested Development", S2 E2, key `arresteddevelopment` |
| `Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E07.Rus.[Ozz].Eng.mkv` | episode, "Arrested Development", S1 E7, same key as above |
| `Pluribus.S01.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP/Pluribus.S01E02.Pirate.Lady.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP.mkv` | episode, "Pluribus", S1 E2, episodeTitle "Pirate Lady" |
| `Movie.Name.2019.1080p.BluRay.x264-GRP/Movie.Name.2019.1080p.BluRay.x264-GRP.mkv` | movie, "Movie Name", 2019 |
| `Some Movie (2021)/Some Movie (2021).mkv` | movie, "Some Movie", 2021 |
| `Another.Film.2018.REPACK.2160p.WEB-DL.DV.HDR10.HEVC.DDP5.1.Atmos-GRP.mkv` (file at root) | movie, "Another Film", 2018 |
| `Show.Name.1x05.Episode.Title.720p.HDTV.mkv` | episode, "Show Name", S1 E5, episodeTitle "Episode Title" |
| `Show Name/Season 02/Show Name - S02E10 - The End.mkv` | episode, "Show Name", S2 E10, episodeTitle "The End" |
| `Show.Name.S01E01E02.720p.mkv` | episode, S1 E1–2 |

### 3.5 Ignore rules (torrent-folder aware)

| Rule | Detail |
|---|---|
| Video extensions (only these are candidates) | `.mkv .mp4 .m4v .mov .webm .avi .ts .m2ts .mts .mpg .mpeg .wmv .flv .ogv .vob`* |
| Incomplete downloads | Skip names ending in `.part .!qB .!ut .crdownload .tmp .download .bts .dctmp .aria2`, and any file whose name starts with `.` or `~` |
| Sample files | Skip files whose name matches `(?i)(^|[ ._-])sample([ ._-]|$)`; skip directories named `(?i)^(sample|samples|extras?|featurettes|behind the scenes|deleted scenes|interviews|trailers?|proof|screens?|screenshots|other)$`; in a folder whose largest video ≥ 200 MB, skip videos < 10 % of the largest |
| Junk | Everything with other extensions (game installs, `.zip`, `.nfo`, extensionless) is ignored by the extension whitelist; directories `$RECYCLE.BIN`, `System Volume Information`, `.git`, `node_modules`, `@eaDir`, `#recycle`, `lost+found` are not entered |
| DVD / Blu-ray folders | A directory named `VIDEO_TS` or `BDMV` (and its parent) is skipped with one `info` log line and counted in the scan report as `unsupported` (*`.vob` outside VIDEO_TS is still a candidate). **Deferred**: Phase 2 plays VIDEO_TS via concat |
| Stability (watcher only) | See §3.8 |
| Size | No minimum size (test fixtures are tiny); the 10 % rule above only applies when the largest sibling ≥ 200 MB |

### 3.6 External subtitles

For a file-backed item at `<dir>/<base>.<ext>`, scan:

- `<dir>/<base>.srt|.vtt|.ass|.ssa`, `<dir>/<base>.<lang>.<ext>`, `<dir>/<base>.<lang>.<forced|sdh|hi|cc>.<ext>` (lang = 2/3-letter code or English name like `english`, `russian`, `turkish`).
- `<dir>/Subs/` and `<dir>/Subtitles/` (case-insensitive) when the folder has exactly one video: every `.srt/.ass/.ssa/.vtt`, language from the file name (`2_English.srt`, `rus.srt`, `English.srt`), else `und`.

Language normalization → ISO 639‑2/B (`eng`, `rus`, `tur`, …); aliases `en/eng/english`, `ru/rus/russian`, `tr/tur/turkish`. `.idx/.sub` (VobSub) and PGS are image subtitles → listed as `isText=false`, not selectable in Phase 1 (**deferred**: burn-in).

### 3.7 Artwork and thumbnails (offline)

| Kind | Local file names (first found wins, case-insensitive, `.jpg/.jpeg/.png/.webp`) | Fallback (extracted with ffmpeg) |
|---|---|---|
| movie poster | `<base>-poster`, `poster`, `folder`, `cover`, `movie` in the movie's folder (only when the folder holds one video) | none (client renders a text card); `thumb` is used as 16:9 art |
| movie backdrop | `<base>-fanart`, `fanart`, `backdrop`, `background` | frame at 25 % of runtime, 1280 px wide |
| movie thumb | `<base>-thumb`, `<base>-landscape` | frame at 10 % (min 2 s), 640 px wide |
| series poster | `poster`, `folder`, `cover` in the series folder (when a dedicated series folder exists) | none (text card); first episode thumb as 16:9 art |
| series backdrop | `fanart`, `backdrop` | backdrop of the first episode |
| season poster | `season01-poster`, `Season 01/poster` | series poster |
| episode thumb | `<base>-thumb`, `<base>` (same base, image ext) | frame at 10 %, 640 px wide |

Random `.jpg` files in scene folders (banners) are ignored unless they match the names above. Extracted images live in `<data>/images/<itemId>/<kind>.jpg`. ffmpeg: `ffmpeg -v error -y -ss <t> -i <file> -map 0:v:0 -an -sn -frames:v 1 -vf scale=<w>:-2 -q:v 4 <out.jpg>` (`-ss` before `-i`; HDR sources get `zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable,zscale=t=bt709:m=bt709:r=tv,format=yuv420p` prepended so thumbnails are not washed out). Resized variants (`?w=`) are produced on demand with `x/image/draw.CatmullRom`, cached at `<data>/cache/images/<itemId>-<kind>-<w>.jpg`, served with `ETag` + `Cache-Control: public, max-age=86400`.

### 3.8 Filesystem watcher (Phase 1)

- One `fsnotify.Watcher` per library; every directory under each root is added (walk at start, new directories on `Create`). Windows `ReadDirectoryChangesW` and Linux inotify are both non-recursive, so the add-every-dir approach is required. Ignored directories (§3.5) are not watched.
- Events are coalesced per path with a **settle timer of 30 s** (reset on every event for that path). After settling: apply ignore rules; require `size > 0`, `mtime` older than 30 s, and that the file opens for reading. Preallocated torrents (qBittorrent default) have a constant size, so **mtime quiescence is the signal**; a stalled torrent may slip through, in which case ffprobe fails/gives wrong duration → the item stays `pending` and is re-evaluated on the next event or periodic scan.
- Renames (`.!qB` → final name) arrive as `Rename`+`Create` → the final name is processed, the old path (if an item) is marked missing.
- `Remove` → item `missing` (not deleted). Directory removal → all items beneath it `missing`.
- Overflow / watcher error → log and schedule a quick scan of that library in 60 s.
- Every watcher-triggered change updates only the affected files (no full walk).

### 3.9 Metadata (Phase 1 = offline)

Title/year/season/episode from names, runtime/streams/chapters from ffprobe, artwork from local files/extracted frames, `overview` empty. **Deferred**: Kodi NFO (`<movie>`/`<tvshow>`/`<episodedetails>` XML) and TMDB (API key in settings, match by title+year, posters/backdrops/overview/genres/ratings, episode titles) in Phase 2. The data model already has `overview`, `premiere_date`, `images.source='provider'` for this.

---

## 4. HTTP API (summary)

Full reference with request/response shapes: [API.md](API.md). Conventions: JSON, camelCase, `/api/v1` prefix, errors as `{"error":{"code","message"}}`, auth via `hs_session` cookie or `Authorization: Bearer <token>` (media endpoints also accept `?token=`), pagination `offset/limit` (`limit` ≤ 200, default 50).

| Group | Endpoints (MVP) |
|---|---|
| System | `GET /system/info` (public), `GET /system/ping`, `POST /system/setup` (first run), `GET /admin/status`, `GET /admin/fs?path=` |
| Auth | `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `PATCH /auth/me/preferences`, `POST /auth/me/password`, `GET /auth/sessions`, `DELETE /auth/sessions/{id}` |
| Libraries | `GET /libraries`, `POST /libraries`, `GET/PATCH/DELETE /libraries/{id}`, `POST /libraries/{id}/scan`, `POST /libraries/{id}/clean`, `POST /libraries/scan`, `GET /tasks`, `GET /tasks/{id}` |
| Items | `GET /items` (filters/search/sort), `GET /items/{id}`, `GET /items/{id}/children`, `GET /items/{id}/image/{kind}`, `GET /items/{id}/file`, `GET /items/{id}/subtitles/{trackId}.vtt`, `POST /items/{id}/play-info`, `POST /items/{id}/progress`, `POST /items/{id}/played` |
| Home | `GET /home` (continue watching, next up, recently added, libraries) |
| Play sessions | `GET /play/{sid}/master.m3u8`, `index.m3u8`, `init.mp4`, `seg{NNNNN}.m4s`, `sub/{trackId}.m3u8`, `sub/{trackId}.vtt`; `POST /play/{sid}/progress`; `DELETE /play/{sid}` |
| Admin | `GET/PATCH /admin/settings`, `GET /admin/play-sessions`, `DELETE /admin/play-sessions/{sid}`, `GET /admin/problems`; users CRUD **deferred** (paths reserved under `/admin/users`) |

---

## 5. Auth, users, sessions

| Topic | Decision |
|---|---|
| Password hashing | Argon2id, `argon2id.Params{Memory: 64*1024, Iterations: 2, Parallelism: 2, SaltLength: 16, KeyLength: 32}`; encoded PHC string in `users.password_hash`. Min length 8 |
| First run | `GET /system/info` → `setupRequired: true` while `users` is empty. `POST /system/setup {username, password, serverName?}` creates the admin and returns a session. Returns 409 once an admin exists. The web client redirects to `#/setup` |
| Sessions | 32 random bytes → base64url token. DB stores `sha256(token)`. Lifetime 90 days sliding (`expires_at` extended when `last_seen_at` is >1 day old). Delivered as `Set-Cookie: hs_session=<token>; Path=/; HttpOnly; SameSite=Lax; Max-Age=7776000` (+`Secure` when the request is HTTPS or `X-Forwarded-Proto: https` from a trusted proxy) **and** as `token` in the login JSON |
| Token transport | `Authorization: Bearer` → cookie → `?token=` (query param accepted **only** on `/items/{id}/file`, `/items/{id}/image/*`, `/items/{id}/subtitles/*`, `/play/*`). Query tokens are redacted in logs |
| CSRF | Cookie-authenticated requests with a method other than GET/HEAD/OPTIONS must carry `Content-Type: application/json` (or `X-Requested-With: HomeStreaming` for `sendBeacon`) — otherwise 403 `csrf`. SameSite=Lax covers the rest |
| Roles | `is_admin`. Admin-only: libraries write, scan, settings, status, fs browser, play-session admin. Phase 1 has exactly one user (the admin) but every query is user-scoped |
| Rate limiting | `/auth/login` and `/system/setup`: per-IP token bucket 10 burst / 10 per minute → 429 + `Retry-After`. Per-username lockout: after 5 consecutive failures lock 60 s, doubling per further failure up to 1 h (`login_attempts` table); successful login resets. Generic unauthenticated endpoints: 120 req/min per IP |
| Client IP | `RemoteAddr`, unless it is in `http.trusted_proxies` → last untrusted hop of `X-Forwarded-For` |
| Logout | Deletes the session row; cookie cleared |
| Device management | `GET /auth/sessions` lists the user's sessions (device name, last seen, IP); any can be revoked |
| Multi-user UI | **Deferred** (Phase 2): admin user CRUD, per-user library access, PIN login for TV |

Preferences (`users.preferences_json`): `{"audioLanguage":"eng","subtitleLanguage":"","subtitleMode":"off"}`; `subtitleMode ∈ off|preferred|always` (`always` = show preferred language or first text track).

---

## 6. Streaming pipeline

### 6.1 Methods

| Method | What the server does | Transport | When |
|---|---|---|---|
| `direct` | Serves the file with HTTP Range (`http.ServeContent`) | `<video src>` progressive | Container + all codecs + bitrate acceptable to the client; selected audio is the stream the browser will pick (first audio or `default`); no image subtitle selected |
| `remux` | ffmpeg `-c:v copy`, audio copy or encode, HLS fMP4, EVENT playlist written by ffmpeg | HLS | Video codec acceptable but container/audio/subtitle selection is not (MKV on iPhone, AC‑3 on Chrome, non-default audio track, …) and bitrate ≤ cap |
| `transcode` | ffmpeg video encode (NVENC or libx264), HLS fMP4, **predictive VOD playlist generated by the server** | HLS | Video codec/profile/bit depth/HDR unsupported, or source bitrate > cap, or image subtitles **deferred** |

Audio within `remux`/`transcode`: copy when the client supports the codec in fMP4 (`aac mp3 ac3 eac3 flac alac`) and (cap is 0/≥ 10000 or the stream is ≤ 256 kbps stereo); else encode to **AC‑3 5.1 @ 640 kbps** when source channels > 2, the client lists `ac3` and the cap is 0/≥ 10000; else **AAC‑LC stereo @ 192 kbps** (`-ac 2`). DTS/TrueHD/DTS‑HD/Opus/Vorbis are therefore always re-encoded for our three clients, and remote (6000 kbps) sessions always carry AAC stereo.

### 6.2 Client capability profile

Sent in `play-info` as `profile`:

```json
{
  "deviceId": "f3c2…", "deviceName": "iPhone", "clientType": "web",
  "platform": "ios" | "webos" | "desktop" | "android" | "other",
  "engine": "native" | "hlsjs",
  "containers": ["mp4", "webm", "matroska"],
  "videoCodecs": [
    {"codec": "h264", "maxLevel": 51, "maxBitDepth": 8, "maxWidth": 3840, "maxHeight": 2160},
    {"codec": "hevc", "maxLevel": 153, "maxBitDepth": 10, "maxWidth": 3840, "maxHeight": 2160, "hdr": ["hdr10", "hlg", "dolbyvision"]},
    {"codec": "vp9"}, {"codec": "av1"}
  ],
  "audioCodecs": ["aac", "mp3", "ac3", "eac3", "flac", "opus"],
  "maxAudioChannels": 6,
  "hlsVideoCodecs": ["h264", "hevc"],
  "maxBitrateKbps": 0,
  "forceTranscode": false
}
```

The client builds it at runtime (`web/src/player/capabilities.ts`):

1. Platform from UA: `Web0S` → `webos`; `iPhone|iPad|iPod` → `ios`; else `desktop`/`android`. Override: `?platform=` / localStorage `hs.platform` (used by tests).
2. Engine: `ios` with `canPlayType('application/vnd.apple.mpegurl')` → `native`; else `Hls.isSupported()` → `hlsjs`; else `native` if `canPlayType` says maybe; else error. Override `hs.engine`.
3. Start from the **platform base profile** (table below), then **confirm each entry** with `MediaSource.isTypeSupported(mime)` (hlsjs engine) or `video.canPlayType(mime)` (native). Entries that return `""`/`false` are removed; additional codecs are added only when the probe says `probably`. HDR via `navigator.mediaCapabilities.decodingInfo({type:'media-source'|'file', video:{contentType, width, height, bitrate, framerate, transferFunction:'pq'|'hlg', colorGamut:'rec2020'}})` when available; errors → keep base.
4. Apply user overrides: max bitrate (Quality menu), force transcode, and the server's `profile_overrides_json` (admin) which can `add`/`remove` codecs per platform.

Base profiles (server-side copy in `internal/stream/profile.go` is the source of truth for validation; the client has the same table):

| Platform | Containers (direct) | Video (direct) | Audio | HLS video | Notes |
|---|---|---|---|---|---|
| `ios` | mp4, mov, m4v | h264 ≤ L5.2 8-bit; hevc ≤ L153 10-bit, `hvc1` tag only, HDR10/HLG/DV8 | aac, mp3, ac3, eac3, alac, flac | h264, hevc | No MKV, no VP9/AV1 (A16 has no AV1 HW), no Opus. Native HLS engine |
| `webos` | mp4 | h264 ≤ L51; hevc ≤ L153 10-bit, HDR10/HLG/DV | aac, mp3, ac3, eac3 (DTS only if runtime probe says so) | h264, hevc | hls.js in the TV browser (MSE). Chrome 79 |
| `desktop` | mp4, webm, matroska (Chromium) | h264 ≤ L51 8-bit; vp9; av1; hevc only if probe says `probably` | aac, mp3, opus, flac, vorbis (ac3/eac3 only if probe) | h264 (+hevc if probe) | Firefox: no matroska |
| `other` | mp4 | h264 | aac, mp3 | h264 | Safe default |

Probe mime strings (client): `video/mp4; codecs="avc1.640033"`, `video/mp4; codecs="hvc1.1.6.L153.B0"`, `video/mp4; codecs="hvc1.2.4.L153.B0"` (Main10), `video/webm; codecs="vp09.00.10.08"`, `video/mp4; codecs="av01.0.08M.08"`, `audio/mp4; codecs="mp4a.40.2"`, `audio/mp4; codecs="ac-3"`, `audio/mp4; codecs="ec-3"`, `audio/mp4; codecs="flac"`, `audio/mp4; codecs="opus"`, `audio/mp4; codecs="dtsc"`, `video/x-matroska; codecs="avc1.640028"`.

### 6.3 Decision engine (`internal/stream/decide.go`)

Inputs: item + streams, requested `audioTrackId` (default: §6.6), `subtitleTrackId`, profile, connection class, `maxBitrateKbps = min(profile.maxBitrateKbps||∞, policy(class))`, `forceTranscode`, hw capabilities. Output: `Decision{Method, Reasons[], VideoAction copy|encode, AudioAction copy|encode, VideoTarget{codec,encoder,w,h,bitrate,hdr}, AudioTarget{codec,channels,bitrate}, SubtitleMode none|sidecar|inband}`.

```
if forceTranscode → transcode(reason "forced")
videoOK  := codecSupported(profile.videoCodecs, v) && bitDepthOK && levelOK && resolutionOK && hdrOK && !(h264 && bitDepth>8)
             && !(hevc && platform==ios && codecTag!="hvc1" && container!="matroska")   // MKV→fMP4 remux re-tags to hvc1
             && !(dvProfile==5)                                                        // DV p5 has no HDR10-compatible base layer
bitrateOK := maxBitrate==0 || item.bitrate <= maxBitrate*1000
audioOK   := audioCodecSupported(selectedAudio) && channels<=profile.maxAudioChannels
subOK     := selectedSub==nil || selectedSub.isText
directOK  := containerSupported && videoOK && bitrateOK && audioOK && subOK && selectedAudioIsBrowserDefault
if directOK → direct
if videoOK && bitrateOK && hlsCodecSupported(v) → remux (audio copy if audioOK else encode)
else → transcode
```

Reason codes (for UI "why is this transcoding?" and tests): `forced`, `container`, `video_codec`, `video_profile_level`, `video_bit_depth`, `video_resolution`, `hdr_unsupported`, `dolby_vision_profile`, `bitrate`, `audio_codec`, `audio_channels`, `audio_track_not_default`, `subtitle_image`, `hls_video_codec`.

Transcode target selection:

| Source | Client | Target |
|---|---|---|
| any SDR | any | H.264 High L4.1 8-bit (`h264_nvenc` or `libx264`), scaled per ladder |
| HDR10/HLG/DV8 (PQ/HLG) | lists `hevc` with `maxBitDepth ≥ 10` and `hdr` containing the source kind, **and** NVENC available | HEVC Main10 **HDR passthrough** (`hevc_nvenc`, p010, BT.2020/PQ VUI kept, `hvc1`), scaled per ladder. No tonemap |
| HDR10/HLG/DV8 | otherwise | H.264 8-bit SDR with **tonemap** (Appendix A) |
| DV profile 5 | any | H.264 SDR with tonemap (colors imperfect in Phase 1; **deferred**: libplacebo DV-aware tonemap) |

Resolution ladder (applied to the longer edge, never upscales; dimensions computed server-side as even integers):

| `maxBitrateKbps` | Max height | Video `-maxrate` |
|---|---|---|
| 0 or ≥ 20000 | source | 0 (CRF/CQ only) or cap−audio |
| 10000–19999 | 2160 | cap − audio |
| 5000–9999 | 1080 | cap − audio |
| 2500–4999 | 720 | cap − audio |
| 1200–2499 | 480 | cap − audio |
| < 1200 | 360 | cap − audio |

Video `-maxrate` = `0.9 × (cap − audio)` (10 % headroom for HLS/fMP4 overhead and relay jitter); `audio` = 640 for AC‑3, 192 for AAC. `-bufsize = 2 × maxrate`. Remote default cap 6000 → 1080p at ~5.2 Mbps video + 192 kbps AAC ≈ 5.4 Mbps total on a 10 Mbps upload.

### 6.4 HLS session mechanics

Directory `<transcode_dir>/<sid>/`. Files: `init.mp4`, `seg%05d.m4s`, ffmpeg's `index.m3u8` (used only for remux), `ffmpeg.log` (stderr tail, last 200 lines kept in memory too).

**Transcode (predictive VOD playlist)** — segment length `L = hls_segment_seconds`, `n = ceil(runtime/L)`:

```
#EXTM3U
#EXT-X-VERSION:7
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:VOD
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-MAP:URI="init.mp4"
#EXTINF:6.000000,
seg00000.m4s
… (last: #EXTINF:<runtime − L·(n−1)>,)
#EXT-X-ENDLIST
```

- ffmpeg is started at segment `S = floor(startPositionMs/1000/L)` with `-ss S·L` **before** `-i`, `-output_ts_offset S·L` (so segment timestamps are absolute — verified: a restart at 12 s yields first PTS 12.000), `-force_key_frames expr:gte(t,n_forced*L)`, `-start_number S`, `-hls_flags independent_segments+temp_file` (segments appear atomically on rename).
- `GET seg{N}.m4s`: if the file exists → serve (`Content-Type: video/iso.segment`). Else let `H` = highest produced segment of the current run, `R` = run start. If ffmpeg is running and `R ≤ N ≤ H+3` → wait (poll 100 ms, up to 30 s) then serve. Otherwise (seek outside the window, or ffmpeg exited) → kill ffmpeg, delete `seg≥N` files, restart at `N`, wait. Backward seeks into already produced segments are served from disk (segments are never deleted while the session lives; **deferred**: prune segments far behind the playhead).
- `init.mp4` is the one produced by the current run (deterministic for the session's encode parameters, so any run's init works).
- Unknown segment numbers (≥ n) → 404.

**Remux (EVENT playlist from ffmpeg)**: ffmpeg runs `-c:v copy` from `startPositionMs` (`-ss` before `-i`), writes `index.m3u8` itself with `-hls_time L -hls_list_size 0 -hls_playlist_type event`; segment boundaries follow source keyframes (6–10 s typical). The server serves that playlist, injecting `#EXT-X-START:TIME-OFFSET=0,PRECISE=YES` after `#EXTM3U` so hls.js/Safari start at the beginning rather than the live edge, and waits until it lists ≥1 segment (max 15 s). Copy runs at disk speed, so the playlist typically completes (gets `#EXT-X-ENDLIST`) within a minute. The session's `timelineOffsetMs = startPositionMs` (0 when starting from the beginning). The client maps `absolute = timelineOffsetMs + video.currentTime·1000`; a seek outside the produced range opens a **new session** (`play-info` with the new `startPositionMs`). The resulting ≤1‑GOP inaccuracy (copy starts at the keyframe before the target) is accepted in Phase 1. **Deferred** (Phase 2): keyframe index from container cues → exact predictive playlists for remux too.

**Master playlist** (`master.m3u8`, server-generated):

```
#EXTM3U
#EXT-X-VERSION:7
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,FORCED=NO,URI="sub/e3.m3u8"   ← only for engine=native
#EXT-X-STREAM-INF:BANDWIDTH=<peak>,AVERAGE-BANDWIDTH=<avg>,CODECS="<codecs>",RESOLUTION=<w>x<h>[,SUBTITLES="subs"][,VIDEO-RANGE=PQ|HLG]
index.m3u8
```

`CODECS` is emitted only when derivable (Appendix B); `VIDEO-RANGE` for HDR outputs. One variant only in Phase 1 (**deferred**: ABR ladder).

**Subtitles in HLS**: `sub/{trackId}.m3u8` is a one-segment VOD playlist (`#EXTINF:<runtime>`, `{trackId}.vtt`, `#EXT-X-ENDLIST`); `sub/{trackId}.vtt` is the full WebVTT shifted by `-timelineOffsetMs` (ffmpeg `-ss` on the subtitle input; verified to shift cues). Only `engine=native` sessions get the `EXT-X-MEDIA` lines (the iPhone's native fullscreen player only shows in-band renditions); hls.js and direct play use a sidecar `<track>` (`/items/{id}/subtitles/{trackId}.vtt?offsetMs=`) and hls.js is configured with `enableWebVTT:false, enableCEA708Captions:false`.

**Lifecycle**: `lastActivity` is updated by every playlist/segment/progress request. A session with no activity for **60 s** is stopped (ffmpeg killed, directory deleted). `DELETE /play/{sid}` stops immediately. On startup `<transcode_dir>` is wiped. Max concurrent HLS sessions = `transcode_max_sessions`; beyond that `play-info` returns 503 `transcode_busy` (direct sessions are unlimited). Hardware fallback ladder per session: `hw-decode+hw-encode` → `sw-decode+hw-encode` → `software`; each ffmpeg exit with non-zero status before producing the first segment triggers the next rung (logged with the stderr tail), so a broken GPU driver never blocks playback.

### 6.5 Hardware acceleration

`transcode.hwaccel = auto | none | nvenc | qsv | amf | vaapi | videotoolbox` (config.toml/env; shown in admin status). `auto` preference order: nvenc → qsv → amf → vaapi → videotoolbox → none; a backend is selected only if its **self-test** passes at startup (and every 10 min if it failed, so a late driver load is picked up):

1. `ffmpeg -encoders` lists the encoder (`h264_nvenc`, `hevc_nvenc`, …).
2. Encode test: `ffmpeg -v error -f lavfi -i testsrc2=size=256x144:rate=10 -frames:v 3 -c:v h264_nvenc -f null -` exit 0.
3. Decode test: a 10-frame H.264 and HEVC Main10 sample generated once into `<data>/cache/selftest/` (libx264/libx265 if present, else decode test skipped); `ffmpeg -v error -hwaccel cuda -hwaccel_output_format cuda -i sample.mp4 -f null -` exit 0 → `hwDecode[h264|hevc] = true`.

Pipelines (concrete flags in Appendix A):

| Backend | Decode | Scale | Tonemap (HDR→SDR) | Encode |
|---|---|---|---|---|
| `nvenc` (this PC) | NVDEC `-hwaccel cuda -hwaccel_output_format cuda` when the source codec passed the decode test, else CPU | `scale_cuda` (GPU) | GPU scale to target size → `hwdownload` → CPU `zscale/tonemap` chain at **output** resolution (cheap at 1080p on 16 cores) | `h264_nvenc` / `hevc_nvenc` (p4, vbr, cq) |
| `qsv` / `amf` / `vaapi` / `videotoolbox` | CPU in Phase 1 | CPU | CPU | `h264_<backend>` (encode-only; args table in Appendix A). **Deferred**: full hardware pipelines |
| `none` | CPU | `scale` | CPU `zscale/tonemap` | `libx264` |

Rationale: stock ffmpeg has no CUDA→OpenCL/Vulkan tonemap interop that works reliably; CPU tonemap after GPU downscale is the robust option. **Deferred** (Phase 2): `libplacebo` (Vulkan) tonemapping incl. Dolby Vision p5, `tonemap_opencl`, AV1 NVENC output.

### 6.6 Audio and subtitle track selection

Default audio (server, when the client sends no `audioTrackId`): first audio stream whose normalized language equals the user's `audioLanguage` (default `eng`); else the stream with `default` disposition; else the first audio stream. Language normalization uses the stream `language` tag, then title heuristics for untagged Russian-release tracks: title matches `(?i)rus|рус|russian|lostfilm|novafilm|kubik|кубик|hdrezka|amedia|newstudio|jaskier|ozz|dub` → `rus`; `(?i)\beng|english|original|orig` → `eng`; `(?i)tur|türk|turkish` → `tur`. Default subtitle: `subtitleMode=off` → none; `preferred` → first text subtitle matching `subtitleLanguage`, plus forced subtitles in that language; `always` → preferred or first text track.

Switching a track during playback = `play-info` again with the new ids, `startPositionMs = current` and `replacesPlaySessionId = <old>` (new session; the old one is stopped atomically). For direct play, choosing a non-default audio track forces `remux`.

Embedded text subtitle codecs treated as text: `subrip, ass, ssa, webvtt, mov_text, text`. Conversion: `ffmpeg -v error -i <file> -map 0:<idx> -f webvtt -` (external: `-i <sub>`; on decode error retry with `-sub_charenc WINDOWS-1251` for `rus`, else `WINDOWS-1252`). Cached at `<data>/cache/subtitles/<itemId>-<trackId>.vtt`; offset variants generated on demand and not cached.

### 6.7 Direct file serving

`GET /items/{id}/file`: `http.ServeContent` with the file's `mtime` (handles `Range`, `If-Range`, `If-Modified-Since`), `Accept-Ranges: bytes`, `Content-Type` by extension (`mp4→video/mp4`, `m4v→video/x-m4v`, `mov→video/quicktime`, `mkv→video/x-matroska`, `webm→video/webm`, `avi→video/x-msvideo`, `ts/m2ts→video/mp2t`), `Content-Disposition: inline`, `Cache-Control: private, max-age=0`. Reads use a 256 KiB buffer; no write timeout on this route (per-request context only).

### 6.8 Connection class and bitrate policy

`class = lan` if the client IP is loopback, link-local, RFC1918, ULA `fc00::/7`, or (when `tailnet_is_lan`) `100.64.0.0/10`; otherwise `remote` (Tailscale peers are `100.x` → `remote` by default, which is what we want on cellular/DERP). `policy(class)` = the matching `*_max_bitrate_kbps` setting. The applied cap is returned in `play-info.connection.maxBitrateKbps` and shown in the player's Quality menu as "Auto (6 Mbps, remote)". Direct play and remux over `remote` are allowed only when the file bitrate ≤ cap (so a 60 Mbps 4K remux never goes out over the 10 Mbps upload). The LAN (1 Gbps wired) direct-plays everything. **Deferred**: hls.js bandwidth-estimate-driven ABR with a multi-variant master.

---

## 7. Client application

### 7.1 Modes and detection

One bundle, two layouts: **default** (mouse/touch; responsive from 360 px phones to desktop) and **TV mode** (10‑foot UI, D‑pad focus navigation, larger type, no hover affordances). TV mode is on when the UA contains `Web0S`, or `?tv=1`, or localStorage `hs.tvMode=1` (toggle in Settings). iPhone PWA: `display: standalone`, `apple-mobile-web-app-capable`, `viewport-fit=cover`, safe-area insets, `playsinline` on the video element.

Server URL: `apiBase = ''` (same origin) when served by the server. When `location.protocol === 'file:'` (Phase 2 IPK) the app shows `#/connect` to enter `http://host:8420`, stores it in localStorage and uses Bearer tokens + `?token=` media URLs (CORS is open for the API; cookies are not used cross-origin).

### 7.2 Screens (hash routes)

| Route | Content |
|---|---|
| `#/setup` | First-run wizard: (1) ffmpeg status check (green/red with the install hint `winget install Gyan.FFmpeg` and a path field), (2) admin username/password, (3) add first library: name, type (`mixed` default), path via the server directory browser (drives on Windows; preselect suggestion if `F:\torrent` exists), (4) "Scan now" → home |
| `#/login` | Username/password; device name auto (UA summary); error states incl. lockout countdown |
| `#/` | Home: rows "Continue Watching", "Next Up", "Recently Added" (movies and episodes, merged, newest first), one row per library ("Movies", "Shows" sections for a `mixed` library) |
| `#/library/:id?tab=movies\|shows` | Grid of posters (2:3 cards; text card when no poster), sort (title/added/year), search box (server `search=`), infinite scroll (`limit=60`), "Problems" badge count for admins |
| `#/item/:id` | Movie: backdrop, poster/thumb, title, year, runtime, codec/HDR/audio-language badges, Play/Resume, audio + subtitle pickers (defaults from preferences), "Mark played". Series: seasons list → episodes list with thumbs, progress bars, "Next up" highlight. Season/episode details inline |
| `#/play/:id?audio=&sub=&t=` | Fullscreen player overlay (see 7.3) |
| `#/search?q=` | Results grid across all libraries |
| `#/settings` | Account (password), Preferences (audio/subtitle language, subtitle mode), Playback (quality per device: Auto / 20 / 10 / 8 / 4 / 2 / 1 Mbps; force transcode toggle; engine override auto/native/hlsjs), Devices (sessions list, revoke), TV mode toggle |
| `#/admin` | Server status (version, ffmpeg path/version, hwaccel backend + self-test results, active play sessions with method/reasons/bitrate, scan tasks), Libraries (add/edit paths/scan/clean/delete), Settings (bitrate caps, max sessions, scan interval, preset), Problems (pending/missing items with reasons) |
| `#/connect` | Server URL entry (only on `file:` origin; Phase 2) |

Components: `Row` (horizontal scroller with focus memory), `Card`, `FocusGrid`, `Modal`/`Menu` (focus-trapped), `Toast`, `ProgressBar`, `Badge`.

### 7.3 Player

- Element: single `<video playsinline preload="metadata">`; custom controls (no native controls except the iPhone native fullscreen, entered with `webkitEnterFullscreen()`; desktop/TV use the Fullscreen API on the player container).
- Engine: `native` → `video.src = masterUrl` (HLS) or file URL; `hlsjs` → `new Hls({ startPosition, fragLoadingTimeOut: 60000, manifestLoadingTimeOut: 30000, enableWebVTT: false, enableCEA708Captions: false, maxBufferLength: 60, backBufferLength: 30 })`.
- Timeline: `absolute = timelineOffsetMs + currentTime·1000`; `duration` from the item (`runtimeMs`), never from `video.duration` (EVENT playlists grow). Seek to `A`: if `timelineOffsetMs == 0` → `video.currentTime = A/1000`; else if `A` is inside `[timelineOffsetMs, timelineOffsetMs + seekableEnd]` → `currentTime = (A − offset)/1000`; else → new session at `A` (same audio/sub ids), swap source, continue.
- Start: `play-info` with `startPositionMs` = resume position (Resume) or 0 (Play from start); `direct`/`transcode` → seek to `startPositionMs` on `loadedmetadata`; `remux` → `startPosition 0` (session already starts there).
- Controls: play/pause, seek bar (buffered + played, absolute times), ±10 s, time, volume (desktop), audio menu, subtitle menu (Off + tracks), quality menu (Auto/… + "why: transcoding (bitrate, hdr_unsupported)"), fullscreen, back. Auto-hide after 3 s.
- Keyboard: Space/K play-pause, ←/→ ±10 s, ↑/↓ volume (desktop), M mute, F fullscreen, C subtitles toggle, Esc back.
- Progress: `POST /play/{sid}/progress {positionMs, state}` every 10 s while playing, on pause/seek/ended, and on `pagehide` via `navigator.sendBeacon` (Blob `application/json`). `ended` → `positionMs = runtime`, server marks played. Next episode: on `ended` for an episode, show "Up next" card (10 s countdown, TV: focused) → autoplay next (`nextUp` from the series detail).
- Subtitles: `engine=native` → in-band (select via `video.textTracks` by `language`+`label`, set `mode='showing'`); otherwise sidecar `<track default kind="subtitles" src=…>`; track switch → new session only if the mode is in-band, else just swap the `<track>`.
- Errors: media error → toast + automatic one-time retry with `forceTranscode: true`; 503 `transcode_busy` → message.

### 7.4 TV mode (LG webOS 6 browser, Phase 1; IPK later)

- **Spatial navigation** (`web/src/nav/spatial.ts`, no library): focusable elements carry `data-focusable`; on Arrow keys the manager picks the best candidate in that direction by geometric scoring (overlap on the perpendicular axis first, then Euclidean distance from the leading edge); sections (`data-nav-section`) remember their last focused child; `Enter` clicks; `Back` (keyCode **461** on webOS, also `Escape`/`Backspace`) pops navigation or closes menus; the player maps Left/Right to ±10 s, Up to show controls / focus the control bar, Down to hide, Enter to play/pause; media keys 415 play, 19 pause, 413 stop, 412 rewind, 417 fast-forward, 403–406 colour keys unused. Magic Remote pointer works as a mouse (hover sets focus).
- Visual: `:focus` ring (2 px accent outline + 1.04 scale on cards; `:focus` not `:focus-visible` because Chrome 79), 32–40 px base font at 1080p (`html{font-size:…}` scaled by viewport), rows scroll the focused card into view (`scrollIntoView({block:'nearest', inline:'center'})`), safe margins 5 %.
- Text entry uses the TV's own on-screen keyboard (native `<input>`).
- Chrome 79 constraints: see Appendix D (no flex `gap`, no `aspect-ratio`, no `inset`, no `?.`/`??` in output, no top-level await, no `:focus-visible`, no `dialog`).
- Phase 1 acceptance uses the TV's built-in browser at `http://192.168.0.13:8420/?tv=1` (also auto-detected). **Deferred**: webOS IPK (`clients/webos/appinfo.json`, `@webos-tools/cli` `ares-package`), which removes browser chrome and keeps the app in the launcher.

### 7.5 Resume / continue watching

- `user_item_data.position_ms` updated by progress pings. In-progress when `position > max(5000, 2 % runtime)` and `position < played_threshold_percent % runtime`; ≥ threshold → `played=1, position=0, play_count++`.
- Home "Continue Watching": movies/episodes in progress, ordered by `last_played_at desc` (limit 20). "Next Up": for each series with ≥1 played episode and no in-progress episode, the first unplayed episode after the last played one in (season, episode) order; series with nothing played are excluded.
- Item page shows "Resume from 1:02:33" and "Play from beginning".

---

## 8. Remote access and security

### 8.1 Network reality and decision

Facts: the ISP (Turksat cable, 100/10 Mbps DOCSIS 3.0) puts the router behind **CGNAT** — the Infinity 401's WAN address is `100.109.73.201` (inside `100.64.0.0/10`) and there is no global IPv6. Inbound port forwarding from the internet is therefore **impossible**; UPnP mappings on the router would only reach the carrier's NAT. Tailscale is already installed on the server PC and on the iPhone.

**Default and only built-in remote path: Tailscale.** The server binds `0.0.0.0:8420` (all interfaces, including the Tailscale adapter); the Windows Firewall rule is created for all profiles (§9.5) so traffic from the tailnet interface is accepted. Why Tailscale: WireGuard-encrypted overlay that traverses CGNAT (direct UDP hole-punching when possible, DERP relays otherwise), no router changes, no public exposure, MagicDNS names, free personal plan, iOS app with on-demand VPN. Limitations accepted: (1) every remote device needs the Tailscale app — the **LG TV cannot run it and stays LAN-only**, which is fine; (2) when a direct connection cannot be established, traffic goes through Tailscale's DERP relays whose throughput is limited and variable — one more reason the remote cap defaults to **6000 kbps** (and the 10 Mbps upload is the hard ceiling anyway).

**Setup checklist** (`deploy/tailscale.md`; both devices are already on the tailnet):

1. On the PC: Tailscale menu → *Preferences* → enable **"Run unattended"** (otherwise the tunnel stops when nobody is logged in). In the Tailscale admin console disable **key expiry** for the PC node.
2. `tailscale status` on the PC → note the machine name (e.g. `desktop-pc`) and the `100.x.y.z` address.
3. Install/start HomeStreaming; confirm the firewall rule exists for all profiles (created by `service install`, or allow it in the first-run prompt and then add `profile=any` with the `netsh` command from `deploy/windows.md`).
4. iPhone: with Tailscale connected, open `http://<machine-name>:8420` (MagicDNS) or `http://100.x.y.z:8420`; sign in; Share → **Add to Home Screen** (PWA). The connection class shows as `remote` → "Auto (6 Mbps)".
5. Optional HTTPS with a valid name (needs HTTPS certificates enabled for the tailnet in the admin console). Simplest: `tailscale serve --bg http://127.0.0.1:8420` publishes `https://<machine>.<tailnet>.ts.net` with Tailscale-managed Let's Encrypt certificates and proxies to the server; then set `http.trusted_proxies = ["127.0.0.1"]` so the server reads the real tailnet client IP from `X-Forwarded-For`. Alternative: `tailscale cert <machine>.<tailnet>.ts.net` and point `tls.cert_file/key_file` at the generated files (renew with a scheduled task every 60 days). HTTPS is a convenience (service worker, Secure cookies), not a playback requirement.
6. LAN: the TV and the iPhone at home use `http://192.168.0.13:8420` (or `http://192.168.0.5:8420` after assigning the recommended static IP outside the DHCP pool `.10–.254`). Windows 11 also answers mDNS for its hostname, so `http://<pcname>.local:8420` usually works from the iPhone; do not rely on it for the TV. **Deferred** (Phase 2): the server advertises `homestreaming.local` itself.

**Rejected: Cloudflare Tunnel.** It would traverse CGNAT without a client app, but the Cloudflare free plan's terms prohibit serving video through the proxy; not an option for a media server.

**Rejected for this ISP: port forward + built-in ACME.** Unusable behind CGNAT. The code path (`[tls]` with provided certs in Phase 1; ACME in Phase 2) is kept because it becomes relevant on a VPS relay or after an ISP change.

**Later alternative for devices that cannot run Tailscale (Phase 3, documented not built): VPS relay.** A small VPS (public IPv4) runs WireGuard; the PC keeps a WireGuard tunnel to it (outbound only, so CGNAT is irrelevant); on the VPS a reverse proxy (Caddy) with Let's Encrypt terminates `https://media.example.com` and forwards to the PC over the tunnel. The server then runs with `http.trusted_proxies` = the VPS tunnel IP, `redirect_https` off (TLS at the VPS), rate limiting on the real client IP. Bandwidth is still bounded by the 10 Mbps upload.

**Phase 2 "Remote access" admin page**: shows tailnet detection (is a `100.x` interface present? Tailscale machine name via `tailscale status --json` when the CLI is found), the current connection class of the browser, a copy of the checklist above, and a one-click re-check of the CGNAT status (public IP from `https://api.ipify.org` vs. router WAN IP via UPnP `GetExternalIPAddress`) for the day the ISP changes. No UPnP port mapping is implemented.

### 8.2 Security measures (Phase 1)

| Measure | Detail |
|---|---|
| Auth everywhere | All `/api/v1` routes except `system/info`, `system/ping`, `system/setup`, `auth/login` require a session |
| Passwords | Argon2id; min 8 chars; lockout + per-IP limiter (§5) |
| Cookies | HttpOnly, SameSite=Lax, Secure on HTTPS; CSRF rule for cookie auth |
| Headers | `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY`, `Content-Security-Policy: default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; worker-src 'self' blob:; connect-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'`; HSTS (`max-age=31536000`) only on HTTPS |
| CORS | `Access-Control-Allow-Origin: *` for `/api/v1/*` **without** credentials (needed by the Phase 2 `file://` IPK; cookies never cross origins) |
| Path safety | The fs browser (`/admin/fs`) is admin-only; library paths are validated to be absolute directories; `rel_path` joined with `filepath.Join` and verified to stay under the root; subtitle/image paths come from DB rows only |
| Query tokens | Only on media routes; redacted in logs; never in `Referer` (same-origin + `Referrer-Policy`) |
| Transcode dir | Session ids are random 128-bit; segment names validated by regex |
| Process | ffmpeg started without a shell; arguments built from typed structs; file paths passed as single argv entries; `-nostdin` |
| Logging | No passwords/tokens; client IP + method + path + status + duration at `info` for API, `debug` for segments |
| Updates | Version in `system/info`; **deferred**: update check |

---

## 9. Configuration, data directory, logging, packaging, migration

### 9.1 Configuration

Resolution order: CLI flags > env `HS_*` > `config.toml` > defaults. `config.toml` lives in the data dir; a commented default is written on first start if absent.

```toml
# <data>/config.toml
[http]
addr = ":8420"                # HTTP listen address; ":8420" = all interfaces incl. the Tailscale adapter
trusted_proxies = []          # e.g. ["127.0.0.1"] when fronted by `tailscale serve`, or a VPS relay IP
redirect_https = false
public_url = ""               # optional, e.g. "https://desktop-pc.tail1234.ts.net" (used in share/QR later)

[tls]                         # optional HTTPS (Phase 1: provided certs, e.g. from `tailscale cert`; Phase 2: acme)
addr = ":8443"
cert_file = ""
key_file = ""
# [tls.acme] domain = "", email = ""     # Phase 2 — only meaningful with a public IP (VPS relay / other ISP)

# [mdns] enabled = true  name = "homestreaming"   # Phase 2 — advertises homestreaming.local on the LAN

[ffmpeg]
path = ""                     # empty = auto-locate (see 9.2)
ffprobe_path = ""

[transcode]
dir = ""                      # empty = <data>/transcode ; can be any drive, e.g. "D:\\hs-transcode"
hwaccel = "auto"              # auto|none|nvenc|qsv|amf|vaapi|videotoolbox
hw_decode = true              # allow NVDEC/… when the self-test passes

[log]
level = "info"                # debug|info|warn|error
file = true                   # <data>/logs/homestreaming.log, 20 MB × 5 rotated
```

| Env | Maps to |
|---|---|
| `HS_DATA_DIR` | data dir (flag `--data-dir`) |
| `HS_HTTP_ADDR`, `HS_TLS_ADDR`, `HS_TLS_CERT_FILE`, `HS_TLS_KEY_FILE` | `[http]`/`[tls]` |
| `HS_FFMPEG_PATH`, `HS_FFPROBE_PATH` | `[ffmpeg]` |
| `HS_TRANSCODE_DIR`, `HS_HWACCEL` | `[transcode]` |
| `HS_LOG_LEVEL` | `[log]` |

Everything else (bitrate caps, preset, scan interval, server name, profile overrides) is in the DB `settings` table and edited from the admin UI.

### 9.2 ffmpeg location and the missing-ffmpeg experience

Locator order (first executable wins): `ffmpeg.path` config/env → `<data>/bin/ffmpeg(.exe)` → `<exe dir>/ffmpeg(.exe)` and `<exe dir>/ffmpeg/bin/ffmpeg(.exe)` → `PATH` (`exec.LookPath`) → Windows well-known globs: `C:\Users\*\AppData\Local\Microsoft\WinGet\Packages\Gyan.FFmpeg*\ffmpeg-*\bin\ffmpeg.exe` and `C:\Users\*\AppData\Local\Microsoft\WinGet\Links\ffmpeg.exe` (every profile, because winget installs per user and adds only the *user* PATH, which a service running as LocalSystem does not see), `C:\ffmpeg\bin\ffmpeg.exe`, `C:\ProgramData\chocolatey\bin\ffmpeg.exe` → macOS `/opt/homebrew/bin`, `/usr/local/bin`. Newest version wins when a glob matches several. `ffprobe` is looked up next to the found `ffmpeg` first. Version is parsed from `ffmpeg -version` (require ≥ 5.1; warn < 6.1). The resolved paths are shown in the setup wizard (editable) and in admin status; the wizard writes a confirmed path into `config.toml` so later service runs do not depend on the locator.

When missing: startup logs `ERROR ffmpeg not found — install with: winget install Gyan.FFmpeg (then restart) or set ffmpeg.path`; `GET /system/info` has `ffmpeg: {found:false}`; the setup wizard and admin status show a red banner with the same instructions; library scans still run (items stay `pending` with reason `ffmpeg_missing`) and play attempts return 503 `ffmpeg_missing`. `homestreaming doctor` prints: data dir, config path, ffmpeg/ffprobe path+version, encoders found, hwaccel self-test results, listen addresses with LAN IPs and tailnet IPs, firewall hint. **Deferred**: `homestreaming ffmpeg download` (BtbN/gyan static builds into `<data>/bin`).

### 9.3 Data directory

| OS | Default |
|---|---|
| Windows | `C:\ProgramData\HomeStreaming` (same for console and service runs) |
| Linux | `/var/lib/homestreaming` when running as root/service, else `$XDG_DATA_HOME/homestreaming` (`~/.local/share/homestreaming`) |
| macOS | `~/Library/Application Support/HomeStreaming` |
| Docker | `/data` (volume) |

```
<data>/
  config.toml
  homestreaming.db  (+ -wal, -shm)
  images/<itemId>/{poster,backdrop,thumb}.jpg      extracted art (local art is referenced in place)
  cache/images/<itemId>-<kind>-<w>.jpg             resized variants (safe to delete)
  cache/subtitles/<itemId>-<trackId>.vtt           converted subtitles (safe to delete)
  cache/selftest/                                  hw self-test samples
  transcode/<sid>/                                 ephemeral (wiped on start) — overridable to another drive
  logs/homestreaming.log(.N)
  bin/                                             optional ffmpeg drop-in location
```

### 9.4 Logging

`slog` text handler to stderr (console) and JSON to the rotating file. Levels per §8.2. Each HLS session logs start/stop/restart with the full ffmpeg argv at `debug`; the last 200 stderr lines of a session are kept in memory and shown in admin play-session details and in `ERROR` logs on failure. Scan reports: counts of added/updated/missing/pending/ignored-sample/ignored-incomplete/unsupported.

### 9.5 Build and packaging

```
make web        # cd web && npm ci && npm run build  → web/dist
make build      # go build -trimpath -ldflags "-s -w -X main.version=$(git describe)" -o bin/homestreaming ./cmd/homestreaming
make dist       # scripts/build-dist.sh: windows/amd64 (.exe), linux/amd64, linux/arm64, darwin/arm64 → dist/homestreaming-<os>-<arch>[.exe] + .zip/.tar.gz
make test       # go test ./...
make test-e2e   # scripts/e2e.sh (see §10)
make docker     # docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile .
```

- **Dockerfile** (multi-stage): `node:22` builds the client → `golang:1.24` builds the binary (`CGO_ENABLED=0`, `GOARCH` from `TARGETARCH`) → runtime `ubuntu:24.04` + `apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata` (ffmpeg 6.1.1, identical to the dev container), user `hs` (uid 1000), `VOLUME /data`, `EXPOSE 8420`, `ENV HS_DATA_DIR=/data`, `ENTRYPOINT ["/usr/local/bin/homestreaming"]`. `deploy/docker-compose.yml` mounts `/data` and `/media:ro`, with commented `devices:` lines for `/dev/dri` (VAAPI/QSV) and the `nvidia` runtime.
- **Windows** (`deploy/windows.md`): unzip to `C:\Program Files\HomeStreaming\`; `homestreaming.exe` runs in a console (first run prompts Windows Defender Firewall → tick both Private and Public so the Tailscale adapter is covered; the LAN profile is Private). Service: `homestreaming service install [--user .\username --password …]` (via `kardianos/service`; runs as LocalSystem by default — fine for local drives such as `F:\`; use `--user` for SMB shares), then `service start`; `service install` also runs `netsh advfirewall firewall add rule name="HomeStreaming" dir=in action=allow protocol=TCP localport=8420 profile=any` when elevated (printed as a hint otherwise). Logs go to the data dir (the service has no console). Network hygiene documented there: assign the PC a static IP outside the DHCP pool (e.g. `192.168.0.5/24`, gateway `192.168.0.1`) or a DHCP reservation on the router, so TV/iPhone bookmarks and (Phase 2) DLNA stay stable; disable sleep while the PC is a server (**deferred**: `SetThreadExecutionState` during active sessions); Tailscale "Run unattended" (§8.1).
- **Linux**: `deploy/homestreaming.service` (`User=homestreaming`, `ExecStart=/usr/local/bin/homestreaming --data-dir /var/lib/homestreaming`, `SupplementaryGroups=video render`, `Restart=on-failure`); or `homestreaming service install` which writes the same unit.
- Startup banner prints all listen URLs including LAN IPv4s (e.g. `http://192.168.0.13:8420`).

### 9.6 Migration path (PC → Linux/ARM64 box or NAS)

1. Stop the service on Windows. Copy `C:\ProgramData\HomeStreaming` (DB is checkpointed on shutdown) to the new host (`/var/lib/homestreaming` or the Docker `/data` volume). Skip `transcode/` and `cache/` (regenerated).
2. Start the new binary/container. Open Admin → Libraries → edit each path (`F:\torrent` → `/media/torrent`). Rescan. Items, progress and art are preserved because items are keyed by `rel_path` and ids do not change.
3. Set `transcode.hwaccel` for the new hardware (`vaapi`/`qsv`/`none`); Phase 2 adds full pipelines for those.
4. Point Tailscale/DNS at the new host; clients keep working (same URL if the Tailscale machine name is reused).

---

## 10. Testing strategy (runs in this container)

| Layer | Tooling | What it covers |
|---|---|---|
| Unit (Go) | `go test ./internal/...` | naming parser with the fixtures of §3.4 and Appendix C; ignore rules; language normalization; decision engine truth table (profiles × sources → method/reasons/targets); playlist generation (predictive VOD, master, subtitle); CODECS string mapping; ffmpeg argv builders for software and NVENC (string comparison, no GPU needed); auth (argon2, token hashing, lockout math); client IP/class; config precedence |
| API (Go) | `httptest.NewServer(app.Router)` on a temp data dir with media generated by `internal/testutil` (ffmpeg lavfi `testsrc2`/`sine`; cached once per `go test` run in `TestMain`). Skips with `t.Skip` if ffmpeg is absent | setup → login → cookie/Bearer/query token → library create/scan → items/children/search → images (ETag) → `play-info` decisions with the three profile types → direct Range (206, partial content) → HLS transcode: playlist shape, `seg00000.m4s` served, seek to `seg00010.m4s` triggers restart and serves → remux EVENT playlist contains `EXT-X-START` and reaches `ENDLIST` → subtitles `.vtt` content and `offsetMs` shift → progress/resume/played thresholds → session idle cleanup (timeout shortened via a test hook) → watcher: copy a file into the library, expect the item within 5 s (settle time shortened via hook), `.part` file ignored, `sample` ignored → rate limit + lockout → CSRF rule |
| HLS validity | `ffmpeg -v error -i http://127.0.0.1:<port>/api/v1/play/<sid>/master.m3u8?token=… -f null -` exit 0 | Acts as a strict native-player-style consumer for both transcode and remux sessions |
| E2E (Playwright) | `e2e/` with `@playwright/test@1.63.0`, `PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers` (fallback `executablePath=/usr/bin/google-chrome`), launch args `--autoplay-policy=no-user-gesture-required`. `scripts/e2e.sh`: build web + binary, `scripts/make-test-media.sh` → temp library, start the binary on a free port with a temp data dir, run tests, stop | Desktop: setup wizard → add library → scan completes (task polling) → home shows rows → library grid → item page → Play H.264 MP4 (direct; `currentTime` advances ≥ 2 s, `play-info` method asserted via request interception) → Play HEVC‑10bit HDR MKV (transcode, tonemap path) → Play AC‑3 MKV (remux + audio encode) → seek to 80 % (transcode restart) → reload → "Continue Watching" shows the item with the saved position → Resume → audio track switch (Rus→Eng) → subtitle on (external .srt) with a rendered cue → mark played → "Next Up" moves to E02. iPhone (`devices['iPhone 13']` + `hs.engine=hlsjs` because Chromium has no native HLS): layout at 390 px, PWA manifest present, inline playback, controls usable by touch. webOS (`userAgent` with `Web0S … Chrome/79`, viewport 1920×1080): TV mode auto-on, ArrowRight/Down/Enter navigate home → library → item → play; Back (keyCode 461) returns; player D‑pad seek. ES target check: `scripts/check-es-target.mjs` parses every `web/dist/assets/*.js` with `acorn` at `ecmaVersion: 2019` |
| Test media | `scripts/make-test-media.sh <dir>` and the Go equivalent in `internal/testutil` generate (20–30 s each, `testsrc2` + `sine`, two audio tracks tagged `rus`/`eng` where noted) the tree below | Mirrors the real naming |

Test media tree (also the parser E2E fixture; durations 20 s unless noted):

```
Movies-and-TV/                                         ← one `mixed` library root
  Movie.Name.2019.1080p.BluRay.x264-GRP/Movie.Name.2019.1080p.BluRay.x264-GRP.mp4     h264+aac, faststart → direct (Chromium)
  Movie.Name.2019.1080p.BluRay.x264-GRP/Sample/movie.name.2019.sample.mp4             ignored
  Movie.Name.2019.1080p.BluRay.x264-GRP/movie.name.nfo, RARBG.txt, banner.jpg          ignored
  Another.Film.2018.2160p.WEB-DL.DV.HDR10.HEVC-GRP.mkv                                hevc main10 PQ/BT.2020 + aac → transcode+tonemap
  Third Movie (2020)/Third Movie (2020).mkv                                            h264 + ac3 5.1 (rus) + aac (eng) → remux (audio encode on Chromium), audio switch test
  Third Movie (2020)/poster.jpg, fanart.jpg                                            local art (generated with lavfi color)
  Fourth.Movie.2017.720p.WEBRip.VP9.webm                                               vp9+opus → direct
  Widow's Bay (Season 1) WEB-DL 2160p/Widow's.Bay.S01E03.WEB-DL.2160p.RGzsRutracker.mkv
  Over the Garden Wall (2014) BDRemux 1080p/[07] The Ringing of the Bell.mkv
  Over the Garden Wall (2014) BDRemux 1080p/[08] Babes in the Wood.mkv
  Better Call Saul S04 2160p/Better.Call.Saul.S04E02.WEB-DL.2160p.SDR.HEVC.2xRus.Eng.mkv   (h264 in tests; 3 audio tracks rus,rus,eng; embedded subrip eng)
  arrested.development.s02.hdtv.720.rus.eng.novafilm.tv/arrested.development.s02e02.hdtv.720.rus.eng.novafilm.tv.mkv
  Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E07.Rus.[Ozz].Eng.mkv
  Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E07.Rus.[Ozz].Eng.srt     external subtitle (und)
  Arrested.Development.S01.720p.HDTVRip/Arrested.Development.S01E08.Rus.[Ozz].Eng.mkv
  Pluribus.S01.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP/Pluribus.S01E02.Pirate.Lady.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP.mkv
  Pluribus.S01.ATVP.DV.HDR.WEB-DL.2160p.by.AKTEP/Subs/English.srt
  Downloading.Show.S01E01.1080p.mkv.!qB                                                ignored (incomplete)
  Other.Movie.2016.mkv.part                                                            ignored
  Some DVD (1999)/VIDEO_TS/VTS_01_1.VOB, VIDEO_TS.IFO                                 skipped, counted unsupported
  Some Game/game.exe, UnityPlayer.dll, data.assets, readme                             ignored
  archive.zip, NOEXT                                                                   ignored
```

Expected scan result: 4 movies; series "Widow's Bay" (S1: E3), "Over the Garden Wall" (2014, S1: E7, E8), "Better Call Saul" (S4: E2), "Arrested Development" (S1: E7, E8; S2: E2 — one series), "Pluribus" (S1: E2); 0 missing; 2 ignored-incomplete; 1 unsupported.

Note on Chromium in CI: HEVC and AC‑3 are unsupported in the bundled Chromium (verified), which is exactly what makes the transcode and remux paths exercisable without hardware. NVENC paths are covered by argv unit tests only; the software fallback ladder is tested by configuring `hwaccel=nvenc` in a test and asserting the session falls back to `software` after the self-test fails.

---

## Appendix A — ffmpeg command templates

Common prefix: `ffmpeg -nostdin -hide_banner -loglevel warning -y`. `<L>` = segment seconds (6). `<T>` = start seconds (multiple of L for transcode). Placeholders in `<>`.

**Probe**
```
ffprobe -v error -print_format json -show_format -show_streams -show_chapters -- <file>
```

**Thumbnail / backdrop** (`<t>` = 10 % or 25 % of runtime, ≥ 2 s; `<w>` 640 / 1280; HDR sources prepend the tonemap chain to `-vf`)
```
ffmpeg … -ss <t> -i <file> -map 0:v:0 -an -sn -frames:v 1 -vf "scale=<w>:-2" -q:v 4 <out.jpg>
```

**Subtitle to WebVTT** (`<idx>` = stream index; external: `-i <subfile>` and no `-map`; `<T>` optional offset)
```
ffmpeg … [-ss <T>] -i <file> -map 0:<idx> -f webvtt -
```

**Remux (HLS fMP4, copy)** — `<vidx>`/`<aidx>` are ffprobe stream indexes
```
ffmpeg … [-ss <T>] -i <file> -map 0:<vidx> -map 0:<aidx> -sn \
  -c:v copy [-tag:v hvc1 (if hevc)] \
  (-c:a copy | -c:a ac3 -b:a 640k | -c:a aac -ac 2 -b:a 192k) \
  -f hls -hls_time <L> -hls_list_size 0 -hls_playlist_type event -hls_segment_type fmp4 \
  -hls_fmp4_init_filename init.mp4 -hls_segment_filename seg%05d.m4s \
  -hls_flags independent_segments+temp_file -start_number 0 index.m3u8
```

**Transcode, software (H.264 SDR)** — `<fps>` from `r_frame_rate` rounded; `<g>` = round(fps·L)
```
ffmpeg … [-ss <T>] -i <file> -map 0:<vidx> -map 0:<aidx> -sn \
  -vf "<filters>" \
  -c:v libx264 -preset <preset> -crf 23 [-maxrate <v>k -bufsize <2v>k] -profile:v high -level 4.1 -pix_fmt yuv420p \
  -g <g> -keyint_min <g> -sc_threshold 0 -force_key_frames "expr:gte(t,n_forced*<L>)" \
  (-c:a copy | -c:a ac3 -b:a 640k | -c:a aac -ac 2 -b:a 192k) \
  [-output_ts_offset <T>] -threads <n> \
  -f hls -hls_time <L> -hls_list_size 0 -hls_segment_type fmp4 \
  -hls_fmp4_init_filename init.mp4 -hls_segment_filename seg%05d.m4s \
  -hls_flags independent_segments+temp_file -start_number <T/L> index.m3u8
```
`<filters>` (comma-joined, in order): `yadif=mode=send_frame` if `field_order ∉ {progressive, unknown, ""}`; SDR: `scale=<W>:<H>:flags=bicubic` (omitted when no downscale), `format=yuv420p`; HDR→SDR: `zscale=w=<W>:h=<H>:t=linear:npl=100:f=spline36,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p` (verified in the container on a PQ/BT.2020 10-bit source; output is BT.709 8-bit).

**Transcode, NVENC (H.264 SDR), GPU decode**
```
ffmpeg … -hwaccel cuda -hwaccel_output_format cuda [-ss <T>] -i <file> -map 0:<vidx> -map 0:<aidx> -sn \
  -vf "[yadif_cuda,]scale_cuda=w=<W>:h=<H>:format=yuv420p" \
  -c:v h264_nvenc -preset p4 -tune hq -rc vbr -cq 23 -b:v 0 [-maxrate <v>k -bufsize <2v>k] -profile:v high -level 4.1 \
  -g <g> -keyint_min <g> -no-scenecut 1 -forced-idr 1 -force_key_frames "expr:gte(t,n_forced*<L>)" \
  … (audio, ts offset, hls options as above)
```
Without GPU decode (fallback rung 2): drop `-hwaccel …` and use the software `<filters>` with `-c:v h264_nvenc …`.

**Transcode, NVENC, HDR → SDR H.264** (GPU decode + scale, CPU tonemap at output size)
```
-vf "scale_cuda=w=<W>:h=<H>:format=p010le,hwdownload,format=p010le,zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
```

**Transcode, NVENC, HDR passthrough to HEVC Main10**
```
-vf "scale_cuda=w=<W>:h=<H>:format=p010le"   (omit entirely when no downscale)
-c:v hevc_nvenc -preset p4 -tune hq -rc vbr -cq 24 -b:v 0 [-maxrate <v>k -bufsize <2v>k] -profile:v main10 -tier main \
-color_primaries bt2020 -color_trc <smpte2084|arib-std-b67> -colorspace bt2020nc -color_range tv -tag:v hvc1 \
-g <g> -keyint_min <g> -no-scenecut 1 -forced-idr 1 -force_key_frames "expr:gte(t,n_forced*<L>)"
```
(Static HDR10 metadata SEI is not re-inserted by ffmpeg 6.1's nvenc wrapper; players treat the stream as PQ/BT.2020 without MDCV — acceptable.)

**Other hardware encoders (encode-only, Phase 1 args)**: `h264_qsv -preset medium -global_quality 23 -look_ahead 0`; `h264_amf -quality balanced -rc vbr_peak -qp_i 23 -qp_p 23`; `h264_vaapi` requires `-vaapi_device /dev/dri/renderD128 -vf 'format=nv12,hwupload' -qp 23` (**deferred** to Phase 2 for full pipelines); `h264_videotoolbox -q:v 60`.

**Self-tests**: see §6.5.

## Appendix B — Device compatibility and CODECS strings

Direct-play matrix (base profiles; runtime probes refine):

| Codec | Desktop Chrome/Edge (Win) | Firefox | iPhone 14 Pro (Safari) | LG A1 webOS 6 (browser, hls.js) |
|---|---|---|---|---|
| H.264 8-bit ≤ L5.1 | ✔ | ✔ | ✔ | ✔ |
| H.264 10-bit (Hi10P) | ✘ | ✘ | ✘ | ✘ → transcode |
| HEVC 8/10-bit | probe (HW + HEVC extension) | ✘ | ✔ (`hvc1`) | ✔ |
| HDR10 / HLG | pass-through if HEVC ok | ✘ | ✔ | ✔ |
| Dolby Vision p8 (HDR10 base) | as HDR10 | ✘ | ✔ (as HDR10 unless DV-tagged) | ✔ |
| Dolby Vision p5 | ✘ → transcode | ✘ | ✘ in Phase 1 (needs `dvh1` tagging; **deferred**) | ✘ |
| VP9 | ✔ (webm/mkv) | ✔ | ✘ | probe |
| AV1 | ✔ | ✔ | ✘ (no HW on A16) | probe |
| AAC / MP3 | ✔ | ✔ | ✔ | ✔ |
| AC‑3 / E‑AC‑3 | probe (Edge yes) | ✘ | ✔ | ✔ |
| DTS / DTS‑HD / TrueHD | ✘ | ✘ | ✘ | ✘ (probe `dtsc`) → AC‑3/AAC |
| Opus / Vorbis / FLAC | ✔ | ✔ | FLAC ✔, Opus ✘ | ✘ → encode |
| Containers | mp4, webm, mkv (Chromium) | mp4, webm | mp4/mov/m4v, HLS | mp4, HLS |
| Text subtitles | WebVTT `<track>` | same | WebVTT in-band HLS or `<track>` | WebVTT `<track>` |
| Image subtitles (PGS/VobSub) | burn-in **deferred** | — | — | — |

CODECS attribute mapping (omit when unknown):

| Codec | Format | Source fields |
|---|---|---|
| h264 | `avc1.PPCCLL` | PP = profile_idc hex (`Baseline`=42, `Main`=4d, `High`=64, `High 10`=6e), CC = `00`, LL = level hex (31→`1f`, 40→`28`, 41→`29`, 51→`33`) |
| hevc | `hvc1.P.T.LxxX.B0` → `hvc1.1.6.L<level>.B0` (Main), `hvc1.2.4.L<level>.B0` (Main 10) | `<level>` = ffprobe `level` (e.g. 120, 123, 150, 153) |
| av1 | `av01.0.<LL>M.<bd>` | level index from ffprobe level, bd 08/10 |
| aac | `mp4a.40.2` (LC), `mp4a.40.5` (HE), `mp4a.40.29` (HEv2) | profile |
| mp3 | `mp4a.40.34` | — |
| ac3 / eac3 | `ac-3` / `ec-3` | — |
| flac / opus | `flac` / `opus` | — |

## Appendix C — Scene-name parser: tag tokens

Stop/strip tokens (case-insensitive, matched as whole tokens after `.`/`_`→space; `+` and `-` inside tokens kept): resolution `2160p 1080p 1080i 720p 576p 480p 720 1080 2160 4k uhd`; source `web-dl webdl web webrip web-rip bluray blu-ray bdrip bdremux remux hdtv hdtvrip dvdrip dvd dvd9 dvd5 hdrip brrip amzn nf atvp dsnp hmax hulu itunes pcok`; codec `x264 x265 h264 h265 h.264 h.265 hevc avc av1 xvid divx vp9`; dynamic range `hdr hdr10 hdr10+ hdr10plus dv dovi dolby vision sdr hlg 10bit 8bit`; audio `aac ac3 dd dd5.1 ddp ddp5.1 eac3 dts dts-hd dtshd truehd atmos flac mp3 opus 2.0 5.1 7.1 2ch 6ch`; language `rus eng ukr tur ger fre fra spa ita jpn kor chi multi dual dub sub subs 2xrus 3xrus 2xeng`; misc `proper repack rerip extended unrated uncut directors cut remastered internal limited complete retail theatrical imax hybrid`; groups/sites (strip, not stop) `rutracker rgzsrutracker novafilm.tv lostfilm newstudio hdrezka kubik amedia ozz jaskier rarbg yts yify evo fgt ctrlhd ntb ntg ethel flux`; `by <token>` (strip both); trailing `-<token>` release group; bracketed `[…]`. The first stop token (or a year/episode marker) ends the title; everything after an episode marker up to the first tag is the episode title.

## Appendix D — Chrome 79 (webOS 6.0) compatibility rules for the client

| Don't | Do |
|---|---|
| Flexbox `gap` (Chrome 84) | `margin` on children or CSS Grid `gap` (Chrome 66) |
| `aspect-ratio` (88) | `padding-top: 150%` box trick |
| `inset` (87) | `top/right/bottom/left` |
| `:focus-visible` (86), `:is()` (88), `:where()` | `:focus` + `.is-focused` class |
| `<dialog>` (37 ok, but styling quirks) | custom modal |
| Optional chaining / nullish coalescing in **output** | Vite `build.target: ['chrome79','safari13']` (esbuild lowers them); verify with `scripts/check-es-target.mjs` |
| `Array.prototype.at`, `String.replaceAll` (85), `Promise.any` (85) | polyfill or avoid |
| `navigator.mediaCapabilities` HDR fields (86+) | feature-check; treat as unknown |
| `ResizeObserver` is fine (64); `IntersectionObserver` fine (51) | — |
| CSS `backdrop-filter` (76 ok), `scroll-behavior` fine | — |
| ES modules via `<script type=module>` are fine (61) | Keep Vite's default module output; no `modulepreload` reliance |

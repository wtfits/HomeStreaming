# HomeStreaming — Roadmap

Phase 1 is what the implementer builds now, in one focused session, and verifies inside the Linux build container (Go 1.24, Node 22, ffmpeg 6.1, Playwright + Chromium). Later phases are ordered by value for this user's setup (Windows PC with RTX 4090, torrent folder of mostly Rus/Eng 4K TV, iPhone 14 Pro, LG A1 webOS 6 TV, CGNAT + Tailscale).

Design reference: [DESIGN.md](DESIGN.md). API reference: [API.md](API.md).

---

## Phase 1 — MVP (build now)

Goal: browse a mixed movie/TV folder and play it in a browser on PC, iPhone and the LG TV's browser over the LAN, with remux/transcode fallback (software in the container, NVENC on the PC), resume, a single admin user with login, and the packaging needed to run on the Windows PC today and on a Linux/ARM64 box later.

### Suggested build order

1. Repo skeleton, `go.mod`, config, data dir, logging, DB + migrations, `cmd/homestreaming` (serve/doctor/version/service).
2. ffmpeg locator + capability self-tests (software path works in the container; NVENC detection is argv-tested).
3. Auth: setup, login, sessions, middleware, rate limit/lockout, preferences.
4. Library: naming parser (fixtures first), ignore rules, scanner, probe, artwork, watcher, tasks.
5. Items/home/search/images API.
6. Streaming: profiles, decision engine, direct serving, HLS sessions (transcode predictive + remux EVENT), subtitles, play-session API.
7. Web client: setup/login/home/library/item/player/settings/admin; TV mode; PWA.
8. Packaging: Makefile, embed, cross-compile, Dockerfile, service install, deploy docs.
9. Tests: unit, API, E2E; `make test` and `make test-e2e` green.

### P1.1 Server skeleton, config, data dir, logging

Acceptance:
- `go build ./cmd/homestreaming` with `CGO_ENABLED=0` succeeds for `linux/amd64`, `windows/amd64`, `linux/arm64`, `darwin/arm64` (`make dist`).
- First start creates the data dir (OS defaults per DESIGN §9.3, `--data-dir`/`HS_DATA_DIR` override), a commented `config.toml`, `homestreaming.db` with all migrations applied, `logs/`, `transcode/` (wiped on every start).
- Precedence flags > env > file > defaults is unit-tested for at least `http.addr`, `ffmpeg.path`, `transcode.dir`, `transcode.hwaccel`.
- Startup banner lists listen URLs with LAN IPv4s (and tailnet IPs when a `100.64/10` interface exists); `homestreaming doctor` prints data dir, config path, ffmpeg/ffprobe path+version, encoders, hwaccel self-test results, listen addresses, firewall hint.
- Logs: text to stderr, JSON rotating file (20 MB × 5); request logging at `info` for `/api` (segments at `debug`); tokens redacted.
- Graceful shutdown on SIGINT/SIGTERM/service stop: sessions killed, transcode dirs removed, WAL checkpointed.

### P1.2 ffmpeg integration and hardware detection

Acceptance:
- Locator order per DESIGN §9.2, including the all-users winget glob on Windows (unit-tested with a fake directory tree).
- Missing ffmpeg: startup `ERROR` line with the winget command, `GET /system/info.ffmpeg.found=false`, setup wizard and admin status banner, scans still work (items `pending`, reason `ffmpeg_missing`), `play-info` → `503 ffmpeg_missing`.
- `transcode.hwaccel`: `auto|none|nvenc|qsv|amf|vaapi|videotoolbox`; `auto` runs encoder + decoder self-tests at startup (and retries every 10 min when failed); results visible in `/admin/status.hwaccel`. In the container `auto` resolves to `none` (`libx264`).
- Argv builders unit-tested by exact string comparison for: software SDR, software HDR→SDR tonemap, NVENC SDR with/without NVDEC, NVENC HDR→SDR hybrid, NVENC HEVC Main10 HDR passthrough, remux with audio copy / AC‑3 / AAC, thumbnail, subtitle extraction (Appendix A of DESIGN).
- Fallback ladder `hw-full → hw-encode-only → software` is exercised by a test that configures `hwaccel=nvenc` (self-test fails in the container) and asserts a session still plays via `libx264` with `transcode.backend == "software"` in the API response and a `warn` log.
- ffmpeg children run with `CREATE_NO_WINDOW | BELOW_NORMAL_PRIORITY_CLASS` on Windows (build-tagged file compiles under `GOOS=windows`), `nice +10` on Unix.

### P1.3 Auth, first-run setup, single user

Acceptance:
- `setupRequired` until the first admin exists; `POST /system/setup` creates the admin, returns a session; `403 setup_done` afterwards.
- Argon2id hashes (params per DESIGN §5); login returns cookie + token; Bearer, cookie and (media-only) query token all work; query token rejected on non-media routes.
- Session expiry/sliding renewal; `GET/DELETE /auth/sessions`; password change revokes other sessions.
- Rate limiting: 11th login attempt within a minute from one IP → `429` with `Retry-After`; 5 wrong passwords → `423 locked` with countdown, doubling; success resets.
- CSRF rule: cookie-authenticated `POST` without JSON content type → `403 csrf`.
- Security headers and CORS per DESIGN §8.2 present on every response (API test).
- Preferences `audioLanguage` (default `eng`), `subtitleLanguage`, `subtitleMode` persisted and applied to track defaults.

### P1.4 Libraries, scanner, naming, watcher, artwork

Acceptance:
- Library types `mixed` (default), `movies`, `shows`; multiple root paths; create/edit paths/delete; `admin/fs` browser returns drives on Windows (tested with the Unix path on Linux, Windows branch compiled).
- Naming parser passes **every fixture row** in DESIGN §3.4 plus the Appendix C tag list tests; `arrested.development…` and `Arrested.Development…` folders merge into **one** series; `[07] The Ringing of the Bell.mkv` becomes S1E7 with the episode title; `Pluribus…S01E02.Pirate.Lady…` yields episode title "Pirate Lady"; all-lowercase titles are title-cased.
- Ignore rules: `Sample/` dirs, `*sample*` files, `.part/.!qB/.crdownload` etc., hidden files, junk extensions, `VIDEO_TS`/`BDMV` (counted `unsupported`), size-ratio rule only when the largest sibling ≥ 200 MB.
- Scan pipeline phases with task progress; items visible after upsert (`pending`), `ok` after probe; probe failures stay `pending` with reason and retry; files vanished → `missing`, hidden from lists, user data kept; `clean` deletes them; preflight aborts when a root is unavailable without marking anything missing.
- ffprobe extraction populates `media_streams` (incl. `hdr`, `dvProfile`, `bitDepth`, language/title), `chapters`, `runtime_ms`, `bitrate`, `container`; audio languages from tags **and** Russian-release title heuristics.
- External subtitles: sidecar `<base>.<lang>.srt` and `Subs/` folder for single-video folders; languages normalized.
- Artwork: local files by name only (random `banner.jpg` ignored); extracted thumb (10 %) and backdrop (25 %) with HDR tonemap for HDR sources; `?w=` resize cache with ETag/304.
- Watcher: adding a file to a library root creates the item without a full rescan (API test with settle time shortened via test hook, default 30 s); `.part` → nothing; rename `.!qB` → final name → item; delete → `missing`; new subdirectories are watched.
- Periodic quick scan every `scan_interval_hours`; scan on startup; manual scan; concurrent scan request on the same library → `409` with the running task.
- Scan of the test tree in DESIGN §10 yields exactly: 4 movies, 5 series (Widow's Bay, Over the Garden Wall 2014, Better Call Saul, Arrested Development, Pluribus), 9 episodes, 2 ignored-incomplete, 1 unsupported, ≥2 ignored-sample/junk.

### P1.5 Browse API, home, search, images

Acceptance:
- `GET /items` filters/sorts/pagination per API.md; `search=` matches titles case-insensitively (movies + series by default).
- `GET /items/{id}` returns streams, audio/subtitle tracks with ids, chapters, default track ids per preferences, `nextUp` for series.
- `GET /items/{id}/children` orders seasons (specials last) and episodes.
- `GET /home` returns continue-watching, next-up, recently-added and library sections with the semantics of DESIGN §7.5.
- Image fallbacks (season → series poster, series thumb → first episode) work; missing poster → `404`.

### P1.6 Streaming: decision engine, direct play, HLS remux/transcode, subtitles, audio selection

Acceptance:
- Decision engine truth table unit tests (≥ 25 cases) covering: ios/webos/desktop profiles × {h264 mp4, h264 mkv, hevc10 HDR mkv, DV p5, DV p8, h264 10-bit, vp9 webm, ac3/dts/truehd/opus audio, non-default audio, image subtitle, bitrate above cap, forceTranscode}. Reason codes match API.md.
- Direct: `GET /items/{id}/file` serves `206` for `Range: bytes=100-199` with correct `Content-Range`, `HEAD` works, `Content-Type` by extension, query token accepted.
- Transcode session: `master.m3u8` + static VOD `index.m3u8` with `ceil(runtime/6)` segments and exact `EXTINF` values; `seg00000.m4s` served within 10 s of session creation; requesting `seg00010.m4s` (outside the producing window) restarts ffmpeg at 60 s (`-ss 60 … -output_ts_offset 60 -start_number 10`, verified via the admin session's `ffmpegArgs` and `restarts == 1`) and serves it; `ffmpeg -i master.m3u8 -f null -` (with `?token=`) exits 0 for a full transcode session.
- Remux session: EVENT playlist with `EXT-X-START` injected; reaches `EXT-X-ENDLIST` for a 20 s file; HEVC sources get `-tag:v hvc1`; audio copy vs. AC‑3 vs. AAC chosen per DESIGN §6.1 (remote cap < 10000 → AAC stereo even for copyable AC‑3); `ffmpeg -i master.m3u8 -f null -` exits 0.
- HDR→SDR: transcoding the HEVC-10bit PQ fixture with a profile lacking HDR produces segments whose `ffprobe` shows `yuv420p`, `color_transfer=bt709`, height per ladder; with an `ios`-like profile (hevc + hdr) and `hwaccel=nvenc` **unavailable**, the server falls back to H.264 SDR tonemap (HEVC passthrough requires NVENC) and reports it in `transcode.video`.
- Bitrate policy: with `remote_max_bitrate_kbps=6000` a request from a non-private IP (test hook to fake the client IP, or `X-Forwarded-For` with a trusted proxy) for a 60 Mbps file yields `transcode` with reason `bitrate`, `maxrateKbps = round(0.9*(6000-192))`, height ≤ 1080; the same request from `192.168.0.16` yields `direct`/`remux`.
- Subtitles: embedded `subrip` → valid WebVTT (`WEBVTT` header, cues); external `.srt` → WebVTT; `offsetMs` shifts cues; CP1251 fallback test with a Cyrillic non-UTF8 file; image subtitle track → `415`; `engine=native` sessions include `EXT-X-MEDIA` subtitle renditions and `sub/{id}.m3u8` + `.vtt`; `hlsjs`/`direct` sessions use `subtitles.mode=sidecar`.
- Audio: default track follows `audioLanguage=eng` over a `default`-flagged Russian track; untagged tracks titled "Русский (LostFilm)" are classified `rus`; choosing a non-default track on a direct-playable file yields `remux`.
- Sessions: idle 60 s (shortened in tests) kills ffmpeg and removes the directory; `DELETE` is immediate; `transcode_max_sessions` → `503 transcode_busy`; `replacesPlaySessionId` stops the previous one; progress pings persist position; `state: stopped` ends the session; the admin list shows method/reasons/backend/log tail.
- Resume/played: position ≤ max(5 s, 2 %) → not in progress; ≥ 92 % → `played`, position reset, `playCount++`; continue-watching and next-up reflect it.

### P1.7 Web client (desktop, iPhone PWA, TV mode)

Acceptance:
- Vite build targets `chrome79`/`safari13`; `scripts/check-es-target.mjs` (acorn `ecmaVersion: 2019`) passes on every emitted chunk; CSS avoids the Appendix D features (reviewed by grep in the check script for `aspect-ratio`, `inset:`, `:focus-visible`, `gap:` outside grid rules).
- Setup wizard: ffmpeg status (red banner + `winget install Gyan.FFmpeg` hint + path field when missing), admin account, first library with directory browser (`mixed` default), scan progress, lands on Home.
- Login with lockout/limit messages; device name auto-filled; session persists across reloads (cookie).
- Home rows (Continue Watching, Next Up, Recently Added, per-library sections), library grid with sort/search/infinite scroll, text cards when no poster, badges (4K, HDR/DV, audio languages, "processing" while `pending`).
- Item page for movie/series/season/episode with Play/Resume, audio & subtitle pickers (defaults from preferences), mark played, next-up highlight.
- Player: custom controls; `play-info` driven engine selection (native HLS on iOS, hls.js elsewhere, `hs.engine` override); absolute timeline mapping with `timelineOffsetMs`; seeking (direct via Range, transcode via segment restarts, remux via new session when outside the produced range); audio/subtitle switch via new session; quality menu (Auto shows the applied cap and class, fixed caps, force transcode); "why transcoding" reasons; progress every 10 s + beacon on `pagehide`; "Up next" autoplay for episodes; media-error → one automatic retry with `forceTranscode`.
- iPhone: `manifest.webmanifest` (standalone, icons), `apple-mobile-web-app-capable`, safe-area padding, `playsinline`, native fullscreen button, touch-friendly controls at 390 px width, in-band subtitle selection on native HLS.
- TV mode: auto-on for `Web0S` UA or `?tv=1`/setting; spatial navigation with arrows/Enter/Back(461)/Escape across home rows, grid, item page, menus, player; focus ring + scale; media keys; everything reachable without a pointer; font sizes for 10-foot viewing at 1920×1080.
- Settings: password, preferences, playback quality/engine/force-transcode (localStorage), devices list with revoke, TV mode toggle. Admin: status (ffmpeg, hwaccel, sessions with reasons, tasks), libraries CRUD + scan/clean, settings form, problems list.
- All UI strings live in `web/src/i18n/en.ts`.

### P1.8 Packaging and deployment

Acceptance:
- `make web && make build` embeds the client; `web/dist/.keep` committed; `.gitignore` excludes build outputs, `node_modules`, data dirs.
- `make dist` produces `dist/homestreaming-{windows-amd64.exe,linux-amd64,linux-arm64,darwin-arm64}` + archives with `deploy/` docs.
- `deploy/Dockerfile` builds (`docker build` is run in the container for `linux/amd64`; multi-arch via `buildx` documented); container starts, `GET /system/info` works with `HS_DATA_DIR=/data`; `deploy/docker-compose.yml` with `/data`, `/media:ro`, commented GPU/VAAPI lines.
- `homestreaming service install|uninstall|start|stop|status` implemented with `kardianos/service` (Windows service, systemd); Windows install adds the firewall rule (`profile=any`) when elevated, prints the `netsh` command otherwise. Unit-compiled for Windows; manual verification on the PC is documented in `deploy/windows.md`.
- `deploy/windows.md`: install path, ffmpeg via winget (and how the service finds it / sets `ffmpeg.path`), firewall prompt (Private + Public), static IP 192.168.0.5 recommendation, sleep settings, service install, where logs/config live, how to point the TV browser and iPhone at `http://192.168.0.13:8420`.
- `deploy/tailscale.md`: the §8.1 checklist (unattended mode, key expiry, MagicDNS URL, Add to Home Screen, optional `tailscale serve` HTTPS with `trusted_proxies`), the DERP throughput note and the 6 Mbps cap rationale, "TV is LAN-only", rejected options (Cloudflare Tunnel, port forwarding behind CGNAT), VPS relay outline.
- `deploy/homestreaming.service` systemd unit; migration steps (DESIGN §9.6) in `README.md`.
- `README.md`: what it is, quick start (Windows and Docker), build from source, links to docs.

### P1.9 Tests and verification

Acceptance:
- `make test` (Go unit + API tests) passes in the container in < 5 min; API tests generate fixtures once via `internal/testutil` and skip cleanly when ffmpeg is absent.
- `make test-e2e` builds everything, generates the DESIGN §10 media tree with `scripts/make-test-media.sh`, starts the binary on a temp data dir and free port, runs the Playwright suite (desktop, iPhone-emulated with `hs.engine=hlsjs`, webOS-emulated TV mode) headless with the preinstalled browsers, and exits 0. Chromium launched with `--autoplay-policy=no-user-gesture-required`.
- E2E assertions include: setup → scan → counts; direct play advances `currentTime ≥ 2 s`; HEVC fixture → `method=transcode` with `hdr_unsupported`/`video_codec` reason; AC‑3 fixture → `remux` with audio encode; seek to 80 % plays; reload → Continue Watching with position; resume; audio switch Rus→Eng; subtitle cue rendered (`video.textTracks[0].activeCues.length > 0` or sidecar `<track>` cue visible); mark played → Next Up; TV mode D-pad path home → library → item → play → back.
- HLS validity: `ffmpeg -i …/master.m3u8 -f null -` passes for one transcode and one remux session inside the API tests.

### Definition of done (Phase 1)

`make test` and `make test-e2e` green in this container; `make dist` and `docker build` succeed; docs in `deploy/` complete; a fresh `homestreaming.exe` on the Windows PC can (per the documented manual steps) locate the winget ffmpeg, detect NVENC, scan `F:\torrent`, and play to the three clients on the LAN and to the iPhone over Tailscale with the 6 Mbps cap.

---

## Phase 2 — Make it comfortable (next)

| Item | Scope | Acceptance (summary) |
|---|---|---|
| webOS IPK | `clients/webos/` (appinfo.json, icon, `@webos-tools/cli ares-package` build script), `#/connect` server-URL screen, Bearer + `?token=` media mode, Back-key exit semantics, "Developer Mode" sideload doc | IPK builds in the container; app installs on the A1 via `ares-install`; plays HEVC via hls.js/MSE on the TV; remembers server URL |
| DLNA/UPnP server | SSDP announce + ContentDirectory (Browse) + HTTP serving (direct files; transcoded profiles later); library tree Movies/Shows/Seasons; the router has multicast enabled | TV's built-in media player lists libraries at 192.168.0.10 and plays MKV/MP4 directly; mDNS `homestreaming.local` advertised alongside |
| Hardware pipelines (full) | NVDEC-only decode paths for VC‑1/MPEG‑2, `libplacebo` Vulkan tonemapping (incl. DV p5), `av1_nvenc` option, full QSV/VAAPI/AMF/VideoToolbox pipelines for the future Linux box, transcode throttling (pause when > 2 min ahead), segment pruning behind the playhead, hw self-test details in UI | Tonemapped output validated by ffprobe color metadata; throttling observable in admin |
| Exact remux seeking | Keyframe index from MP4 `stss`/MKV cues at scan time → predictive playlists for remux sessions (no EVENT playlists) | Remux seek lands within one segment; native iOS scrubber covers the whole runtime |
| ABR | Multi-variant master (e.g. 1080p/720p/480p) for transcode sessions, hls.js bandwidth estimation; "Auto" becomes adaptive | Remote playback over DERP switches variants without stalls |
| Metadata | Kodi NFO parsing; TMDB provider (API key in settings): match by title+year, posters/backdrops/overview/genres/ratings/episode titles; per-item "identify" dialog; genre/year filters | Offline mode unaffected; provider failures never block scans |
| Remote access page | Tailnet detection, checklist, CGNAT re-check, speed-test hint; ACME TLS for a future public IP/VPS relay | — |
| Users | Admin user CRUD, per-user library access, PIN/device-code login for TV, per-series audio/subtitle memory | — |
| Library | `videos` (home videos) and `music` library types; multi-part movies (cd1/cd2); anime absolute numbering `[Group] Title - 01`; dated episodes; VIDEO_TS playback via `concat`; charset detection for subtitles; `ffmpeg download` command; progress preserved across file moves (size + partial hash) | — |
| Player | Trickplay thumbnails on scrub, chapters UI, subtitle styling, PGS/VobSub burn-in, playback speed, keyboard shortcut help | — |
| Ops | Update check, backup/restore command (zip of DB + images + config), prevent Windows sleep during sessions, Event Log logging, hourly metrics in admin | — |

## Phase 3 — Beyond

| Item | Notes |
|---|---|
| Native iOS app (SwiftUI + AVPlayer) | Needs a Mac/Xcode; the API is already Bearer-token friendly. Gains: background audio, PiP, Dolby Vision p5 via `dvh1` tagging, offline downloads |
| VPS relay for non-Tailscale devices | WireGuard PC→VPS + Caddy + Let's Encrypt; documented first, optional helper later |
| Android TV / Android | Web wrapper (TWA) first |
| Chromecast / AirPlay | AirPlay works implicitly from iOS Safari with HLS; Chromecast needs a receiver app |
| SyncPlay / multi-user watch together | — |
| Live TV / DVR | **Not planned** unless requested |
| Music features | Album/artist browsing, gapless, transcoding to AAC/Opus |
| Multi-server / federation | Not planned |

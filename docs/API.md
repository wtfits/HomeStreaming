# HomeStreaming — HTTP API v1 (MVP)

Base path: `/api/v1`. All request and response bodies are JSON (`Content-Type: application/json; charset=utf-8`) unless stated. Field names are camelCase. Times are ISO‑8601 UTC strings (`2026-11-01T14:30:00Z`); durations/positions are integer **milliseconds**; bitrates are integer **kbps** unless the field name ends in `Bps`.

## 1. Conventions

### 1.1 Authentication

| Transport | Where accepted | Notes |
|---|---|---|
| `Authorization: Bearer <token>` | every endpoint | Preferred for non-browser clients and the Phase 2 `file://` webOS app |
| Cookie `hs_session=<token>` | every endpoint | Set by `login`/`setup`; `HttpOnly; SameSite=Lax; Path=/` (+`Secure` on HTTPS) |
| Query `?token=<token>` | **only** `GET /items/{id}/file`, `GET /items/{id}/image/*`, `GET /items/{id}/subtitles/*`, `GET /play/*` | For `<video>`/`<track>`/HLS requests when cookies are unavailable |

Order of precedence when several are present: header → cookie → query.

Public (no auth): `GET /system/info`, `GET /system/ping`, `POST /system/setup`, `POST /auth/login`. Everything else returns `401 unauthenticated` without a valid session. Admin-only endpoints return `403 forbidden` for non-admins.

**CSRF rule**: a request authenticated by cookie with a method other than GET/HEAD/OPTIONS must carry `Content-Type: application/json` or the header `X-Requested-With: HomeStreaming`; otherwise `403 csrf`.

**CORS**: `Access-Control-Allow-Origin: *`, `Access-Control-Allow-Headers: Authorization, Content-Type, X-Requested-With`, `Access-Control-Allow-Methods: GET, POST, PATCH, DELETE, OPTIONS`, no credentials. `OPTIONS` preflights return 204.

### 1.2 Errors

```json
{ "error": { "code": "not_found", "message": "item not found", "details": {} } }
```

| HTTP | `code` | When |
|---|---|---|
| 400 | `bad_request`, `validation` (`details: {field: message}`) | Malformed JSON, invalid params |
| 401 | `unauthenticated` | Missing/expired session |
| 403 | `forbidden`, `csrf`, `setup_done` | Role, CSRF rule, setup already completed |
| 404 | `not_found` | Unknown id/path/segment |
| 409 | `conflict`, `scan_running` | Duplicate username/library path; scan already running (returns the running task) |
| 423 | `locked` (`details: {retryAfterSeconds}`) | Account lockout |
| 429 | `rate_limited` + `Retry-After` header | Per-IP limiter |
| 503 | `ffmpeg_missing`, `transcode_busy`, `not_ready` | ffmpeg not found; max sessions reached; playlist/segment not produced in time |
| 500 | `internal` | Unexpected; logged with a request id (`X-Request-Id` response header) |

### 1.3 Pagination and sorting

`?offset=0&limit=50` (`limit` max 200). List responses: `{ "items": [...], "total": 123, "offset": 0, "limit": 50 }`.

### 1.4 Common objects

**User**
```json
{ "id": "…", "username": "can", "isAdmin": true, "createdAt": "…",
  "preferences": { "audioLanguage": "eng", "subtitleLanguage": "", "subtitleMode": "off" } }
```
`subtitleMode ∈ "off" | "preferred" | "always"`. Languages are ISO 639‑2/B codes (`eng`, `rus`, `tur`) or `""`.

**AuthSession** (a logged-in device)
```json
{ "id": "…", "deviceId": "…", "deviceName": "Safari on iPhone", "clientType": "web",
  "ip": "100.101.102.103", "createdAt": "…", "lastSeenAt": "…", "expiresAt": "…", "current": true }
```

**Library**
```json
{ "id": "…", "name": "Torrent", "type": "mixed", "paths": ["F:\\torrent"],
  "createdAt": "…", "lastScanAt": "…", "lastScanStatus": "ok", "lastScanError": null,
  "counts": { "movies": 4, "series": 5, "episodes": 9, "pending": 0, "missing": 0 } }
```
`type ∈ "mixed" | "movies" | "shows"`.

**ItemSummary** (what lists return)
```json
{ "id": "…", "type": "episode", "libraryId": "…",
  "title": "Pirate Lady", "sortTitle": "pirate lady", "year": null, "overview": "",
  "runtimeMs": 3482000, "premiereDate": null, "addedAt": "…",
  "parentId": "<seasonId>", "seriesId": "<seriesId>", "seriesTitle": "Pluribus",
  "seasonNumber": 1, "episodeNumber": 2, "episodeEndNumber": null,
  "images": { "poster": false, "backdrop": true, "thumb": true },
  "video": { "codec": "hevc", "width": 3840, "height": 2160, "hdr": "dolbyvision", "bitDepth": 10 },
  "audioLanguages": ["rus", "eng"],
  "childCount": null, "unplayedCount": null,
  "status": "ok",
  "userData": { "positionMs": 125000, "played": false, "playCount": 0, "lastPlayedAt": "…" } }
```
`type ∈ movie | series | season | episode`. `video` is null for `series`/`season`. `childCount`/`unplayedCount` are set for `series` (episodes) and `season` (episodes). `status ∈ ok | pending | missing` (lists exclude `missing` unless `?status=` says otherwise). Image URLs are derived by the client: `/api/v1/items/{id}/image/{kind}?w=…`.

**ItemDetail** = ItemSummary plus:
```json
{ "path": "F:\\torrent\\Pluribus.S01…\\Pluribus.S01E02….mkv", "size": 4123456789, "container": "matroska",
  "bitrate": 9480, "mtime": "…",
  "mediaStreams": [ MediaStream, … ],
  "audioTracks": [ { "id": 1, "codec": "eac3", "channels": 6, "channelLayout": "5.1", "language": "rus", "title": "Русский (LostFilm)", "isDefault": true, "bitrate": 768 },
                   { "id": 2, "codec": "eac3", "channels": 6, "channelLayout": "5.1", "language": "eng", "title": "English", "isDefault": false, "bitrate": 768 } ],
  "subtitleTracks": [ { "id": "e3", "codec": "subrip", "language": "eng", "title": "English", "isDefault": false, "isForced": false, "isText": true, "isExternal": false },
                      { "id": "x9f1…", "codec": "srt", "language": "eng", "title": "English.srt", "isDefault": false, "isForced": false, "isText": true, "isExternal": true } ],
  "chapters": [ { "index": 0, "startMs": 0, "endMs": 61000, "title": "Chapter 1" } ],
  "defaultAudioTrackId": 2, "defaultSubtitleTrackId": null,
  "nextUp": ItemSummary | null,            // series only
  "statusReason": null }
```
Audio track ids are ffprobe stream indexes (integers). Subtitle track ids are strings: `e<streamIndex>` for embedded, `x<externalSubtitleId>` for external files.

**MediaStream**
```json
{ "index": 0, "type": "video", "codec": "hevc", "profile": "Main 10", "level": 153, "codecTag": "hvc1",
  "width": 3840, "height": 2160, "pixFmt": "yuv420p10le", "bitDepth": 10, "frameRate": 23.976,
  "colorTransfer": "smpte2084", "colorPrimaries": "bt2020", "colorSpace": "bt2020nc",
  "hdr": "hdr10", "dvProfile": 8, "dvBlCompatId": 1, "fieldOrder": "progressive", "bitrate": 8700,
  "channels": null, "channelLayout": null, "sampleRate": null,
  "language": null, "title": null, "isDefault": true, "isForced": false, "isText": null }
```

**Task**
```json
{ "id": "…", "kind": "scan", "libraryId": "…", "status": "running", "phase": "probe",
  "progress": { "done": 12, "total": 40 }, "startedAt": "…", "finishedAt": null, "error": null,
  "report": { "added": 3, "updated": 0, "missing": 0, "pending": 1, "ignoredSample": 1, "ignoredIncomplete": 2, "unsupported": 1 } }
```
`status ∈ queued | running | done | failed`; `phase ∈ walk | upsert | probe | images`.

---

## 2. System

### `GET /system/info` (public)
```json
{ "name": "HomeStreaming", "serverName": "HomeStreaming", "version": "0.1.0", "apiVersion": 1,
  "serverId": "…", "setupRequired": false, "os": "windows", "arch": "amd64",
  "ffmpeg": { "found": true, "path": "C:\\Users\\can\\AppData\\Local\\Microsoft\\WinGet\\…\\ffmpeg.exe", "version": "7.1" } }
```
`ffmpeg.path`/`version` are omitted for unauthenticated callers (only `found` is public).

### `GET /system/ping` (public) → `{ "ok": true, "time": "…" }`

### `POST /system/setup` (public; only while `setupRequired`)
Request: `{ "username": "can", "password": "…", "serverName": "HomeStreaming", "deviceId": "…", "deviceName": "…" }`
Response `201`: same as `POST /auth/login` (session created, cookie set). `403 setup_done` afterwards. Rate limited like login.

### `GET /admin/status` (admin)
```json
{ "version": "0.1.0", "uptimeSeconds": 1234, "dataDir": "C:\\ProgramData\\HomeStreaming", "configPath": "…",
  "transcodeDir": "…", "transcodeDirFreeBytes": 123456789012,
  "ffmpeg": { "found": true, "path": "…", "ffprobePath": "…", "version": "7.1", "encoders": ["libx264","h264_nvenc","hevc_nvenc","av1_nvenc"] },
  "hwaccel": { "configured": "auto", "active": "nvenc", "hwDecode": { "h264": true, "hevc": true },
               "selfTests": [ { "backend": "nvenc", "test": "encode_h264", "ok": true, "detail": "" } ] },
  "listen": [ "http://0.0.0.0:8420" ], "lanAddresses": ["192.168.0.13"], "tailnetAddresses": ["100.101.102.103"],
  "connection": { "class": "lan", "ip": "192.168.0.16" },
  "activePlaySessions": 1, "runningTasks": 0,
  "libraries": [ { "id": "…", "name": "…", "counts": { … } } ] }
```

### `GET /admin/update` (admin)
```json
{ "current": "v0.1.7", "devBuild": false, "repo": "wtfits/HomeStreaming",
  "latest": { "version": "v0.1.9", "name": "v0.1.9", "notes": "- …", "url": "https://github.com/…/releases/tag/v0.1.9", "publishedAt": "…" },
  "available": true, "checkedAt": "…", "checkError": "",
  "canInstall": true, "cannotInstallReason": "",
  "state": "idle", "error": "", "downloadedBytes": 0, "totalBytes": 0,
  "lastResult": { "from": "v0.1.7", "to": "v0.1.8", "ok": true, "message": "Updated from v0.1.7 to v0.1.8.", "at": "…" },
  "activePlaySessions": 0 }
```
`latest` is the newest published GitHub release (`null` before the first check or when none exists). `devBuild` is true when the running version is not a release (`a2d4144`, `0.1.0-dev`); any release counts as newer then. `state ∈ idle | downloading | installing | restarting | restart_required | failed`. The server checks every 6 h (`[update] check` in `config.toml`).

### `POST /admin/update/check` (admin) → `200` same body as `GET /admin/update`; a failed check is reported in `checkError`.

### `POST /admin/update/install` (admin) → `202` same body, `state: "downloading"`
Downloads the platform archive, verifies it against the release's `SHA256SUMS`, runs `<new> version`, swaps the executable (the old one is kept as `homestreaming.old[.exe]`) and restarts. The Windows service restarts through a detached helper (`homestreaming update finish`) that rolls back if the new version does not answer `GET /system/info` within 90 s; other setups end in `restart_required`. `409 conflict` with the reason when nothing can be installed (up to date, update in progress, container, program folder not writable).

### `GET /admin/fs?path=` (admin)
Directory browser for the library setup. Without `path` on Windows returns drives; on Unix returns `/`.
```json
{ "path": "F:\\", "parent": null, "entries": [ { "name": "torrent", "path": "F:\\torrent", "isDir": true } ],
  "roots": [ { "name": "C:\\", "path": "C:\\" }, { "name": "F:\\", "path": "F:\\" } ] }
```
Only directories are listed. Hidden/system directories are skipped. `400` for relative paths.

---

## 3. Auth

### `POST /auth/login` (public, rate limited)
Request: `{ "username": "can", "password": "…", "deviceId": "f3c2…", "deviceName": "Chrome on Windows", "clientType": "web" }`
`deviceId` is a client-generated UUID persisted in localStorage; a login with the same `deviceId` replaces that device's previous session.
Response `200` (+ `Set-Cookie: hs_session=…`):
```json
{ "token": "…", "user": User, "session": AuthSession, "connection": { "class": "lan", "ip": "192.168.0.16" } }
```
Errors: `401 invalid_credentials`, `423 locked`, `429 rate_limited`.

### `POST /auth/logout` → `204`, deletes the current session, clears the cookie.

### `GET /auth/me` → `{ "user": User, "session": AuthSession, "connection": { "class": "remote", "ip": "100.…", "maxBitrateKbps": 6000 } }`

### `PATCH /auth/me/preferences`
Request (partial): `{ "audioLanguage": "eng", "subtitleLanguage": "eng", "subtitleMode": "preferred" }` → `200 User`.

### `POST /auth/me/password`
Request: `{ "currentPassword": "…", "newPassword": "…" }` → `204`. Other sessions of the user are revoked (`?keepOtherSessions=true` to keep them).

### `GET /auth/sessions` → `{ "items": [AuthSession, …] }` (current user's devices)
### `DELETE /auth/sessions/{id}` → `204` (own sessions only; admin may delete any)

---

## 4. Libraries and tasks

### `GET /libraries` → `{ "items": [Library, …] }`
### `POST /libraries` (admin)
Request: `{ "name": "Torrent", "type": "mixed", "paths": ["F:\\torrent"] }` → `201 Library` and starts a scan (`Location: /api/v1/tasks/{taskId}`, body also includes `"scanTaskId"`). Validation: paths absolute, exist, are directories, not nested in another library path (`409 conflict`).
### `GET /libraries/{id}` → `Library`
### `PATCH /libraries/{id}` (admin) — `{ "name"?, "paths"? }`. Changing `paths` relocates roots: paths are matched to existing `library_paths` rows by position in the array (`{"paths": [{"id": "…", "path": "/media/torrent"}, "F:\\other"]}` form is also accepted to relocate explicitly). Triggers a scan.
### `DELETE /libraries/{id}` (admin) → `204` (items and user data of this library are deleted)
### `POST /libraries/{id}/scan` (admin) — body optional `{ "mode": "full" | "quick" }` (default `quick`) → `202 Task`; `409 scan_running` with the running task in `details.task`.
### `POST /libraries/scan` (admin) → `202 { "tasks": [Task, …] }` (all libraries)
### `POST /libraries/{id}/clean` (admin) → `200 { "deleted": 3 }` — deletes `missing` items.
### `GET /tasks?status=running` → `{ "items": [Task, …] }` (last 50)
### `GET /tasks/{id}` → `Task`

---

## 5. Items

### `GET /items`
Query parameters:

| Param | Values |
|---|---|
| `libraryId` | library id |
| `type` | `movie`, `series`, `season`, `episode` (comma list allowed) |
| `parentId` | children of a series/season (prefer `/items/{id}/children`) |
| `seriesId` | all episodes of a series |
| `search` | case-insensitive substring on `title` (and `seriesTitle` for episodes); `type` defaults to `movie,series` when `search` is set |
| `sort` | `title` (default), `added`, `year`, `premiere`, `lastPlayed`, `episode` (season/episode order) |
| `order` | `asc` (default for title/episode), `desc` (default for added/lastPlayed) |
| `played` | `true`/`false` |
| `status` | `ok` (default), `pending`, `missing`, `all` |
| `offset`, `limit` | pagination |

Response: `{ "items": [ItemSummary], "total", "offset", "limit" }`.

### `GET /items/{id}` → `ItemDetail`
### `GET /items/{id}/children?type=` → `{ "items": [ItemSummary] }` — seasons of a series (sorted by `seasonNumber`, specials last), episodes of a season (by `episodeNumber`); `type=episode` on a series returns all episodes in order.

### `GET /items/{id}/image/{kind}?w=&h=`
`kind ∈ poster | backdrop | thumb`. Fallbacks: season poster → series poster; episode poster → series poster; series thumb → first episode thumb; series backdrop → first episode backdrop; movie poster missing → `404` (the client renders a text card). `w`/`h` (max 3840) resize keeping aspect ratio (fit inside). Response `image/jpeg` with `ETag`, `Cache-Control: public, max-age=86400`, `304` on `If-None-Match`.

### `GET /items/{id}/file`
Direct file stream. Supports `Range` (`206 Partial Content`), `HEAD`, `If-Range`, `If-Modified-Since`. Headers: `Accept-Ranges: bytes`, `Content-Type` by container, `Content-Length`, `Content-Disposition: inline; filename="…"`, `Cache-Control: private, max-age=0`. `404` when the item is `missing` or not file-backed.

### `GET /items/{id}/subtitles/{trackId}.vtt?offsetMs=0`
WebVTT (`text/vtt; charset=utf-8`) for an embedded text track (`e<idx>`) or an external file (`x<id>`). `offsetMs` shifts all cues earlier by that amount (used for remux sessions with a non-zero timeline offset). `415` when the track is not text-based (image subtitles).

### `POST /items/{id}/play-info`
Creates a **play session** and returns how to play.

Request:
```json
{ "profile": CapabilityProfile,
  "audioTrackId": 2, "subtitleTrackId": "e3",
  "startPositionMs": 125000,
  "maxBitrateKbps": 0,
  "forceTranscode": false,
  "replacesPlaySessionId": "…" }
```
`audioTrackId`/`subtitleTrackId` optional (server applies the user's preferences; `subtitleTrackId: null` = off). `maxBitrateKbps` 0 = no client cap. `replacesPlaySessionId` stops the previous session atomically (track switches/seeks across sessions).

`CapabilityProfile` (see DESIGN §6.2):
```json
{ "deviceId": "…", "deviceName": "…", "clientType": "web", "platform": "ios", "engine": "native",
  "containers": ["mp4","mov","m4v"],
  "videoCodecs": [ { "codec": "h264", "maxLevel": 52, "maxBitDepth": 8, "maxWidth": 3840, "maxHeight": 2160 },
                   { "codec": "hevc", "maxLevel": 153, "maxBitDepth": 10, "maxWidth": 3840, "maxHeight": 2160, "hdr": ["hdr10","hlg","dolbyvision"] } ],
  "audioCodecs": ["aac","mp3","ac3","eac3","alac","flac"], "maxAudioChannels": 6,
  "hlsVideoCodecs": ["h264","hevc"], "hlsAudioCodecs": ["aac","mp3","ac3","eac3","alac","flac"] }
```
`audioCodecs` is what plays directly (a file in `<video>`); `hlsAudioCodecs` is what the HLS engine accepts in segments. With hls.js that is what `MediaSource.isTypeSupported` accepts, which can be less (the LG webOS browser decodes AC-3 in files but may refuse it through MediaSource). Remux/transcode copy or encode audio only into `hlsAudioCodecs`; omitted = `audioCodecs`.

Response `201`:
```json
{ "playSessionId": "…",
  "method": "transcode",
  "reasons": ["bitrate", "hdr_unsupported"],
  "engine": "hls",
  "mediaUrl": "/api/v1/play/5f1c…/master.m3u8",
  "mimeType": "application/vnd.apple.mpegurl",
  "timelineOffsetMs": 0,
  "startPositionMs": 125000,
  "runtimeMs": 3482000,
  "audioTrackId": 2, "subtitleTrackId": "e3",
  "subtitles": { "mode": "sidecar", "url": "/api/v1/items/…/subtitles/e3.vtt?offsetMs=0" },
  "connection": { "class": "remote", "maxBitrateKbps": 6000 },
  "transcode": {
    "backend": "nvenc", "hwDecode": true,
    "video": { "action": "encode", "codec": "h264", "encoder": "h264_nvenc", "width": 1920, "height": 1080, "maxrateKbps": 5227, "hdr": false, "tonemap": true },
    "audio": { "action": "encode", "codec": "aac", "channels": 2, "bitrateKbps": 192 } },
  "item": ItemSummary }
```
| Field | Values |
|---|---|
| `method` | `direct` (progressive file), `remux` (HLS, video copy), `transcode` (HLS, video encode) |
| `engine` | `html5` (set `video.src = mediaUrl`) or `hls` (native HLS or hls.js on `mediaUrl`) |
| `mimeType` | `video/mp4`, `video/x-matroska`, `video/webm`, … or `application/vnd.apple.mpegurl` |
| `timelineOffsetMs` | 0 for direct/transcode; the session start for remux when it did not start at 0. Absolute position = `timelineOffsetMs + video.currentTime*1000` |
| `startPositionMs` | Where the client should seek inside the media timeline after load (0 for remux, the requested start otherwise) |
| `subtitles.mode` | `none`, `sidecar` (client adds `<track src=url>`), `inband` (native HLS: select via `video.textTracks`, `url` absent) |
| `reasons` | `forced`, `container`, `video_codec`, `video_profile_level`, `video_bit_depth`, `video_resolution`, `hdr_unsupported`, `dolby_vision_profile`, `bitrate`, `audio_codec`, `audio_channels`, `audio_track_not_default`, `subtitle_image`, `hls_video_codec` (empty for `direct`) |
| `transcode` | `null` for `direct`; for `remux` `video.action = "copy"` |

Errors: `503 ffmpeg_missing`, `503 transcode_busy` (`details: {max, active}`), `404` for missing items, `400 validation` for unknown track ids.

### `POST /items/{id}/progress`
For clients that have no play session (not used by the web client, which reports through the session). Request `{ "positionMs": 125000, "state": "playing" | "paused" | "stopped" }` → `200 { "userData": {…} }`.

### `POST /items/{id}/played`
Request `{ "played": true }` → `200 { "userData": {…} }`. On a `series`/`season` applies to all episodes.

---

## 6. Home

### `GET /home`
```json
{ "continueWatching": [ItemSummary, …],       // in-progress movies/episodes, lastPlayed desc, max 20
  "nextUp": [ItemSummary, …],                 // next unplayed episode per series with history, max 20
  "recentlyAdded": [ItemSummary, …],          // movies + episodes by addedAt desc, max 30 (episodes carry seriesTitle)
  "libraries": [ { "library": Library, "sections": [ { "type": "movie", "count": 4 }, { "type": "series", "count": 5 } ] } ] }
```

---

## 7. Play sessions (HLS and progress)

All `/play/{sid}/*` routes require the session to belong to the caller (admin may access any). Every request refreshes the session's `lastActivity` (60 s idle timeout).

### `GET /play/{sid}/master.m3u8` → `application/vnd.apple.mpegurl`
Master playlist (one variant; `EXT-X-MEDIA` subtitle renditions only for `engine=native` sessions). `Cache-Control: no-store`.

### `GET /play/{sid}/index.m3u8`
Media playlist. Transcode sessions: complete predictive VOD playlist (static). Remux sessions: ffmpeg's EVENT playlist with `#EXT-X-START:TIME-OFFSET=0,PRECISE=YES` injected; blocks up to 15 s until ≥1 segment exists, else `503 not_ready`.

### `GET /play/{sid}/init.mp4` → `video/mp4` (init segment; blocks up to 15 s)
### `GET /play/{sid}/seg{NNNNN}.m4s` → `video/iso.segment`
Five-digit zero-padded index (`seg00012.m4s`). Blocks up to 30 s while the segment is produced; restarts ffmpeg at this segment when outside the producing window (transcode sessions). `404` for an index beyond the playlist; `503 not_ready` on timeout.

### `GET /play/{sid}/sub/{trackId}.m3u8` → one-segment VOD subtitle playlist referencing `{trackId}.vtt`
### `GET /play/{sid}/sub/{trackId}.vtt` → WebVTT shifted by the session's `timelineOffsetMs`

### `POST /play/{sid}/progress`
Request `{ "positionMs": 130000, "state": "playing" | "paused" | "stopped" }` (position is **absolute**, i.e. already includes `timelineOffsetMs`). Updates `user_item_data` (in-progress / played thresholds per DESIGN §7.5), keeps the session alive. `state: "stopped"` also ends the session. Response `200 { "userData": {…}, "played": false }`. Accepts `navigator.sendBeacon` bodies (`Blob` with `application/json`).

### `DELETE /play/{sid}` → `204` (stops ffmpeg, deletes the session directory). Idempotent.

### Admin
- `GET /admin/play-sessions` → `{ "items": [ { "id", "userId", "username", "deviceName", "ip", "connectionClass", "item": ItemSummary, "method", "reasons", "backend", "transcode": {…}, "positionMs", "state", "startedAt", "lastActivityAt", "segmentsProduced", "ffmpegRunning", "restarts" } ] }`
- `GET /admin/play-sessions/{sid}` → the same object plus `"ffmpegArgs": [...]`, `"ffmpegLogTail": "…"`
- `DELETE /admin/play-sessions/{sid}` → `204`

---

## 8. Admin settings and problems

### `GET /admin/settings`
```json
{ "serverName": "HomeStreaming", "remoteMaxBitrateKbps": 6000, "lanMaxBitrateKbps": 0, "tailnetIsLan": false,
  "transcodeMaxSessions": 3, "transcodePreset": "veryfast", "transcodeThreads": 0, "hlsSegmentSeconds": 6,
  "scanIntervalHours": 6, "playedThresholdPercent": 92, "audioTranscodeCodec": "auto",
  "profileOverrides": { "webos": { "audioCodecs": { "add": ["dts"], "remove": [] } } },
  "readOnly": { "hwaccel": "auto", "hwaccelActive": "nvenc", "transcodeDir": "…", "httpAddr": ":8420", "ffmpegPath": "…" } }
```
### `PATCH /admin/settings` — partial body with any writable key above → `200` full settings. Validation: bitrates ≥ 0, sessions 1–16, segment 2–10, threshold 50–100, preset ∈ libx264 presets.

### `GET /admin/problems?limit=100`
```json
{ "items": [ { "item": ItemSummary, "status": "pending", "statusReason": "ffprobe: moov atom not found", "probeAttempts": 3, "path": "…" } ],
  "counts": { "pending": 1, "missing": 2, "lowConfidence": 1 } }
```
Includes `pending`, `missing`, and `ok` items whose parse confidence was `low` (`statusReason: "naming:low_confidence"`).

### Reserved (Phase 2, not implemented in MVP → `404`)
`/admin/users`, `/admin/users/{id}`, `/admin/users/{id}/password`, `/admin/remote-access`, `/admin/metadata/*`.

---

## 9. Web client routes (served by the same binary)

| Path | Content |
|---|---|
| `/` and any non-`/api` path without a file extension | `index.html` (SPA; hash routing, so a server-side fallback is only a convenience) |
| `/assets/*` | hashed Vite assets, `Cache-Control: public, max-age=31536000, immutable` |
| `/manifest.webmanifest`, `/sw.js`, `/icons/*` | PWA files, `Cache-Control: no-cache` for `sw.js` |

## 10. Quick reference

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/system/info` | — | Server info, setupRequired, ffmpeg found |
| GET | `/system/ping` | — | Liveness |
| POST | `/system/setup` | — | First-run admin |
| GET | `/admin/status` | admin | Runtime status |
| GET | `/admin/fs` | admin | Directory browser |
| POST | `/auth/login` | — | Login |
| POST | `/auth/logout` | user | Logout |
| GET | `/auth/me` | user | Current user + connection |
| PATCH | `/auth/me/preferences` | user | Audio/subtitle preferences |
| POST | `/auth/me/password` | user | Change password |
| GET/DELETE | `/auth/sessions[/{id}]` | user | Devices |
| GET/POST | `/libraries` | user/admin | List / create |
| GET/PATCH/DELETE | `/libraries/{id}` | user/admin | Read / edit paths / delete |
| POST | `/libraries/{id}/scan`, `/libraries/scan` | admin | Scan |
| POST | `/libraries/{id}/clean` | admin | Remove missing |
| GET | `/tasks[/{id}]` | user | Task progress |
| GET | `/items` | user | Browse/search |
| GET | `/items/{id}` | user | Detail |
| GET | `/items/{id}/children` | user | Seasons/episodes |
| GET | `/items/{id}/image/{kind}` | user (+query token) | Art |
| GET | `/items/{id}/file` | user (+query token) | Direct play |
| GET | `/items/{id}/subtitles/{trackId}.vtt` | user (+query token) | Sidecar subtitles |
| POST | `/items/{id}/play-info` | user | Decision + session |
| POST | `/items/{id}/progress`, `/items/{id}/played` | user | User data |
| GET | `/home` | user | Home rows |
| GET | `/play/{sid}/master.m3u8`, `index.m3u8`, `init.mp4`, `seg{N}.m4s`, `sub/{t}.m3u8`, `sub/{t}.vtt` | user (+query token) | HLS |
| POST | `/play/{sid}/progress` | user | Progress + keepalive |
| DELETE | `/play/{sid}` | user | Stop |
| GET/DELETE | `/admin/play-sessions[/{sid}]` | admin | Now playing |
| GET/PATCH | `/admin/settings` | admin | Settings |
| GET | `/admin/problems` | admin | Pending/missing/low-confidence items |

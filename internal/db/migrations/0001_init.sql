CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  preferences_json TEXT NOT NULL DEFAULT '{}',
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  device_id TEXT NOT NULL,
  device_name TEXT NOT NULL,
  client_type TEXT NOT NULL,
  user_agent TEXT, ip TEXT,
  created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, expires_at TEXT NOT NULL);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE libraries (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK(type IN ('mixed','movies','shows')),
  created_at TEXT NOT NULL,
  last_scan_at TEXT, last_scan_status TEXT, last_scan_error TEXT);

CREATE TABLE library_paths (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  position INTEGER NOT NULL DEFAULT 0,
  UNIQUE(library_id, path));

CREATE TABLE items (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK(type IN ('movie','series','season','episode')),
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  parent_id TEXT REFERENCES items(id) ON DELETE CASCADE,
  series_id TEXT REFERENCES items(id) ON DELETE CASCADE,
  series_key TEXT,
  title TEXT NOT NULL, sort_title TEXT NOT NULL, year INTEGER, overview TEXT NOT NULL DEFAULT '',
  season_number INTEGER, episode_number INTEGER, episode_end_number INTEGER,
  premiere_date TEXT,
  library_path_id TEXT REFERENCES library_paths(id) ON DELETE CASCADE,
  rel_path TEXT,
  size INTEGER, mtime TEXT, container TEXT, runtime_ms INTEGER, bitrate INTEGER,
  video_codec TEXT, width INTEGER, height INTEGER, hdr TEXT, bit_depth INTEGER,
  audio_langs TEXT,
  probe_json TEXT,
  confidence TEXT NOT NULL DEFAULT 'high',
  status TEXT NOT NULL DEFAULT 'ok' CHECK(status IN ('ok','pending','missing')),
  status_reason TEXT, probe_attempts INTEGER NOT NULL DEFAULT 0,
  images_done INTEGER NOT NULL DEFAULT 0,
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
  idx INTEGER NOT NULL,
  type TEXT NOT NULL CHECK(type IN ('video','audio','subtitle')),
  codec TEXT NOT NULL, profile TEXT, level INTEGER, codec_tag TEXT,
  width INTEGER, height INTEGER, pix_fmt TEXT, bit_depth INTEGER, frame_rate REAL,
  color_transfer TEXT, color_primaries TEXT, color_space TEXT, hdr TEXT, dv_profile INTEGER, dv_bl_compat INTEGER,
  field_order TEXT, bitrate INTEGER,
  channels INTEGER, channel_layout TEXT, sample_rate INTEGER,
  language TEXT, title TEXT, is_default INTEGER NOT NULL DEFAULT 0, is_forced INTEGER NOT NULL DEFAULT 0,
  is_text INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(item_id, idx));

CREATE TABLE external_subtitles (
  id TEXT PRIMARY KEY,
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  rel_path TEXT NOT NULL,
  format TEXT NOT NULL,
  language TEXT, title TEXT, is_forced INTEGER NOT NULL DEFAULT 0, is_sdh INTEGER NOT NULL DEFAULT 0,
  UNIQUE(item_id, rel_path));

CREATE TABLE images (
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK(kind IN ('poster','backdrop','thumb')),
  source TEXT NOT NULL CHECK(source IN ('local','extracted','provider')),
  path TEXT NOT NULL,
  width INTEGER, height INTEGER, updated_at TEXT NOT NULL,
  PRIMARY KEY(item_id, kind));

CREATE TABLE chapters (
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  idx INTEGER NOT NULL, start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, title TEXT,
  PRIMARY KEY(item_id, idx));

CREATE TABLE user_item_data (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position_ms INTEGER NOT NULL DEFAULT 0,
  played INTEGER NOT NULL DEFAULT 0,
  play_count INTEGER NOT NULL DEFAULT 0,
  last_played_at TEXT,
  audio_language TEXT, subtitle_language TEXT,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id, item_id));
CREATE INDEX uid_last_played ON user_item_data(user_id, last_played_at);

CREATE TABLE login_attempts (
  key TEXT PRIMARY KEY,
  failures INTEGER NOT NULL,
  locked_until TEXT,
  updated_at TEXT NOT NULL);

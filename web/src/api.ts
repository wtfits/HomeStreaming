// API client: Bearer token from login (also stored as an HttpOnly cookie by
// the server), JSON helpers and media URL builder.

export interface Preferences {
  audioLanguage: string;
  subtitleLanguage: string;
  subtitleMode: 'off' | 'preferred' | 'always';
}
export interface User {
  id: string;
  username: string;
  isAdmin: boolean;
  preferences: Preferences;
}
export interface Connection {
  class: 'lan' | 'remote';
  ip?: string;
  maxBitrateKbps: number;
}
export interface UserData {
  positionMs: number;
  played: boolean;
  playCount: number;
  lastPlayedAt: string | null;
}
export interface ItemSummary {
  id: string;
  type: 'movie' | 'series' | 'season' | 'episode';
  libraryId: string;
  title: string;
  year: number | null;
  runtimeMs: number | null;
  addedAt: string;
  parentId: string | null;
  seriesId: string | null;
  seriesTitle: string | null;
  seasonNumber: number | null;
  episodeNumber: number | null;
  episodeEndNumber: number | null;
  images: { poster: boolean; backdrop: boolean; thumb: boolean };
  video: { codec: string; width: number; height: number; hdr: string | null; bitDepth: number } | null;
  audioLanguages: string[];
  childCount: number | null;
  unplayedCount: number | null;
  status: 'ok' | 'pending' | 'missing';
  userData: UserData;
}
export interface AudioTrack {
  id: number;
  codec: string;
  channels: number;
  language: string;
  title: string;
  displayName: string;
  isDefault: boolean;
}
export interface SubtitleTrack {
  id: string;
  codec: string;
  language: string;
  title: string;
  displayName: string;
  isText: boolean;
  isExternal: boolean;
  isForced: boolean;
}
export interface ItemDetail extends ItemSummary {
  path?: string;
  container?: string;
  bitrate?: number;
  size?: number;
  audioTracks?: AudioTrack[];
  subtitleTracks?: SubtitleTrack[];
  chapters?: { index: number; startMs: number; endMs: number; title: string }[];
  defaultAudioTrackId?: number | null;
  defaultSubtitleTrackId?: string | null;
  nextUp?: ItemSummary | null;
  statusReason?: string | null;
}
export interface Library {
  id: string;
  name: string;
  type: 'mixed' | 'movies' | 'shows';
  paths: string[];
  pathIds: string[];
  lastScanAt: string | null;
  lastScanStatus: string | null;
  lastScanError: string | null;
  counts: { movies: number; series: number; episodes: number; pending: number; missing: number };
  scanning: boolean;
  scanTaskId?: string;
}
export interface Task {
  id: string;
  kind: string;
  libraryId?: string;
  status: 'queued' | 'running' | 'done' | 'failed';
  phase: string;
  progress: { done: number; total: number };
  error: string | null;
  report?: Record<string, number>;
}
export interface SystemInfo {
  serverName: string;
  version: string;
  setupRequired: boolean;
  os: string;
  ffmpeg: { found: boolean; path?: string; version?: string; hint?: string; warning?: string };
  suggestedLibraryPath?: string;
}
export interface PlayInfo {
  playSessionId: string;
  method: 'direct' | 'remux' | 'transcode';
  reasons: string[];
  engine: 'html5' | 'hls';
  mediaUrl: string;
  mimeType: string;
  timelineOffsetMs: number;
  startPositionMs: number;
  runtimeMs: number;
  audioTrackId: number | null;
  subtitleTrackId: string | null;
  subtitles: { mode: 'none' | 'sidecar' | 'inband'; url?: string; trackId?: string };
  connection: { class: string; maxBitrateKbps: number };
  transcode: null | {
    backend: string;
    hwDecode: boolean;
    video: { action: string; codec: string; width: number; height: number; maxrateKbps: number; hdr: boolean; tonemap: boolean };
    audio: { action: string; codec: string; channels: number; bitrateKbps: number };
  };
}

export class ApiError extends Error {
  status: number;
  code: string;
  details: Record<string, any>;
  constructor(status: number, code: string, message: string, details?: Record<string, any>) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details || {};
  }
}

function store(key: string, value?: string | null): string {
  try {
    if (value === undefined) return localStorage.getItem(key) || '';
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch (_) {
    /* private mode */
  }
  return value || '';
}

export const prefs = {
  get: (k: string) => store('hs.' + k),
  set: (k: string, v: string | null) => store('hs.' + k, v),
};

let token = prefs.get('token');
let onUnauthorized: (() => void) | null = null;

export function setToken(t: string | null) {
  token = t || '';
  prefs.set('token', t);
}
export function getToken() {
  return token;
}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

function randomId(): string {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  return Array.prototype.map.call(b, (x: number) => ('0' + x.toString(16)).slice(-2)).join('');
}

export function deviceId(): string {
  let id = prefs.get('deviceId');
  if (!id) {
    id = randomId();
    prefs.set('deviceId', id);
  }
  return id;
}

export function deviceName(): string {
  const ua = navigator.userAgent;
  const browser = /Edg\//.test(ua) ? 'Edge' : /Web0S|webOS/.test(ua) ? 'LG TV' : /CriOS|Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : 'Browser';
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Windows/.test(ua) ? 'Windows' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'Mac' : /Web0S/.test(ua) ? 'webOS' : /Linux/.test(ua) ? 'Linux' : '';
  return browser === 'LG TV' ? 'LG TV' : os ? browser + ' on ' + os : browser;
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers['Authorization'] = 'Bearer ' + token;
  let res: Response;
  try {
    res = await fetch('/api/v1' + path, { method, headers, body: body !== undefined ? JSON.stringify(body) : undefined, credentials: 'same-origin' });
  } catch (e) {
    throw new ApiError(0, 'offline', 'offline');
  }
  if (res.status === 204) return undefined as unknown as T;
  let data: any = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch (_) {
      data = null;
    }
  }
  if (!res.ok) {
    const err = (data && data.error) || {};
    if (res.status === 401 && onUnauthorized && path !== '/auth/login') onUnauthorized();
    throw new ApiError(res.status, err.code || 'http_' + res.status, err.message || res.statusText, err.details);
  }
  return data as T;
}

/** Adds the auth token to a media URL (images, files, HLS, subtitles). */
export function mediaUrl(path: string): string {
  if (!token) return path;
  return path + (path.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(token);
}

export function imageUrl(id: string, kind: 'poster' | 'backdrop' | 'thumb', width: number): string {
  return mediaUrl('/api/v1/items/' + id + '/image/' + kind + '?w=' + width);
}

export function formatTime(ms: number): string {
  if (!isFinite(ms) || ms < 0) ms = 0;
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const mm = h > 0 ? ('0' + m).slice(-2) : String(m);
  return (h > 0 ? h + ':' : '') + mm + ':' + ('0' + sec).slice(-2);
}

export function formatRuntime(ms: number | null): string {
  if (!ms) return '';
  if (ms < 60000) return Math.round(ms / 1000) + 's';
  const min = Math.round(ms / 60000);
  if (min < 60) return min + 'm';
  return Math.floor(min / 60) + 'h ' + (min % 60) + 'm';
}

export function episodeLabel(it: ItemSummary): string {
  if (it.type !== 'episode') return '';
  const e = it.episodeEndNumber ? it.episodeNumber + '–' + it.episodeEndNumber : String(it.episodeNumber);
  return 'S' + it.seasonNumber + '·E' + e;
}

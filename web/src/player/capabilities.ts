// Builds the client capability profile sent to play-info (DESIGN §6.2).
import Hls from 'hls.js/light';
import { deviceId, deviceName, prefs } from '../api';
import { platform, Platform } from '../device';

export interface VideoCodecCap {
  codec: string;
  maxLevel?: number;
  maxBitDepth?: number;
  maxWidth?: number;
  maxHeight?: number;
  hdr?: string[];
}
export interface Profile {
  deviceId: string;
  deviceName: string;
  clientType: string;
  platform: Platform;
  engine: 'native' | 'hlsjs';
  containers: string[];
  videoCodecs: VideoCodecCap[];
  audioCodecs: string[];
  maxAudioChannels: number;
  hlsVideoCodecs: string[];
  /** Audio the HLS engine accepts in segments (MediaSource for hls.js). */
  hlsAudioCodecs: string[];
  maxBitrateKbps: number;
  forceTranscode: boolean;
}

const HDR_ALL = ['hdr10', 'hlg', 'dolbyvision'];

function base(p: Platform): Omit<Profile, 'deviceId' | 'deviceName' | 'clientType' | 'engine' | 'maxBitrateKbps' | 'forceTranscode' | 'hlsAudioCodecs'> {
  switch (p) {
    case 'ios':
      return {
        platform: p,
        containers: ['mp4', 'mov', 'm4v'],
        videoCodecs: [
          { codec: 'h264', maxLevel: 52, maxBitDepth: 8, maxWidth: 3840, maxHeight: 2160 },
          { codec: 'hevc', maxLevel: 153, maxBitDepth: 10, maxWidth: 3840, maxHeight: 2160, hdr: HDR_ALL },
        ],
        audioCodecs: ['aac', 'mp3', 'ac3', 'eac3', 'alac', 'flac'],
        maxAudioChannels: 6,
        hlsVideoCodecs: ['h264', 'hevc'],
      };
    case 'webos':
      return {
        platform: p,
        containers: ['mp4'],
        videoCodecs: [
          { codec: 'h264', maxLevel: 51, maxBitDepth: 8, maxWidth: 3840, maxHeight: 2160 },
          { codec: 'hevc', maxLevel: 153, maxBitDepth: 10, maxWidth: 3840, maxHeight: 2160, hdr: HDR_ALL },
        ],
        audioCodecs: ['aac', 'mp3', 'ac3', 'eac3'],
        maxAudioChannels: 6,
        hlsVideoCodecs: ['h264', 'hevc'],
      };
    case 'desktop':
    case 'android':
      return {
        platform: p,
        containers: ['mp4', 'webm', 'matroska'],
        videoCodecs: [
          { codec: 'h264', maxLevel: 51, maxBitDepth: 8, maxWidth: 3840, maxHeight: 2160 },
          { codec: 'vp9', maxBitDepth: 10, maxWidth: 3840, maxHeight: 2160 },
          { codec: 'av1', maxBitDepth: 10, maxWidth: 3840, maxHeight: 2160 },
        ],
        audioCodecs: ['aac', 'mp3', 'opus', 'flac', 'vorbis'],
        maxAudioChannels: 6,
        hlsVideoCodecs: ['h264'],
      };
    default:
      return {
        platform: 'other',
        containers: ['mp4'],
        videoCodecs: [{ codec: 'h264', maxLevel: 41, maxBitDepth: 8, maxWidth: 1920, maxHeight: 1080 }],
        audioCodecs: ['aac', 'mp3'],
        maxAudioChannels: 2,
        hlsVideoCodecs: ['h264'],
      };
  }
}

export function pickEngine(p: Platform): 'native' | 'hlsjs' {
  const forced = prefs.get('engine');
  if (forced === 'native' || forced === 'hlsjs') return forced;
  const v = document.createElement('video');
  const native = v.canPlayType('application/vnd.apple.mpegurl');
  if (p === 'ios' && native) return 'native';
  if (Hls.isSupported()) return 'hlsjs';
  return native ? 'native' : 'hlsjs';
}

const MSE = typeof window !== 'undefined' && (window as any).MediaSource;

function supported(engine: 'native' | 'hlsjs', mime: string): '' | 'maybe' | 'probably' {
  try {
    if (engine === 'hlsjs' && MSE && MSE.isTypeSupported) return MSE.isTypeSupported(mime) ? 'probably' : '';
    const r = document.createElement('video').canPlayType(mime);
    return r === 'probably' || r === 'maybe' ? r : '';
  } catch (_) {
    return '';
  }
}

const PROBES = {
  h264: 'video/mp4; codecs="avc1.640033"',
  hevc: 'video/mp4; codecs="hvc1.2.4.L153.B0"',
  hevc8: 'video/mp4; codecs="hvc1.1.6.L153.B0"',
  vp9: 'video/webm; codecs="vp09.00.10.08"',
  av1: 'video/mp4; codecs="av01.0.08M.08"',
  aac: 'audio/mp4; codecs="mp4a.40.2"',
  mp3: 'audio/mpeg',
  mp3mp4: 'audio/mp4; codecs="mp4a.40.34"',
  ac3: 'audio/mp4; codecs="ac-3"',
  eac3: 'audio/mp4; codecs="ec-3"',
  flac: 'audio/mp4; codecs="flac"',
  opus: 'audio/mp4; codecs="opus"',
  dts: 'audio/mp4; codecs="dtsc"',
  mkv: 'video/x-matroska; codecs="avc1.640028"',
} as Record<string, string>;

async function hdrSupported(engine: 'native' | 'hlsjs', transfer: 'pq' | 'hlg'): Promise<boolean | null> {
  const mc = (navigator as any).mediaCapabilities;
  if (!mc || !mc.decodingInfo) return null;
  try {
    const res = await mc.decodingInfo({
      type: engine === 'hlsjs' ? 'media-source' : 'file',
      video: { contentType: PROBES.hevc, width: 3840, height: 2160, bitrate: 20000000, framerate: 24, transferFunction: transfer, colorGamut: 'rec2020' },
    });
    return !!res.supported;
  } catch (_) {
    return null;
  }
}

let cached: Profile | null = null;

/** Build the capability profile: platform base profile confirmed by runtime probes. */
export async function buildProfile(): Promise<Profile> {
  if (cached) return withUserPrefs(cached);
  const p = platform();
  const engine = pickEngine(p);
  const b = base(p);
  const isPlatformBase = p === 'ios' || p === 'webos';
  const videoCodecs: VideoCodecCap[] = [];
  for (const vc of b.videoCodecs) {
    const r = supported(engine, PROBES[vc.codec] || '');
    // Base profiles of iOS/webOS are trusted when the probe says "maybe".
    if (r === 'probably' || (r === 'maybe' && isPlatformBase) || (isPlatformBase && vc.codec === 'h264')) videoCodecs.push({ ...vc });
  }
  // Desktop: add HEVC when the browser reports hardware decoding.
  if ((p === 'desktop' || p === 'android') && supported(engine, PROBES.hevc) === 'probably') {
    const hevc: VideoCodecCap = { codec: 'hevc', maxLevel: 153, maxBitDepth: 10, maxWidth: 3840, maxHeight: 2160, hdr: [] };
    if ((await hdrSupported(engine, 'pq')) === true) hevc.hdr!.push('hdr10', 'dolbyvision');
    if ((await hdrSupported(engine, 'hlg')) === true) hevc.hdr!.push('hlg');
    videoCodecs.push(hevc);
  }
  if (!videoCodecs.some((v) => v.codec === 'h264')) videoCodecs.unshift({ codec: 'h264', maxLevel: 51, maxBitDepth: 8, maxWidth: 1920, maxHeight: 1080 });

  const audioCodecs: string[] = [];
  for (const a of b.audioCodecs) {
    const r = supported(engine, PROBES[a] || '');
    if (r || isPlatformBase || a === 'aac' || a === 'mp3') audioCodecs.push(a);
  }
  for (const extra of ['ac3', 'eac3', 'dts']) {
    if (audioCodecs.indexOf(extra) < 0 && supported(engine, PROBES[extra]) === 'probably') audioCodecs.push(extra);
  }
  let containers = b.containers.slice();
  if (p === 'desktop' && !supported('native', PROBES.mkv) && !/Chrome\//.test(navigator.userAgent)) {
    containers = containers.filter((c) => c !== 'matroska');
  }
  // HLS segments are fMP4: with hls.js only what MediaSource accepts works,
  // even when the <video> element plays the same codec in a file (webOS + AC-3).
  let hlsAudioCodecs = audioCodecs.slice();
  if (engine === 'hlsjs') {
    const mseProbe: Record<string, string> = { mp3: PROBES.mp3mp4 };
    hlsAudioCodecs = audioCodecs.filter((a) => a === 'aac' || supported('hlsjs', mseProbe[a] || PROBES[a] || '') === 'probably');
  }
  if (hlsAudioCodecs.indexOf('aac') < 0) hlsAudioCodecs.unshift('aac');
  // Formats this device's MediaSource refused before, although it claimed support.
  const rejected = rejectedCodecs();
  hlsAudioCodecs = hlsAudioCodecs.filter((a) => rejected.indexOf(a) < 0);
  let hlsVideoCodecs = b.hlsVideoCodecs.filter((c) => videoCodecs.some((v) => v.codec === c));
  if (videoCodecs.some((v) => v.codec === 'hevc') && hlsVideoCodecs.indexOf('hevc') < 0) hlsVideoCodecs.push('hevc');
  hlsVideoCodecs = hlsVideoCodecs.filter((c) => c === 'h264' || rejected.indexOf(c) < 0);

  cached = {
    deviceId: deviceId(),
    deviceName: deviceName(),
    clientType: 'web',
    platform: p,
    engine,
    containers,
    videoCodecs,
    audioCodecs,
    maxAudioChannels: b.maxAudioChannels,
    hlsVideoCodecs,
    hlsAudioCodecs,
    maxBitrateKbps: 0,
    forceTranscode: false,
  };
  return withUserPrefs(cached);
}

function withUserPrefs(p: Profile): Profile {
  return {
    ...p,
    maxBitrateKbps: parseInt(prefs.get('maxBitrate') || '0', 10) || 0,
    forceTranscode: prefs.get('forceTranscode') === '1',
  };
}

const REJECTED_KEY = 'mseRejected';

function rejectedCodecs(): string[] {
  return (prefs.get(REJECTED_KEY) || '').split(',').filter(Boolean);
}

/**
 * Remembers a codec this device's MediaSource refused (hls.js
 * bufferAddCodecError carries the mime type), so the next play-info asks
 * the server for something else. H.264 and AAC are never excluded: they are
 * the fallback.
 */
export function rememberRejectedMime(mime: string) {
  const m = mime.toLowerCase();
  const map: [RegExp, string][] = [
    [/ac-3/, 'ac3'],
    [/ec-3/, 'eac3'],
    [/hvc1|hev1/, 'hevc'],
    [/flac/, 'flac'],
    [/opus/, 'opus'],
    [/mp4a\.40\.34|mp4a\.6b/, 'mp3'],
    [/alac/, 'alac'],
  ];
  const list = rejectedCodecs();
  for (const [re, codec] of map) {
    if (re.test(m) && list.indexOf(codec) < 0) list.push(codec);
  }
  prefs.set(REJECTED_KEY, list.length ? list.join(',') : null);
  cached = null;
}

/**
 * The most compatible profile, used after a playback error: H.264 video and
 * AAC stereo audio, transcoded, which every browser's MediaSource accepts.
 */
export function safeProfile(p: Profile): Profile {
  return {
    ...p,
    forceTranscode: true,
    videoCodecs: p.videoCodecs.filter((v) => v.codec === 'h264').map((v) => ({ ...v, maxLevel: Math.min(v.maxLevel || 51, 51) })),
    hlsVideoCodecs: ['h264'],
    hlsAudioCodecs: ['aac'],
    audioCodecs: ['aac'],
    maxAudioChannels: 2,
  };
}

export function resetProfileCache() {
  cached = null;
}

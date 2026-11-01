import Hls from 'hls.js/light';
import { useEffect, useRef, useState } from 'preact/hooks';
import { api, ApiError, episodeLabel, formatTime, getToken, ItemDetail, ItemSummary, mediaUrl, PlayInfo, prefs } from '../api';
import { Menu, MenuOption } from '../components/ui';
import { isIOS, isTV } from '../device';
import { t } from '../i18n/en';
import { focusEl, isBackKey, KEY, setPlayerKeys } from '../nav/spatial';
import { navigate, Route } from '../router';
import { buildProfile, rememberRejectedMime, safeProfile } from './capabilities';

const QUALITIES = [0, 20000, 10000, 8000, 4000, 2000, 1000];
const REASON_TEXT: Record<string, string> = {
  forced: 'forced by settings',
  container: 'container',
  video_codec: 'video codec',
  video_profile_level: 'video level',
  video_bit_depth: '10-bit video',
  video_resolution: 'resolution',
  hdr_unsupported: 'HDR not supported',
  dolby_vision_profile: 'Dolby Vision profile',
  bitrate: 'bitrate above limit',
  audio_codec: 'audio codec',
  audio_channels: 'audio channels',
  audio_track_not_default: 'non-default audio track',
  subtitle_image: 'image subtitles',
  hls_video_codec: 'codec not streamable',
};

type MenuKind = 'audio' | 'sub' | 'quality' | null;

export function Player({ route }: { route: Route }) {
  const id = route.segments[1];
  const videoRef = useRef<HTMLVideoElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const playBtnRef = useRef<HTMLButtonElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  const infoRef = useRef<PlayInfo | null>(null);
  const retriedRef = useRef(false);
  const lastReportRef = useRef(0);
  const exitingRef = useRef(false);

  const [item, setItem] = useState<ItemDetail | null>(null);
  const [info, setInfo] = useState<PlayInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [paused, setPaused] = useState(true);
  const [pos, setPos] = useState(0);
  const [buffered, setBuffered] = useState(0);
  const [controls, setControls] = useState(true);
  const [menu, setMenu] = useState<MenuKind>(null);
  const [needTap, setNeedTap] = useState(false);
  const [waiting, setWaiting] = useState(true);
  const [nextEp, setNextEp] = useState<ItemSummary | null>(null);
  const [upNext, setUpNext] = useState<number | null>(null);
  const [subId, setSubId] = useState<string | null>(null);
  const [volume, setVolumeState] = useState(() => {
    const s = parseFloat(prefs.get('volume') || '');
    return isNaN(s) ? 1 : Math.max(0, Math.min(1, s));
  });
  const [muted, setMutedState] = useState(() => prefs.get('muted') === '1');

  const runtime = (info && info.runtimeMs) || (item && item.runtimeMs) || 0;
  const absPos = () => {
    const v = videoRef.current;
    const i = infoRef.current;
    if (!v || !i) return 0;
    return Math.round(i.timelineOffsetMs + v.currentTime * 1000);
  };

  // ---- progress reporting ----
  const report = (state: string, positionMs?: number, beacon = false) => {
    const i = infoRef.current;
    if (!i) return;
    const body = { positionMs: positionMs !== undefined ? positionMs : absPos(), state };
    lastReportRef.current = Date.now();
    const url = '/api/v1/play/' + i.playSessionId + '/progress';
    if (beacon && navigator.sendBeacon) {
      navigator.sendBeacon(url, new Blob([JSON.stringify(body)], { type: 'application/json' }));
      return;
    }
    api('POST', '/play/' + i.playSessionId + '/progress', body).catch(() => undefined);
  };

  // ---- session start / media attach ----
  const detach = () => {
    if (hlsRef.current) {
      hlsRef.current.destroy();
      hlsRef.current = null;
    }
    const v = videoRef.current;
    if (v) {
      Array.prototype.slice.call(v.querySelectorAll('track')).forEach((tr: Element) => tr.remove());
      v.removeAttribute('src');
      try {
        v.load();
      } catch (_) {
        /* ignore */
      }
    }
  };

  const setSidecar = (url: string | null) => {
    const v = videoRef.current;
    if (!v) return;
    Array.prototype.slice.call(v.querySelectorAll('track')).forEach((tr: Element) => tr.remove());
    if (!url) return;
    const tr = document.createElement('track');
    tr.kind = 'subtitles';
    tr.label = 'Subtitles';
    tr.default = true;
    tr.src = mediaUrl(url);
    v.appendChild(tr);
    tr.addEventListener('load', () => {
      if (tr.track) tr.track.mode = 'showing';
    });
    setTimeout(() => {
      if (tr.track) tr.track.mode = 'showing';
    }, 0);
  };

  const selectInband = (trackId: string | null) => {
    const v = videoRef.current;
    const it = item;
    if (!v || !it) return;
    const sub = trackId ? (it.subtitleTracks || []).find((s) => s.id === trackId) : null;
    for (let k = 0; k < v.textTracks.length; k++) {
      const tt = v.textTracks[k];
      tt.mode = sub && (tt.label === sub.displayName || (sub.language && tt.language && sub.language.indexOf(tt.language) === 0)) ? 'showing' : 'disabled';
    }
  };

  const start = async (opts: { startMs: number; audio?: number | null; sub?: string | null; safe?: boolean; replaces?: string }) => {
    setError(null);
    setWaiting(true);
    let profile = await buildProfile();
    if (opts.safe) profile = safeProfile(profile);
    const body: Record<string, unknown> = { profile, startPositionMs: Math.max(0, Math.round(opts.startMs)) };
    if (opts.audio !== undefined && opts.audio !== null) body.audioTrackId = opts.audio;
    if (opts.sub !== undefined) body.subtitleTrackId = opts.sub;
    if (opts.replaces) body.replacesPlaySessionId = opts.replaces;
    let pi: PlayInfo;
    try {
      pi = await api<PlayInfo>('POST', '/items/' + id + '/play-info', body);
    } catch (e: any) {
      if (e instanceof ApiError && e.code === 'transcode_busy') setError(t('busy'));
      else if (e instanceof ApiError && e.code === 'ffmpeg_missing') setError(t('ffmpegMissing'));
      else if (e instanceof ApiError && e.code === 'not_ready') setError(t('notReady'));
      else setError(e.message || t('playbackError'));
      return;
    }
    detach();
    infoRef.current = pi;
    setInfo(pi);
    setSubId(pi.subtitleTrackId);
    const v = videoRef.current!;
    const url = mediaUrl(pi.mediaUrl);
    const seekTo = pi.startPositionMs / 1000;
    if (pi.engine === 'hls' && profile.engine === 'hlsjs' && Hls.isSupported()) {
      const hls = new Hls({
        startPosition: pi.method === 'transcode' && seekTo > 0 ? seekTo : -1,
        fragLoadingTimeOut: 60000,
        manifestLoadingTimeOut: 30000,
        levelLoadingTimeOut: 30000,
        enableWebVTT: false,
        enableCEA708Captions: false,
        maxBufferLength: 60,
        backBufferLength: 30,
        xhrSetup: (xhr) => {
          xhr.withCredentials = false;
        },
      });
      hlsRef.current = hls;
      hls.on(Hls.Events.ERROR, (_e, data) => {
        const d = data as any;
        if (data.details === Hls.ErrorDetails.BUFFER_ADD_CODEC_ERROR && d.mimeType) rememberRejectedMime(String(d.mimeType));
        if (!data.fatal) return;
        let detail = 'hls: ' + data.details;
        if (d.mimeType) detail += ' ' + d.mimeType;
        else if (d.error && d.error.message) detail += ' ' + String(d.error.message).slice(0, 160);
        onMediaError(detail);
      });
      hls.loadSource(url);
      hls.attachMedia(v);
    } else {
      v.src = url;
      if (seekTo > 0 && pi.method !== 'remux') {
        const onMeta = () => {
          v.currentTime = seekTo;
          v.removeEventListener('loadedmetadata', onMeta);
        };
        v.addEventListener('loadedmetadata', onMeta);
      }
    }
    if (pi.subtitles.mode === 'sidecar' && pi.subtitles.url) setSidecar(pi.subtitles.url);
    if (pi.subtitles.mode === 'inband') {
      const onTracks = () => selectInband(pi.subtitleTrackId);
      v.addEventListener('loadedmetadata', onTracks, { once: true } as any);
    }
    const p = v.play();
    if (p && p.catch) {
      p.catch(() => setNeedTap(true));
    }
  };

  const onMediaError = (detail: string) => {
    const i = infoRef.current;
    if (!i || exitingRef.current) return;
    // Tell the server (it logs it), so failures on the TV can be diagnosed.
    api('POST', '/play/' + i.playSessionId + '/progress', { positionMs: absPos(), state: 'paused', error: detail }).catch(() => undefined);
    if (!retriedRef.current) {
      retriedRef.current = true;
      setNotice(t('retryingTranscode'));
      start({ startMs: absPos(), audio: i.audioTrackId, sub: i.subtitleTrackId, safe: true, replaces: i.playSessionId });
      return;
    }
    setError(t('playbackError') + ' (' + detail + ')');
  };

  // ---- initial load ----
  useEffect(() => {
    let alive = true;
    retriedRef.current = false;
    exitingRef.current = false;
    setUpNext(null);
    setNextEp(null);
    (async () => {
      let it: ItemDetail;
      try {
        it = await api<ItemDetail>('GET', '/items/' + id);
      } catch (e: any) {
        setError(e.message);
        return;
      }
      if (!alive) return;
      setItem(it);
      const q = route.query;
      const startMs = q.t ? parseInt(q.t, 10) : q.resume === '1' ? it.userData.positionMs : 0;
      const audio = q.audio !== undefined && q.audio !== '' ? parseInt(q.audio, 10) : undefined;
      const sub = q.sub === 'off' ? null : q.sub || undefined;
      start({ startMs, audio, sub });
      if (it.type === 'episode' && it.seriesId) {
        api<{ items: ItemSummary[] }>('GET', '/items/' + it.seriesId + '/children?type=episode').then((r) => {
          const idx = r.items.findIndex((e) => e.id === it.id);
          if (idx >= 0 && idx + 1 < r.items.length) setNextEp(r.items[idx + 1]);
        });
      }
    })();
    return () => {
      alive = false;
    };
  }, [id]);

  // ---- video events ----
  useEffect(() => {
    const v = videoRef.current;
    if (!v) return;
    const onTime = () => {
      setPos(absPos());
      if (v.buffered.length) setBuffered((infoRef.current ? infoRef.current.timelineOffsetMs : 0) + v.buffered.end(v.buffered.length - 1) * 1000);
      if (!v.paused && Date.now() - lastReportRef.current > 10000) report('playing');
    };
    const onPlay = () => {
      setPaused(false);
      setNeedTap(false);
      setNotice(null);
    };
    const onPause = () => {
      setPaused(true);
      if (!v.ended) report('paused');
    };
    const onWaiting = () => setWaiting(true);
    const onPlaying = () => setWaiting(false);
    const onEnded = () => {
      report('ended', runtimeRef.current);
      if (nextRef.current) setUpNext(10);
    };
    const onErr = () => {
      if (!hlsRef.current && v.error) onMediaError('media error ' + v.error.code);
    };
    const onSeeked = () => report(v.paused ? 'paused' : 'playing');
    // Volume: restore the saved level, then mirror every change (slider, keys, OS) into state + prefs.
    v.volume = volume;
    v.muted = muted;
    const onVolume = () => {
      setVolumeState(v.volume);
      setMutedState(v.muted);
      prefs.set('volume', String(Math.round(v.volume * 100) / 100));
      prefs.set('muted', v.muted ? '1' : null);
    };
    v.addEventListener('volumechange', onVolume);
    v.addEventListener('timeupdate', onTime);
    v.addEventListener('play', onPlay);
    v.addEventListener('pause', onPause);
    v.addEventListener('waiting', onWaiting);
    v.addEventListener('playing', onPlaying);
    v.addEventListener('canplay', onPlaying);
    v.addEventListener('ended', onEnded);
    v.addEventListener('error', onErr);
    v.addEventListener('seeked', onSeeked);
    const onHide = () => {
      if (!exitingRef.current) report('stopped', undefined, true);
    };
    window.addEventListener('pagehide', onHide);
    return () => {
      v.removeEventListener('timeupdate', onTime);
      v.removeEventListener('play', onPlay);
      v.removeEventListener('pause', onPause);
      v.removeEventListener('waiting', onWaiting);
      v.removeEventListener('playing', onPlaying);
      v.removeEventListener('canplay', onPlaying);
      v.removeEventListener('ended', onEnded);
      v.removeEventListener('error', onErr);
      v.removeEventListener('seeked', onSeeked);
      v.removeEventListener('volumechange', onVolume);
      window.removeEventListener('pagehide', onHide);
    };
  }, []);

  const runtimeRef = useRef(0);
  runtimeRef.current = runtime;
  const nextRef = useRef<ItemSummary | null>(null);
  nextRef.current = nextEp;

  // Stop the session when leaving the player.
  useEffect(
    () => () => {
      exitingRef.current = true;
      const i = infoRef.current;
      if (i) {
        const v = videoRef.current;
        const p = v ? Math.round(i.timelineOffsetMs + v.currentTime * 1000) : 0;
        api('POST', '/play/' + i.playSessionId + '/progress', { positionMs: v && v.ended ? runtimeRef.current : p, state: 'stopped' }).catch(() => undefined);
      }
      detach();
      setPlayerKeys(null);
    },
    []
  );

  // ---- up next countdown ----
  const upNextShown = upNext !== null;
  useEffect(() => {
    if (!upNextShown || !isTV()) return;
    const el = wrapRef.current && (wrapRef.current.querySelector('.up-next .btn-primary') as HTMLElement | null);
    if (el) focusEl(el);
  }, [upNextShown]);
  useEffect(() => {
    if (upNext === null) return;
    if (upNext <= 0) {
      if (nextEp) navigate('/play/' + nextEp.id, true);
      return;
    }
    const tm = setTimeout(() => setUpNext((n) => (n === null ? null : n - 1)), 1000);
    return () => clearTimeout(tm);
  }, [upNext]);

  // ---- controls auto-hide ----
  const hideTimer = useRef<number | undefined>(undefined);
  const poke = () => {
    setControls(true);
    if (hideTimer.current) clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => {
      const v = videoRef.current;
      if (v && !v.paused && !menuRef.current) setControls(false);
    }, 3000);
  };
  const menuRef = useRef<MenuKind>(null);
  menuRef.current = menu;
  useEffect(() => {
    poke();
  }, [paused]);

  // ---- actions ----
  const togglePlay = () => {
    const v = videoRef.current;
    if (!v) return;
    if (v.paused) v.play().catch(() => setNeedTap(true));
    else v.pause();
    poke();
  };

  const seekAbs = (targetMs: number) => {
    const v = videoRef.current;
    const i = infoRef.current;
    if (!v || !i) return;
    const A = Math.max(0, Math.min(targetMs, (runtimeRef.current || targetMs) - 500));
    if (i.method !== 'remux') {
      v.currentTime = A / 1000;
      setPos(A);
      return;
    }
    // Remux: inside the produced range → seek; otherwise start a new session there.
    let end = 0;
    if (v.seekable && v.seekable.length) end = v.seekable.end(v.seekable.length - 1) * 1000;
    if (A >= i.timelineOffsetMs && A <= i.timelineOffsetMs + end) {
      v.currentTime = (A - i.timelineOffsetMs) / 1000;
      setPos(A);
      return;
    }
    setPos(A);
    start({ startMs: A, audio: i.audioTrackId, sub: i.subtitleTrackId, replaces: i.playSessionId });
  };

  const changeVolume = (vol: number) => {
    const v = videoRef.current;
    if (!v) return;
    v.volume = Math.max(0, Math.min(1, vol));
    v.muted = v.volume === 0;
    poke();
  };

  const toggleMute = () => {
    const v = videoRef.current;
    if (!v) return;
    if (v.muted || v.volume === 0) {
      if (v.volume === 0) v.volume = 0.5;
      v.muted = false;
    } else v.muted = true;
    poke();
  };

  const skip = (ms: number) => {
    seekAbs(absPos() + ms);
    poke();
  };

  const restartWith = (patch: { audio?: number | null; sub?: string | null }) => {
    const i = infoRef.current;
    if (!i) return;
    start({
      startMs: absPos(),
      audio: patch.audio !== undefined ? patch.audio : i.audioTrackId,
      sub: patch.sub !== undefined ? patch.sub : i.subtitleTrackId,
      replaces: i.playSessionId,
    });
  };

  const chooseSub = (sid: string) => {
    const i = infoRef.current;
    if (!i || !item) return;
    const target = sid === 'off' ? null : sid;
    setSubId(target);
    if (i.subtitles.mode === 'inband' || (i.engine === 'hls' && isIOS() && i.method !== 'direct')) {
      selectInband(target);
      return;
    }
    setSidecar(target ? `/api/v1/items/${item.id}/subtitles/${target}.vtt?offsetMs=${i.timelineOffsetMs}` : null);
  };

  const fullscreen = () => {
    const v = videoRef.current as any;
    const w = wrapRef.current as any;
    if (isIOS() && v && v.webkitEnterFullscreen) {
      v.webkitEnterFullscreen();
      return;
    }
    const d = document as any;
    if (d.fullscreenElement || d.webkitFullscreenElement) (d.exitFullscreen || d.webkitExitFullscreen).call(d);
    else if (w) (w.requestFullscreen || w.webkitRequestFullscreen).call(w);
  };

  const exit = () => {
    exitingRef.current = true;
    if (history.length > 1) history.back();
    else navigate('/item/' + id, true);
  };

  // ---- keys (TV remote + keyboard) ----
  useEffect(() => {
    setPlayerKeys((e: KeyboardEvent) => {
      if (menuRef.current) return false; // the menu handles its own keys
      const tv = isTV();
      const inControls = !!(document.activeElement && (document.activeElement as HTMLElement).closest('.player-controls'));
      if (isBackKey(e)) {
        if (upNextRef.current !== null) {
          setUpNext(null);
          return true;
        }
        if (tv && controlsRef.current && inControls && !videoRef.current!.paused) {
          setControls(false);
          (document.activeElement as HTMLElement).blur();
          return true;
        }
        exit();
        return true;
      }
      const k = e.keyCode;
      if (k === KEY.PLAY) {
        videoRef.current!.play();
        return true;
      }
      if (k === KEY.PAUSE) {
        videoRef.current!.pause();
        return true;
      }
      if (k === KEY.STOP) {
        exit();
        return true;
      }
      if (k === KEY.REWIND) {
        skip(-30000);
        return true;
      }
      if (k === KEY.FAST_FORWARD) {
        skip(30000);
        return true;
      }
      if (tv && inControls && controlsRef.current) {
        poke();
        return false; // spatial nav moves between control buttons
      }
      switch (k) {
        case KEY.LEFT:
          skip(-10000);
          return true;
        case KEY.RIGHT:
          skip(10000);
          return true;
        case KEY.UP:
        case KEY.DOWN:
          if (tv) {
            if (k === KEY.DOWN && controlsRef.current) {
              setControls(false);
              return true;
            }
            poke();
            setTimeout(() => playBtnRef.current && focusEl(playBtnRef.current), 20);
            return true;
          }
          if (videoRef.current) changeVolume(videoRef.current.volume + (k === KEY.UP ? 0.1 : -0.1));
          return true;
        case KEY.ENTER:
          if (tv) {
            togglePlay();
            return true;
          }
          return false;
        case 32: // space
        case 75: // k
          togglePlay();
          return true;
        case 77: // m
          toggleMute();
          return true;
        case 70: // f
          fullscreen();
          return true;
        case 67: // c
          chooseSub(subId ? 'off' : (item && item.subtitleTracks && item.subtitleTracks.find((s) => s.isText)?.id) || 'off');
          return true;
      }
      return false;
    });
    return () => setPlayerKeys(null);
  }, [item, subId]);
  const controlsRef = useRef(controls);
  controlsRef.current = controls;
  const upNextRef = useRef<number | null>(upNext);
  upNextRef.current = upNext;

  // ---- menus ----
  const menuOptions = (): MenuOption[] => {
    if (!item || !info) return [];
    if (menu === 'audio') return (item.audioTracks || []).map((a) => ({ id: String(a.id), label: a.displayName, selected: a.id === info.audioTrackId }));
    if (menu === 'sub')
      return [{ id: 'off', label: t('subtitlesOff'), selected: !subId }].concat(
        (item.subtitleTracks || []).filter((s) => s.isText).map((s) => ({ id: s.id, label: s.displayName, selected: s.id === subId }))
      );
    const cur = parseInt(prefs.get('maxBitrate') || '0', 10) || 0;
    const opts: MenuOption[] = QUALITIES.map((q) => ({
      id: String(q),
      label: q === 0 ? (info.connection.maxBitrateKbps ? t('qualityAutoCap', { mbps: info.connection.maxBitrateKbps / 1000, cls: info.connection.class }) : t('qualityAuto')) : q / 1000 + ' Mbps',
      selected: q === cur,
    }));
    opts.push({ id: 'force', label: t('forceTranscode'), selected: prefs.get('forceTranscode') === '1' });
    return opts;
  };
  const onMenuSelect = (sel: string) => {
    if (!info) return;
    if (menu === 'audio') restartWith({ audio: parseInt(sel, 10) });
    else if (menu === 'sub') chooseSub(sel);
    else {
      if (sel === 'force') prefs.set('forceTranscode', prefs.get('forceTranscode') === '1' ? null : '1');
      else prefs.set('maxBitrate', sel === '0' ? null : sel);
      restartWith({});
    }
  };

  const methodLabel = info ? (info.method === 'direct' ? t('methodDirect') : info.method === 'remux' ? t('methodRemux') : t('methodTranscode')) : '';
  const why = info && info.reasons.length ? t('whyTranscoding', { reasons: info.reasons.map((r) => REASON_TEXT[r] || r).join(', ') }) : '';
  const tc = info && info.transcode;
  const title = item ? (item.type === 'episode' ? (item.seriesTitle || '') + ' · ' + episodeLabel(item) + ' · ' + item.title : item.title) : '';

  return (
    <div
      class={'player' + (controls ? ' show-controls' : '')}
      ref={wrapRef}
      onMouseMove={poke}
      onTouchStart={poke}
      data-testid="player"
    >
      <video
        ref={videoRef}
        class="player-video"
        playsInline
        preload="metadata"
        onClick={() => (isTV() ? undefined : controls ? togglePlay() : poke())}
        data-testid="video"
        x-webkit-airplay="allow"
      />
      {waiting && !error && !paused ? <div class="player-spinner"><div class="spinner" /></div> : null}
      {needTap && !error ? (
        <button class="big-play" onClick={togglePlay} aria-label={t('play')}>
          ▶
        </button>
      ) : null}
      {error ? (
        <div class="player-error">
          <p>{error}</p>
          <button class="btn" data-autofocus onClick={exit}>
            {t('back')}
          </button>
        </div>
      ) : null}
      {notice ? <div class="player-notice">{notice}</div> : null}

      <div class="player-top">
        <button class="icon-btn" onClick={exit} aria-label={t('back')}>
          ←
        </button>
        <div class="player-title">{title}</div>
      </div>

      <div class="player-controls" data-nav-section>
        <div class="seek-row">
          <span class="time">{formatTime(pos)}</span>
          <div class="seek">
            <div class="seek-buffer" style={{ width: (runtime ? Math.min(100, (buffered / runtime) * 100) : 0) + '%' }} />
            <input
              type="range"
              min={0}
              max={runtime || 1}
              step={1000}
              value={pos}
              aria-label="Seek"
              tabIndex={isTV() ? -1 : 0}
              onInput={(e) => setPos(parseInt((e.target as HTMLInputElement).value, 10))}
              onChange={(e) => seekAbs(parseInt((e.target as HTMLInputElement).value, 10))}
            />
          </div>
          <span class="time">{formatTime(runtime)}</span>
        </div>
        <div class="button-row">
          <button class="icon-btn" onClick={() => skip(-10000)} aria-label={t('rewind')}>
            ⟲10
          </button>
          <button class="icon-btn play-btn" ref={playBtnRef} onClick={togglePlay} aria-label={paused ? t('play') : 'Pause'} data-testid="play-pause">
            {paused ? '▶' : '❚❚'}
          </button>
          <button class="icon-btn" onClick={() => skip(10000)} aria-label={t('forward')}>
            10⟳
          </button>
          {isTV() ? null : (
            <div class="volume">
              <button class="icon-btn" onClick={toggleMute} aria-label={muted || volume === 0 ? t('unmute') : t('mute')} data-testid="mute">
                {muted || volume === 0 ? '🔇' : volume < 0.5 ? '🔉' : '🔊'}
              </button>
              {isIOS() ? null : (
                <input
                  type="range"
                  min={0}
                  max={100}
                  step={1}
                  value={muted ? 0 : Math.round(volume * 100)}
                  aria-label={t('volume')}
                  data-testid="volume"
                  onInput={(e) => changeVolume(parseInt((e.target as HTMLInputElement).value, 10) / 100)}
                />
              )}
            </div>
          )}
          <div class="spacer" />
          <span class="method muted small" title={why} data-testid="method">
            {methodLabel}
            {tc ? ' · ' + tc.backend + (tc.video.action === 'encode' ? ' ' + tc.video.codec.toUpperCase() + ' ' + tc.video.height + 'p' : '') : ''}
          </span>
          {item && item.audioTracks && item.audioTracks.length > 1 ? (
            <button class="btn btn-small" onClick={() => setMenu('audio')} data-testid="audio-menu">
              {t('audio')}
            </button>
          ) : null}
          {item && item.subtitleTracks && item.subtitleTracks.some((s) => s.isText) ? (
            <button class="btn btn-small" onClick={() => setMenu('sub')} data-testid="sub-menu">
              {t('subtitles')}
            </button>
          ) : null}
          <button class="btn btn-small" onClick={() => setMenu('quality')} data-testid="quality-menu">
            {t('quality')}
          </button>
          <button class="icon-btn" onClick={fullscreen} aria-label={t('fullscreen')}>
            ⛶
          </button>
        </div>
        {why ? <div class="why muted small">{why}</div> : null}
      </div>

      {upNext !== null && nextEp ? (
        <div class="up-next" data-focus-trap>
          <div class="muted small">{t('upNext')}</div>
          <div class="up-next-title">
            {episodeLabel(nextEp)} · {nextEp.title}
          </div>
          <div class="muted small">{t('playingIn', { n: upNext })}</div>
          <div class="actions">
            <button class="btn btn-primary" data-autofocus onClick={() => navigate('/play/' + nextEp.id, true)}>
              {t('playNow')}
            </button>
            <button class="btn" onClick={() => setUpNext(null)}>
              {t('cancel')}
            </button>
          </div>
        </div>
      ) : null}

      {menu ? <Menu title={menu === 'audio' ? t('audio') : menu === 'sub' ? t('subtitles') : t('quality')} options={menuOptions()} onSelect={onMenuSelect} onClose={() => setMenu(null)} /> : null}
      <span hidden data-token={getToken() ? '1' : '0'} />
    </div>
  );
}

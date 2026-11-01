import { useEffect, useState } from 'preact/hooks';
import { api, episodeLabel, formatRuntime, formatTime, imageUrl, ItemDetail, ItemSummary } from '../api';
import { Badges, ErrorBox, ProgressBar, Spinner, useAsync } from '../components/ui';
import { episodesLabel, t } from '../i18n/en';
import { focusFirst } from '../nav/spatial';
import { href, navigate, qs, Route } from '../router';

function playHref(id: string, opts: { resume?: boolean; audio?: number | null; sub?: string | null }) {
  return '/play/' + id + qs({ resume: opts.resume ? 1 : undefined, audio: opts.audio ?? undefined, sub: opts.sub === null ? 'off' : opts.sub || undefined });
}

export function Item({ route }: { route: Route }) {
  const id = route.segments[1];
  const { data, error, loading, reload } = useAsync(() => api<ItemDetail>('GET', '/items/' + id), [id]);
  useEffect(() => {
    if (data) setTimeout(() => focusFirst(), 60);
  }, [data && data.id]);
  if (loading && !data) return <Spinner />;
  if (error) return <ErrorBox message={error} onRetry={reload} />;
  if (!data) return null;
  if (data.type === 'season' && data.seriesId) {
    navigate('/item/' + data.seriesId + qs({ season: data.seasonNumber }), true);
    return null;
  }
  return data.type === 'series' ? <Series item={data} route={route} reload={reload} /> : <Playable item={data} reload={reload} />;
}

function Backdrop({ it }: { it: ItemSummary }) {
  const kind = it.images.backdrop ? 'backdrop' : it.images.thumb ? 'thumb' : null;
  const [ok, setOk] = useState(true);
  if (!kind || !ok) return <div class="backdrop backdrop-empty" />;
  return (
    <div class="backdrop">
      <img src={imageUrl(it.id, kind, 1280)} alt="" onError={() => setOk(false)} />
    </div>
  );
}

function Playable({ item, reload }: { item: ItemDetail; reload: () => void }) {
  const [audio, setAudio] = useState<number | null>(item.defaultAudioTrackId ?? null);
  const [sub, setSub] = useState<string | null>(item.defaultSubtitleTrackId ?? null);
  const pos = item.userData.positionMs;
  const canPlay = item.status === 'ok';
  const togglePlayed = async () => {
    await api('POST', '/items/' + item.id + '/played', { played: !item.userData.played });
    reload();
  };
  return (
    <div class="page detail">
      <Backdrop it={item} />
      <div class="detail-body">
        {item.type === 'episode' && item.seriesId ? (
          <a class="eyebrow" href={href('/item/' + item.seriesId + qs({ season: item.seasonNumber }))}>
            {item.seriesTitle} · {episodeLabel(item)}
          </a>
        ) : null}
        <h1>{item.title}</h1>
        <div class="meta muted">
          {[item.year, formatRuntime(item.runtimeMs), item.container ? item.container.toUpperCase() : '', item.bitrate ? (item.bitrate / 1000).toFixed(1) + ' Mbps' : '']
            .filter(Boolean)
            .join(' · ')}
        </div>
        <Badges it={item} />
        {pos > 0 && item.runtimeMs ? <ProgressBar value={pos / item.runtimeMs} /> : null}
        {!canPlay ? <p class="warn-box">{item.status === 'pending' ? t('notReady') : t('missingFile')} {item.statusReason ? '(' + item.statusReason + ')' : ''}</p> : null}
        <div class="actions" data-nav-section>
          {pos > 0 ? (
            <a class="btn btn-primary" data-autofocus href={href(playHref(item.id, { resume: true, audio, sub }))} aria-disabled={!canPlay}>
              ▶ {t('resume', { time: formatTime(pos) })}
            </a>
          ) : null}
          <a class={'btn' + (pos > 0 ? '' : ' btn-primary')} data-autofocus={pos > 0 ? undefined : ''} href={href(playHref(item.id, { audio, sub }))} aria-disabled={!canPlay}>
            ▶ {pos > 0 ? t('playFromStart') : t('play')}
          </a>
          <button class="btn" onClick={togglePlayed}>
            {item.userData.played ? t('markUnplayed') : t('markPlayed')}
          </button>
        </div>
        {item.audioTracks && item.audioTracks.length > 0 ? (
          <div class="track-pickers" data-nav-section>
            <label class="field inline">
              <span>{t('audio')}</span>
              <select value={audio === null ? '' : String(audio)} onChange={(e) => setAudio(parseInt((e.target as HTMLSelectElement).value, 10))}>
                {item.audioTracks.map((a) => (
                  <option key={a.id} value={String(a.id)}>
                    {a.displayName}
                  </option>
                ))}
              </select>
            </label>
            <label class="field inline">
              <span>{t('subtitles')}</span>
              <select value={sub || ''} onChange={(e) => setSub((e.target as HTMLSelectElement).value || null)}>
                <option value="">{t('subtitlesOff')}</option>
                {(item.subtitleTracks || []).map((s) => (
                  <option key={s.id} value={s.id} disabled={!s.isText}>
                    {s.displayName}
                  </option>
                ))}
              </select>
            </label>
          </div>
        ) : null}
        {item.path ? <p class="path muted">{item.path}</p> : null}
      </div>
    </div>
  );
}

function Series({ item, route, reload }: { item: ItemDetail; route: Route; reload: () => void }) {
  const [seasons, setSeasons] = useState<ItemSummary[] | null>(null);
  const [episodes, setEpisodes] = useState<ItemSummary[] | null>(null);
  const seasonNum = route.query.season ? parseInt(route.query.season, 10) : null;

  useEffect(() => {
    api<{ items: ItemSummary[] }>('GET', '/items/' + item.id + '/children').then((r) => setSeasons(r.items));
  }, [item.id]);
  const current = seasons ? seasons.find((s) => s.seasonNumber === seasonNum) || seasons.find((s) => item.nextUp && s.seasonNumber === item.nextUp.seasonNumber) || seasons[0] : null;
  useEffect(() => {
    if (!current) return;
    setEpisodes(null);
    api<{ items: ItemSummary[] }>('GET', '/items/' + current.id + '/children').then((r) => setEpisodes(r.items));
  }, [current && current.id]);

  const togglePlayed = async () => {
    await api('POST', '/items/' + item.id + '/played', { played: !!item.unplayedCount });
    reload();
  };
  const nu = item.nextUp;
  return (
    <div class="page detail">
      <Backdrop it={item} />
      <div class="detail-body">
        <h1>{item.title}</h1>
        <div class="meta muted">
          {[item.year, item.childCount ? episodesLabel(item.childCount) : '', item.unplayedCount ? t('unplayed', { n: item.unplayedCount }) : ''].filter(Boolean).join(' · ')}
        </div>
        <div class="actions" data-nav-section>
          {nu ? (
            <a class="btn btn-primary" data-autofocus href={href(playHref(nu.id, { resume: nu.userData.positionMs > 0 }))}>
              ▶ {t('playNext', { label: episodeLabel(nu) })}
            </a>
          ) : null}
          <button class="btn" onClick={togglePlayed}>
            {item.unplayedCount ? t('markPlayed') : t('markUnplayed')}
          </button>
        </div>
        {seasons && seasons.length > 1 ? (
          <div class="tabs" data-nav-section>
            {seasons.map((s) => (
              <button
                key={s.id}
                class={'tab' + (current && s.id === current.id ? ' active' : '')}
                onClick={() => navigate('/item/' + item.id + qs({ season: s.seasonNumber }), true)}
              >
                {s.seasonNumber === 0 ? t('specials') : t('season', { n: s.seasonNumber || 0 })}
              </button>
            ))}
          </div>
        ) : null}
        {!episodes ? (
          <Spinner />
        ) : (
          <div class="episodes" data-nav-section>
            {episodes.map((ep) => (
              <a key={ep.id} class={'episode' + (nu && nu.id === ep.id ? ' next' : '')} href={href('/item/' + ep.id)} data-focusable>
                <div class="art art-thumb episode-art">
                  {ep.images.thumb ? <img src={imageUrl(ep.id, 'thumb', 400)} alt="" loading="lazy" /> : <div class="text-art"><span>{episodeLabel(ep)}</span></div>}
                  <ProgressBar value={ep.runtimeMs ? ep.userData.positionMs / ep.runtimeMs : 0} />
                  {ep.userData.played ? <span class="corner-check">✓</span> : null}
                </div>
                <div class="episode-text">
                  <div class="episode-title">
                    <span class="muted">{episodeLabel(ep)}</span> {ep.title}
                  </div>
                  <div class="muted small">
                    {formatRuntime(ep.runtimeMs)} {nu && nu.id === ep.id ? '· ' + t('nextUp') : ''}
                  </div>
                  <Badges it={ep} />
                </div>
              </a>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

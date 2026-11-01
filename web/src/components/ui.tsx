import { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { episodeLabel, imageUrl, ItemSummary } from '../api';
import { episodesLabel, t } from '../i18n/en';
import { focusFirst, pushBackHandler } from '../nav/spatial';
import { href } from '../router';

export function Spinner({ label }: { label?: string }) {
  return (
    <div class="spinner-wrap" role="status">
      <div class="spinner" />
      {label ? <span class="muted">{label}</span> : null}
    </div>
  );
}

export function ErrorBox({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div class="error-box" role="alert">
      <p>{message}</p>
      {onRetry ? (
        <button class="btn" onClick={onRetry}>
          {t('retry')}
        </button>
      ) : null}
    </div>
  );
}

export function ProgressBar({ value }: { value: number }) {
  if (!(value > 0)) return null;
  return (
    <div class="progress" aria-hidden="true">
      <div class="progress-fill" style={{ width: Math.min(100, Math.max(2, value * 100)) + '%' }} />
    </div>
  );
}

export function Badges({ it }: { it: ItemSummary }) {
  const b: string[] = [];
  if (it.video) {
    if (it.video.height >= 2000 || it.video.width >= 3500) b.push('4K');
    else if (it.video.height >= 1000) b.push('1080p');
    else if (it.video.height >= 700) b.push('720p');
    if (it.video.hdr === 'dolbyvision') b.push('DV');
    else if (it.video.hdr === 'hdr10') b.push('HDR10');
    else if (it.video.hdr === 'hlg') b.push('HLG');
    b.push(it.video.codec.toUpperCase());
  }
  for (const l of it.audioLanguages || []) b.push(l.toUpperCase());
  if (it.status === 'pending') b.push(t('processing'));
  if (it.status === 'missing') b.push(t('missingFile'));
  return (
    <div class="badges">
      {b.map((x) => (
        <span class={'badge' + (x === t('processing') ? ' badge-warn' : '')} key={x}>
          {x}
        </span>
      ))}
    </div>
  );
}

function ImgOrText({ src, title, sub, className, overlay }: { src: string | null; title: string; sub?: string; className: string; overlay?: boolean }) {
  const [failed, setFailed] = useState(!src);
  useEffect(() => setFailed(!src), [src]);
  return (
    <div class={className}>
      {!failed && src ? (
        <>
          <img src={src} alt="" loading="lazy" onError={() => setFailed(true)} />
          {overlay ? (
            <div class="art-overlay">
              <span>{title}</span>
              {sub ? <small>{sub}</small> : null}
            </div>
          ) : null}
        </>
      ) : (
        <div class="text-art">
          <span>{title}</span>
          {sub ? <small>{sub}</small> : null}
        </div>
      )}
    </div>
  );
}

function progressOf(it: ItemSummary): number {
  if (!it.runtimeMs || !it.userData) return 0;
  return it.userData.positionMs / it.runtimeMs;
}

/** Poster card (2:3) for movies and series. */
export function PosterCard({ it }: { it: ItemSummary }) {
  const target = it.type === 'season' && it.seriesId ? '/item/' + it.seriesId + '?season=' + it.seasonNumber : '/item/' + it.id;
  const unplayed = it.type === 'series' && it.unplayedCount ? it.unplayedCount : 0;
  // No poster (typical without online metadata): use a video frame, cropped.
  const frame = it.images.thumb ? 'thumb' : it.images.backdrop ? 'backdrop' : null;
  return (
    <a class="card card-poster" href={href(target)} data-focusable title={it.title}>
      <div class="art art-poster">
        <ImgOrText
          className="art-inner"
          src={it.images.poster ? imageUrl(it.id, 'poster', 300) : frame ? imageUrl(it.id, frame, 640) : null}
          overlay={!it.images.poster}
          title={it.title}
          sub={it.year ? String(it.year) : ''}
        />
        {unplayed ? <span class="corner-count">{unplayed}</span> : null}
        {it.userData && it.userData.played && it.type !== 'series' ? <span class="corner-check">✓</span> : null}
        <ProgressBar value={progressOf(it)} />
      </div>
      <div class="card-title">{it.title}</div>
      <div class="card-sub muted">{it.year || (it.type === 'series' && it.childCount ? episodesLabel(it.childCount) : '')}</div>
    </a>
  );
}

/** Wide (16:9) card for episodes, continue watching and next up. */
export function ThumbCard({ it, to }: { it: ItemSummary; to?: string }) {
  const isEp = it.type === 'episode';
  const title = isEp ? it.seriesTitle || it.title : it.title;
  const sub = isEp ? episodeLabel(it) + ' · ' + it.title : it.year ? String(it.year) : '';
  const kind = it.images.thumb ? 'thumb' : it.images.backdrop ? 'backdrop' : null;
  return (
    <a class="card card-thumb" href={href(to || '/item/' + it.id)} data-focusable title={title}>
      <div class="art art-thumb">
        <ImgOrText className="art-inner" src={kind ? imageUrl(it.id, kind as any, 480) : null} title={title} sub={sub} />
        <ProgressBar value={progressOf(it)} />
        {it.userData && it.userData.played ? <span class="corner-check">✓</span> : null}
      </div>
      <div class="card-title">{title}</div>
      <div class="card-sub muted">{sub}</div>
    </a>
  );
}

/** Horizontal scrolling row with a heading. */
export function Row({ title, children, more }: { title: string; children: ComponentChildren; more?: string }) {
  return (
    <section class="row" data-nav-section>
      <div class="row-head">
        <h2>{title}</h2>
        {more ? (
          <a class="row-more" href={href(more)}>
            {t('seeAll')}
          </a>
        ) : null}
      </div>
      <div class="row-scroll">{children}</div>
    </section>
  );
}

/** Modal dialog / menu with focus trap and Back-to-close. */
export function Modal({ title, onClose, children }: { title?: string; onClose: () => void; children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    const pop = pushBackHandler(() => {
      onClose();
      return true;
    });
    setTimeout(() => ref.current && focusFirst(ref.current), 30);
    return () => {
      pop();
      if (prev && document.body.contains(prev)) prev.focus();
    };
  }, []);
  return (
    <div class="modal-backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal" role="dialog" aria-modal="true" data-focus-trap ref={ref}>
        {title ? <h3 class="modal-title">{title}</h3> : null}
        {children}
      </div>
    </div>
  );
}

export interface MenuOption {
  id: string;
  label: string;
  selected?: boolean;
  note?: string;
}

export function Menu({ title, options, onSelect, onClose }: { title: string; options: MenuOption[]; onSelect: (id: string) => void; onClose: () => void }) {
  return (
    <Modal title={title} onClose={onClose}>
      <div class="menu-list">
        {options.map((o) => (
          <button
            key={o.id}
            class={'menu-item' + (o.selected ? ' selected' : '')}
            data-autofocus={o.selected ? '' : undefined}
            onClick={() => {
              onSelect(o.id);
              onClose();
            }}
          >
            <span class="menu-check">{o.selected ? '●' : ''}</span>
            <span>{o.label}</span>
            {o.note ? <small class="muted"> {o.note}</small> : null}
          </button>
        ))}
      </div>
    </Modal>
  );
}

let toastSetter: ((m: string | null) => void) | null = null;
export function toast(msg: string) {
  if (toastSetter) toastSetter(msg);
}
export function ToastHost() {
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => {
    toastSetter = setMsg;
    return () => {
      toastSetter = null;
    };
  }, []);
  useEffect(() => {
    if (!msg) return;
    const id = setTimeout(() => setMsg(null), 4000);
    return () => clearTimeout(id);
  }, [msg]);
  return msg ? (
    <div class="toast" role="status">
      {msg}
    </div>
  ) : null;
}

/** Load data with loading/error state. */
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): { data: T | null; error: string | null; loading: boolean; reload: () => void } {
  const [state, setState] = useState<{ data: T | null; error: string | null; loading: boolean }>({ data: null, error: null, loading: true });
  const [n, setN] = useState(0);
  useEffect(() => {
    let alive = true;
    setState((s) => ({ ...s, loading: true, error: null }));
    fn().then(
      (data) => alive && setState({ data, error: null, loading: false }),
      (e) => alive && setState({ data: null, error: (e && e.message) || t('error'), loading: false })
    );
    return () => {
      alive = false;
    };
  }, deps.concat([n]));
  return { ...state, reload: () => setN((x) => x + 1) };
}

import { useEffect, useRef, useState } from 'preact/hooks';
import { api } from '../api';
import { toast } from '../components/ui';
import { t } from '../i18n/en';

export interface UpdateStatus {
  current: string;
  devBuild: boolean;
  latest: null | { version: string; name: string; notes: string; url: string; publishedAt: string };
  available: boolean;
  checkedAt: string | null;
  checkError?: string;
  canInstall: boolean;
  cannotInstallReason?: string;
  state: 'idle' | 'downloading' | 'installing' | 'restarting' | 'restart_required' | 'failed';
  error?: string;
  downloadedBytes: number;
  totalBytes: number;
  lastResult?: { from: string; to: string; ok: boolean; message: string; at: string };
  activePlaySessions: number;
}

const mb = (n: number) => (n / 1e6).toFixed(1);
const RESTART_TIMEOUT_MS = 180000;

function ago(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return t('justNow');
  if (s < 5400) return t('minutesAgo', { n: Math.round(s / 60) });
  if (s < 129600) return t('hoursAgo', { n: Math.round(s / 3600) });
  return t('daysAgo', { n: Math.round(s / 86400) });
}

/** Admin → Updates: shows the running and latest release and installs it. */
export function UpdatesCard() {
  const [st, setSt] = useState<UpdateStatus | null>(null);
  const [checking, setChecking] = useState(false);
  const [waitingSince, setWaitingSince] = useState<number | null>(null);
  const [slow, setSlow] = useState(false);
  const fromRef = useRef('');

  const load = () =>
    api<UpdateStatus>('GET', '/admin/update').then(
      (s) => setSt(s),
      () => undefined
    );

  useEffect(() => {
    load();
  }, []);

  // Poll while downloading/installing.
  const busy = !!st && (st.state === 'downloading' || st.state === 'installing');
  useEffect(() => {
    if (!busy) return;
    const id = setInterval(load, 1000);
    return () => clearInterval(id);
  }, [busy]);

  // While restarting, the server goes away; reload once a new version answers.
  useEffect(() => {
    if (!st || st.state !== 'restarting' || waitingSince !== null) return;
    fromRef.current = st.current;
    setWaitingSince(Date.now());
  }, [st && st.state]);
  useEffect(() => {
    if (waitingSince === null) return;
    const id = setInterval(async () => {
      try {
        const info = await api<{ version: string }>('GET', '/system/info');
        if (info.version !== fromRef.current) {
          location.reload();
          return;
        }
        // Same version answering again: the restart failed or rolled back.
        const s = await api<UpdateStatus>('GET', '/admin/update');
        if (s.state !== 'restarting') {
          setWaitingSince(null);
          setSt(s);
          return;
        }
      } catch (_) {
        /* server is restarting */
      }
      if (Date.now() - waitingSince > RESTART_TIMEOUT_MS) setSlow(true);
    }, 2000);
    return () => clearInterval(id);
  }, [waitingSince]);

  const check = async () => {
    setChecking(true);
    try {
      setSt(await api<UpdateStatus>('POST', '/admin/update/check', {}));
    } catch (e: any) {
      toast(e.message);
    }
    setChecking(false);
  };

  const install = async () => {
    if (!st || !st.latest) return;
    if (st.activePlaySessions > 0 && !confirm(t('updateConfirmStreams', { n: st.activePlaySessions, version: st.latest.version }))) return;
    try {
      setSt(await api<UpdateStatus>('POST', '/admin/update/install', {}));
    } catch (e: any) {
      toast(e.message);
      load();
    }
  };

  if (!st) return null;
  const latest = st.latest;
  const recentResult = st.lastResult && Date.now() - new Date(st.lastResult.at).getTime() < 3 * 86400000 ? st.lastResult : null;
  const restarting = st.state === 'restarting' || waitingSince !== null;

  return (
    <section class="card-section" data-testid="updates">
      <div class="section-head">
        <h2>{t('updates')}</h2>
        <button class="btn btn-small" onClick={check} disabled={checking || busy || restarting} data-testid="update-check">
          {checking ? t('checking') : t('checkNow')}
        </button>
      </div>

      {recentResult && !restarting ? <p class={recentResult.ok ? 'ok-box' : 'warn-box'}>{recentResult.message}</p> : null}

      <dl class="kv">
        <dt>{t('running')}</dt>
        <dd>
          {st.current} {st.devBuild ? <span class="muted">· {t('devBuild')}</span> : null}
        </dd>
        <dt>{t('latestRelease')}</dt>
        <dd data-testid="update-latest">
          {latest ? (
            <span>
              {latest.version} <span class="muted">· {ago(latest.publishedAt)}</span>
            </span>
          ) : st.checkedAt ? (
            t('noReleases')
          ) : (
            '—'
          )}
        </dd>
        <dt>{t('lastChecked')}</dt>
        <dd>{st.checkedAt ? ago(st.checkedAt) : t('notYet')}</dd>
      </dl>
      {st.checkError ? <p class="form-error small">{st.checkError}</p> : null}

      {latest && st.available && latest.notes ? (
        <details class="update-notes" open={!restarting}>
          <summary>{t('whatsNew', { version: latest.version })}</summary>
          <pre class="notes small">{latest.notes}</pre>
          <a class="small" href={latest.url} target="_blank" rel="noopener">
            {t('onGitHub')}
          </a>
        </details>
      ) : null}

      {st.state === 'downloading' ? (
        <p class="small" data-testid="update-progress">
          {st.totalBytes ? t('downloadingMb', { done: mb(st.downloadedBytes), total: mb(st.totalBytes) }) : t('downloading')}
        </p>
      ) : null}
      {st.state === 'installing' ? <p class="small">{t('installing')}</p> : null}
      {restarting ? <p class="ok-box">{slow ? t('restartSlow') : t('restartingServer')}</p> : null}
      {st.state === 'restart_required' ? <p class="warn-box">{t('restartRequired', { version: latest ? latest.version : '' })}</p> : null}
      {st.state === 'failed' && st.error ? <p class="warn-box">{st.error}</p> : null}

      {latest && st.available && !busy && !restarting && st.state !== 'restart_required' ? (
        st.canInstall ? (
          <div class="actions">
            <button class="btn btn-primary" onClick={install} data-testid="update-install">
              {t('installVersion', { version: latest.version })}
            </button>
            <span class="muted small">{t('installHint')}</span>
          </div>
        ) : (
          <p class="warn-box small">{st.cannotInstallReason}</p>
        )
      ) : null}
      {latest && !st.available && !st.checkError ? <p class="muted small">{t('upToDate')}</p> : null}
    </section>
  );
}

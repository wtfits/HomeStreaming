import { useEffect, useState } from 'preact/hooks';
import { api, ApiError, ItemSummary, Library, Task } from '../api';
import { ErrorBox, Spinner, toast, useAsync } from '../components/ui';
import { t } from '../i18n/en';
import { FolderPicker } from './FolderPicker';

interface Status {
  version: string;
  uptimeSeconds: number;
  dataDir: string;
  transcodeDir: string;
  transcodeDirFreeBytes: number;
  ffmpeg: { found: boolean; path?: string; version?: string; hint?: string; warning?: string; encoders?: string[] };
  hwaccel: { configured: string; active: string; hwDecode: Record<string, boolean>; selfTests: { backend: string; test: string; ok: boolean; detail: string }[] };
  lanAddresses: string[];
  tailnetAddresses: string[];
  connection: { class: string; ip: string };
}

interface AdminSession {
  id: string;
  deviceName: string;
  ip: string;
  connectionClass: string;
  method: string;
  reasons: string[];
  backend: string;
  positionMs: number;
  state: string;
  restarts: number;
  item?: ItemSummary;
  transcode: null | { video: { codec: string; height: number; maxrateKbps: number } };
}

function gb(n: number) {
  return (n / 1e9).toFixed(1) + ' GB';
}

export function Admin() {
  const status = useAsync(() => api<Status>('GET', '/admin/status'), []);
  const libs = useAsync(() => api<{ items: Library[] }>('GET', '/libraries'), []);
  const sessions = useAsync(() => api<{ items: AdminSession[] }>('GET', '/admin/play-sessions'), []);
  const tasks = useAsync(() => api<{ items: Task[] }>('GET', '/tasks'), []);
  const problems = useAsync(() => api<{ items: { item: ItemSummary; status: string; statusReason: string; path: string }[]; counts: Record<string, number> }>('GET', '/admin/problems'), []);
  const [adding, setAdding] = useState(false);

  // Live refresh while scans or sessions are active.
  useEffect(() => {
    const id = setInterval(() => {
      sessions.reload();
      tasks.reload();
      libs.reload();
    }, 4000);
    return () => clearInterval(id);
  }, []);

  if (status.error) return <ErrorBox message={status.error} onRetry={status.reload} />;
  if (!status.data) return <Spinner />;
  const st = status.data;
  return (
    <div class="page narrow">
      <h1>{t('adminTitle')}</h1>

      <section class="card-section">
        <h2>{t('status')}</h2>
        {!st.ffmpeg.found ? <FfmpegMissing onDone={status.reload} hint={st.ffmpeg.hint || ''} /> : null}
        <dl class="kv">
          <dt>Version</dt>
          <dd>{st.version}</dd>
          <dt>ffmpeg</dt>
          <dd>{st.ffmpeg.found ? `${st.ffmpeg.version} — ${st.ffmpeg.path}` : '—'}</dd>
          <dt>{t('hwaccel')}</dt>
          <dd>
            {st.hwaccel.active === 'none' ? 'software (libx264)' : st.hwaccel.active} <span class="muted">(configured: {st.hwaccel.configured})</span>
            {Object.keys(st.hwaccel.hwDecode || {}).length ? <span class="muted"> · GPU decode: {Object.keys(st.hwaccel.hwDecode).filter((k) => st.hwaccel.hwDecode[k]).join(', ') || 'none'}</span> : null}
          </dd>
          <dt>LAN</dt>
          <dd>{st.lanAddresses.map((a) => 'http://' + a + ':8420').join('  ') || '—'}</dd>
          <dt>Tailscale</dt>
          <dd>{st.tailnetAddresses.map((a) => 'http://' + a + ':8420').join('  ') || '—'}</dd>
          <dt>You</dt>
          <dd>
            {st.connection.ip} ({st.connection.class})
          </dd>
          <dt>Data</dt>
          <dd>{st.dataDir}</dd>
          <dt>Transcode</dt>
          <dd>
            {st.transcodeDir} · {gb(st.transcodeDirFreeBytes)} free
          </dd>
        </dl>
        {st.ffmpeg.warning ? <p class="warn-box">{st.ffmpeg.warning}</p> : null}
        {st.hwaccel.selfTests.length ? (
          <details>
            <summary>{t('selfTests')}</summary>
            <ul class="small">
              {st.hwaccel.selfTests.map((s, i) => (
                <li key={i}>
                  {s.ok ? '✓' : '✗'} {s.backend} {s.test} <span class="muted">{s.detail}</span>
                </li>
              ))}
            </ul>
          </details>
        ) : null}
      </section>

      <section class="card-section">
        <h2>{t('activeSessions')}</h2>
        {!sessions.data || !sessions.data.items.length ? <p class="muted">{t('noSessions')}</p> : null}
        <div class="list">
          {(sessions.data ? sessions.data.items : []).map((s) => (
            <div class="list-row" key={s.id}>
              <div>
                <div>
                  <strong>{s.item ? (s.item.seriesTitle ? s.item.seriesTitle + ' · ' : '') + s.item.title : ''}</strong>
                </div>
                <div class="muted small">
                  {s.deviceName} · {s.ip} ({s.connectionClass}) · {s.method}
                  {s.transcode ? ` · ${s.backend} ${s.transcode.video.codec} ${s.transcode.video.height}p` : ''} {s.reasons.length ? '· ' + s.reasons.join(', ') : ''}
                  {s.restarts ? ` · ${s.restarts} seeks` : ''}
                </div>
              </div>
              <button class="btn btn-small" onClick={() => api('DELETE', '/admin/play-sessions/' + s.id).then(sessions.reload)}>
                ■
              </button>
            </div>
          ))}
        </div>
      </section>

      <section class="card-section">
        <div class="section-head">
          <h2>{t('libraries')}</h2>
          <button class="btn btn-small" onClick={() => setAdding(true)}>
            + {t('addLibrary')}
          </button>
        </div>
        {adding ? <AddLibrary onDone={() => (setAdding(false), libs.reload())} /> : null}
        {(libs.data ? libs.data.items : []).map((l) => (
          <LibraryRow key={l.id} lib={l} task={(tasks.data ? tasks.data.items : []).find((x) => x.libraryId === l.id && x.status === 'running')} reload={libs.reload} />
        ))}
      </section>

      <SettingsForm />

      <section class="card-section">
        <h2>
          {t('problems')} {problems.data ? <span class="muted small">({Object.keys(problems.data.counts).map((k) => k + ': ' + problems.data!.counts[k]).join(', ')})</span> : null}
        </h2>
        {problems.data && !problems.data.items.length ? <p class="muted">{t('noProblems')}</p> : null}
        <div class="list">
          {(problems.data ? problems.data.items : []).map((p) => (
            <div class="list-row" key={p.item.id}>
              <div>
                <div>
                  {p.item.title} <span class="badge">{p.status}</span>
                </div>
                <div class="muted small">{p.statusReason}</div>
                <div class="muted small path">{p.path}</div>
              </div>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

function FfmpegMissing({ hint, onDone }: { hint: string; onDone: () => void }) {
  const [path, setPath] = useState('');
  const save = async () => {
    const res = await api('POST', '/admin/ffmpeg', { path });
    if (!res.ffmpeg.found) toast('ffmpeg still not found');
    onDone();
  };
  return (
    <div class="warn-box">
      <p>
        <strong>ffmpeg not found.</strong> {hint}
      </p>
      <pre class="code">winget install Gyan.FFmpeg</pre>
      <div class="input-row">
        <input value={path} placeholder="C:\ffmpeg\bin\ffmpeg.exe (optional)" onInput={(e) => setPath((e.target as HTMLInputElement).value)} />
        <button class="btn" onClick={save}>
          {t('setupCheckAgain')}
        </button>
      </div>
    </div>
  );
}

function LibraryRow({ lib, task, reload }: { lib: Library; task?: Task; reload: () => void }) {
  const [editing, setEditing] = useState(false);
  const [paths, setPaths] = useState(lib.paths.join('\n'));
  const scan = async (mode: string) => {
    try {
      await api('POST', '/libraries/' + lib.id + '/scan', { mode });
    } catch (e: any) {
      toast(e.message);
    }
    reload();
  };
  const clean = async () => {
    const r = await api('POST', '/libraries/' + lib.id + '/clean', {});
    toast('Removed ' + r.deleted);
    reload();
  };
  const del = async () => {
    if (!confirm(t('confirmDelete', { name: lib.name }))) return;
    await api('DELETE', '/libraries/' + lib.id);
    reload();
  };
  const savePaths = async () => {
    try {
      await api('PATCH', '/libraries/' + lib.id, { paths: paths.split('\n').map((p) => p.trim()).filter(Boolean) });
      setEditing(false);
      reload();
    } catch (e: any) {
      toast(e instanceof ApiError && e.details.paths ? String(e.details.paths) : e.message);
    }
  };
  const c = lib.counts;
  return (
    <div class="lib-row">
      <div class="lib-head">
        <strong>{lib.name}</strong> <span class="muted small">{lib.type}</span>
      </div>
      <div class="muted small">
        {c.movies} movies · {c.series} shows · {c.episodes} episodes {c.pending ? `· ${c.pending} pending` : ''} {c.missing ? `· ${c.missing} missing` : ''}
      </div>
      {editing ? (
        <div>
          <textarea rows={3} value={paths} onInput={(e) => setPaths((e.target as HTMLTextAreaElement).value)} />
          <div class="actions">
            <button class="btn btn-small" onClick={() => setEditing(false)}>
              {t('cancel')}
            </button>
            <button class="btn btn-small btn-primary" onClick={savePaths}>
              {t('save')}
            </button>
          </div>
        </div>
      ) : (
        <div class="muted small path">{lib.paths.join(' · ')}</div>
      )}
      {task ? <div class="small">{t('scanning', { phase: task.phase, done: task.progress.done, total: task.progress.total })}</div> : null}
      {lib.lastScanError ? <div class="form-error small">{lib.lastScanError}</div> : null}
      <div class="actions">
        <button class="btn btn-small" onClick={() => scan('quick')} disabled={!!task}>
          {t('scan')}
        </button>
        <button class="btn btn-small" onClick={() => scan('full')} disabled={!!task}>
          {t('scanFull')}
        </button>
        <button class="btn btn-small" onClick={clean}>
          {t('clean')}
        </button>
        <button class="btn btn-small" onClick={() => setEditing(true)}>
          {t('edit')}
        </button>
        <button class="btn btn-small btn-danger" onClick={del}>
          {t('delete')}
        </button>
      </div>
    </div>
  );
}

function AddLibrary({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('');
  const [type, setType] = useState('mixed');
  const [path, setPath] = useState('');
  const [picker, setPicker] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const submit = async (e: Event) => {
    e.preventDefault();
    try {
      await api('POST', '/libraries', { name, type, paths: [path] });
      onDone();
    } catch (e: any) {
      setErr(e instanceof ApiError && e.details.paths ? String(e.details.paths) : e.message);
    }
  };
  return (
    <form class="add-lib" onSubmit={submit}>
      <label class="field">
        <span>{t('libraryName')}</span>
        <input required value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
      </label>
      <label class="field">
        <span>{t('libraryType')}</span>
        <select value={type} onChange={(e) => setType((e.target as HTMLSelectElement).value)}>
          <option value="mixed">{t('typeMixed')}</option>
          <option value="movies">{t('typeMovies')}</option>
          <option value="shows">{t('typeShows')}</option>
        </select>
      </label>
      <label class="field">
        <span>{t('folders')}</span>
        <div class="input-row">
          <input required value={path} onInput={(e) => setPath((e.target as HTMLInputElement).value)} />
          <button type="button" class="btn" onClick={() => setPicker(true)}>
            {t('browse')}
          </button>
        </div>
      </label>
      {err ? <p class="form-error">{err}</p> : null}
      <div class="actions">
        <button type="button" class="btn" onClick={onDone}>
          {t('cancel')}
        </button>
        <button type="submit" class="btn btn-primary">
          {t('addLibrary')}
        </button>
      </div>
      {picker ? (
        <FolderPicker
          initial={path}
          onPick={(p) => {
            setPath(p);
            setPicker(false);
          }}
          onClose={() => setPicker(false)}
        />
      ) : null}
    </form>
  );
}

function SettingsForm() {
  const s = useAsync(() => api<Record<string, any>>('GET', '/admin/settings'), []);
  const [draft, setDraft] = useState<Record<string, any> | null>(null);
  useEffect(() => {
    if (s.data) setDraft(s.data);
  }, [s.data]);
  if (!draft) return null;
  const num = (k: string) => (e: Event) => setDraft({ ...draft, [k]: parseInt((e.target as HTMLInputElement).value || '0', 10) });
  const save = async (e: Event) => {
    e.preventDefault();
    try {
      const { readOnly: _ro, ...body } = draft;
      const res = await api('PATCH', '/admin/settings', body);
      setDraft(res);
      toast(t('saved'));
    } catch (err: any) {
      toast(err instanceof ApiError && Object.keys(err.details).length ? JSON.stringify(err.details) : err.message);
    }
  };
  return (
    <form class="card-section" onSubmit={save}>
      <h2>{t('serverSettings')}</h2>
      <div class="form-grid">
        <label class="field">
          <span>{t('serverName')}</span>
          <input value={draft.serverName} onInput={(e) => setDraft({ ...draft, serverName: (e.target as HTMLInputElement).value })} />
        </label>
        <label class="field">
          <span>{t('remoteCap')}</span>
          <input type="number" min={0} value={draft.remoteMaxBitrateKbps} onInput={num('remoteMaxBitrateKbps')} />
        </label>
        <label class="field">
          <span>{t('lanCap')}</span>
          <input type="number" min={0} value={draft.lanMaxBitrateKbps} onInput={num('lanMaxBitrateKbps')} />
        </label>
        <label class="field">
          <span>{t('maxSessions')}</span>
          <input type="number" min={1} max={16} value={draft.transcodeMaxSessions} onInput={num('transcodeMaxSessions')} />
        </label>
        <label class="field">
          <span>{t('preset')}</span>
          <select value={draft.transcodePreset} onChange={(e) => setDraft({ ...draft, transcodePreset: (e.target as HTMLSelectElement).value })}>
            {['ultrafast', 'superfast', 'veryfast', 'faster', 'fast', 'medium'].map((p) => (
              <option key={p}>{p}</option>
            ))}
          </select>
        </label>
        <label class="field">
          <span>{t('scanInterval')}</span>
          <input type="number" min={0} value={draft.scanIntervalHours} onInput={num('scanIntervalHours')} />
        </label>
        <label class="field">
          <span>{t('playedThreshold')}</span>
          <input type="number" min={50} max={100} value={draft.playedThresholdPercent} onInput={num('playedThresholdPercent')} />
        </label>
        <label class="field">
          <span>{t('audioCodec')}</span>
          <select value={draft.audioTranscodeCodec} onChange={(e) => setDraft({ ...draft, audioTranscodeCodec: (e.target as HTMLSelectElement).value })}>
            <option value="auto">auto (AC-3 5.1 when possible)</option>
            <option value="aac">AAC stereo</option>
          </select>
        </label>
      </div>
      <label class="check">
        <input type="checkbox" checked={draft.tailnetIsLan} onChange={(e) => setDraft({ ...draft, tailnetIsLan: (e.target as HTMLInputElement).checked })} />
        <span>{t('tailnetIsLan')}</span>
      </label>
      <div class="actions">
        <button type="submit" class="btn btn-primary">
          {t('save')}
        </button>
      </div>
    </form>
  );
}

import { useEffect, useState } from 'preact/hooks';
import { api, ApiError, deviceId, deviceName, Library, setToken, SystemInfo, Task } from '../api';
import { t } from '../i18n/en';
import { navigate } from '../router';
import { setState } from '../state';
import { FolderPicker } from './FolderPicker';

export function Setup({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState(1);
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [ffPath, setFfPath] = useState('');
  const [checking, setChecking] = useState(false);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [password2, setPassword2] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [libName, setLibName] = useState('Torrent');
  const [libType, setLibType] = useState<'mixed' | 'movies' | 'shows'>('mixed');
  const [libPath, setLibPath] = useState('');
  const [picker, setPicker] = useState(false);
  const [task, setTask] = useState<Task | null>(null);

  const loadInfo = async () => {
    setChecking(true);
    try {
      const i = await api<SystemInfo>('GET', '/system/info');
      setInfo(i);
      if (!i.setupRequired && step < 3) navigate('/login', true);
      if (i.suggestedLibraryPath && !libPath) setLibPath(i.suggestedLibraryPath);
    } finally {
      setChecking(false);
    }
  };
  useEffect(() => {
    loadInfo();
  }, []);

  const createAccount = async (e: Event) => {
    e.preventDefault();
    setErr(null);
    if (password.length < 8) return setErr(t('passwordShort'));
    if (password !== password2) return setErr(t('passwordMismatch'));
    setBusy(true);
    try {
      const res = await api('POST', '/system/setup', {
        username,
        password,
        deviceId: deviceId(),
        deviceName: deviceName(),
        clientType: 'web',
        ffmpegPath: !info?.ffmpeg.found && ffPath ? ffPath : undefined,
      });
      setToken(res.token);
      setState({ user: res.user, connection: res.connection });
      setStep(3);
    } catch (e: any) {
      const d = e instanceof ApiError ? e.details : {};
      setErr(Object.keys(d).length ? Object.keys(d).map((k) => k + ': ' + d[k]).join(', ') : e.message);
    } finally {
      setBusy(false);
    }
  };

  const addLibrary = async (e: Event) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const lib = await api<Library>('POST', '/libraries', { name: libName, type: libType, paths: [libPath] });
      setStep(4);
      const poll = async () => {
        const tk = await api<Task>('GET', '/tasks/' + lib.scanTaskId);
        setTask(tk);
        if (tk.status === 'running' || tk.status === 'queued') setTimeout(poll, 700);
      };
      poll();
    } catch (e: any) {
      const d = e instanceof ApiError ? e.details : {};
      setErr(d && d.paths ? String(d.paths) : e.message);
    } finally {
      setBusy(false);
    }
  };

  const finish = () => {
    onDone();
    navigate('/', true);
  };

  return (
    <div class="center-page">
      <div class="panel wide">
        <h1>{t('setupTitle')}</h1>
        <ol class="steps">
          {[1, 2, 3, 4].map((n) => (
            <li class={n === step ? 'active' : n < step ? 'done' : ''} key={n}>
              {n}
            </li>
          ))}
        </ol>

        {step === 1 && info ? (
          <section>
            <h2>{t('setupFfmpeg')}</h2>
            {info.ffmpeg.found ? (
              <p class="ok-box">✓ {t('setupFfmpegOk', { path: info.ffmpeg.path || '', version: info.ffmpeg.version || '' })}</p>
            ) : (
              <div class="warn-box">
                <p>{t('setupFfmpegMissing')}</p>
                <p>{t('setupFfmpegHint')}</p>
                <pre class="code">winget install Gyan.FFmpeg</pre>
                <label class="field">
                  <span>{t('setupFfmpegPath')}</span>
                  <input value={ffPath} onInput={(e) => setFfPath((e.target as HTMLInputElement).value)} placeholder="C:\ffmpeg\bin\ffmpeg.exe" />
                </label>
                <button class="btn" onClick={loadInfo} disabled={checking}>
                  {t('setupCheckAgain')}
                </button>
              </div>
            )}
            {info.ffmpeg.warning ? <p class="warn-box">{info.ffmpeg.warning}</p> : null}
            <div class="actions">
              <button class="btn btn-primary" data-autofocus onClick={() => setStep(2)}>
                {t('next')}
              </button>
            </div>
          </section>
        ) : null}

        {step === 2 ? (
          <form onSubmit={createAccount}>
            <h2>{t('setupAccount')}</h2>
            <label class="field">
              <span>{t('username')}</span>
              <input autoFocus required autocomplete="username" value={username} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
            </label>
            <label class="field">
              <span>{t('password')}</span>
              <input type="password" required autocomplete="new-password" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
            </label>
            <label class="field">
              <span>{t('passwordConfirm')}</span>
              <input type="password" required autocomplete="new-password" value={password2} onInput={(e) => setPassword2((e.target as HTMLInputElement).value)} />
            </label>
            {err ? <p class="form-error">{err}</p> : null}
            <div class="actions">
              <button type="button" class="btn" onClick={() => setStep(1)}>
                {t('back')}
              </button>
              <button type="submit" class="btn btn-primary" disabled={busy}>
                {t('createAccount')}
              </button>
            </div>
          </form>
        ) : null}

        {step === 3 ? (
          <form onSubmit={addLibrary}>
            <h2>{t('setupLibrary')}</h2>
            <label class="field">
              <span>{t('libraryName')}</span>
              <input required value={libName} onInput={(e) => setLibName((e.target as HTMLInputElement).value)} />
            </label>
            <label class="field">
              <span>{t('libraryType')}</span>
              <select value={libType} onChange={(e) => setLibType((e.target as HTMLSelectElement).value as any)}>
                <option value="mixed">{t('typeMixed')}</option>
                <option value="movies">{t('typeMovies')}</option>
                <option value="shows">{t('typeShows')}</option>
              </select>
            </label>
            <label class="field">
              <span>{t('folders')}</span>
              <div class="input-row">
                <input required value={libPath} placeholder="F:\torrent" onInput={(e) => setLibPath((e.target as HTMLInputElement).value)} />
                <button type="button" class="btn" onClick={() => setPicker(true)}>
                  {t('browse')}
                </button>
              </div>
            </label>
            {err ? <p class="form-error">{err}</p> : null}
            <div class="actions">
              <button type="button" class="btn" onClick={finish}>
                {t('setupSkipLibrary')}
              </button>
              <button type="submit" class="btn btn-primary" disabled={busy || !libPath}>
                {t('scan')}
              </button>
            </div>
            {picker ? (
              <FolderPicker
                initial={libPath}
                onPick={(p) => {
                  setLibPath(p);
                  setPicker(false);
                }}
                onClose={() => setPicker(false)}
              />
            ) : null}
          </form>
        ) : null}

        {step === 4 ? (
          <section>
            <h2>{t('setupScan')}</h2>
            {task && task.status !== 'running' ? (
              <p class="ok-box">
                ✓ {t('setupScanDone')} {task.report ? `(${task.report.added} files)` : ''}
              </p>
            ) : (
              <p class="muted">{task ? t('scanning', { phase: task.phase, done: task.progress.done, total: task.progress.total }) : t('loading')}</p>
            )}
            <div class="actions">
              <button class="btn btn-primary" data-autofocus onClick={finish}>
                {t('setupGoHome')}
              </button>
            </div>
          </section>
        ) : null}
      </div>
    </div>
  );
}

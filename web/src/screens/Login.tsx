import { useEffect, useState } from 'preact/hooks';
import { api, ApiError, deviceId, deviceName, setToken } from '../api';
import { t } from '../i18n/en';
import { navigate } from '../router';
import { setState, useAppState } from '../state';

export function Login() {
  const { info } = useAppState();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [lockedFor, setLockedFor] = useState(0);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (lockedFor <= 0) return;
    const id = setTimeout(() => setLockedFor((s) => s - 1), 1000);
    return () => clearTimeout(id);
  }, [lockedFor]);

  const submit = async (e: Event) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const res = await api('POST', '/auth/login', { username, password, deviceId: deviceId(), deviceName: deviceName(), clientType: 'web' });
      setToken(res.token);
      setState({ user: res.user, connection: res.connection });
      navigate('/', true);
    } catch (e: any) {
      if (e instanceof ApiError && e.code === 'locked') {
        setLockedFor(e.details.retryAfterSeconds || 60);
        setErr('locked');
      } else if (e instanceof ApiError && e.code === 'rate_limited') setErr(t('loginRateLimited'));
      else if (e instanceof ApiError && e.code === 'invalid_credentials') setErr(t('loginInvalid'));
      else setErr(e.message || t('error'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="center-page">
      <form class="panel" onSubmit={submit}>
        <div class="logo-big">▶</div>
        <h1>{info ? info.serverName : t('appName')}</h1>
        <h2 class="muted">{t('loginTitle')}</h2>
        <label class="field">
          <span>{t('username')}</span>
          <input autoFocus data-autofocus required autocomplete="username" value={username} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
        </label>
        <label class="field">
          <span>{t('password')}</span>
          <input type="password" required autocomplete="current-password" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
        </label>
        {err ? <p class="form-error">{err === 'locked' ? t('loginLocked', { seconds: lockedFor }) : err}</p> : null}
        <div class="actions">
          <button type="submit" class="btn btn-primary btn-block" disabled={busy || lockedFor > 0}>
            {t('loginButton')}
          </button>
        </div>
      </form>
    </div>
  );
}

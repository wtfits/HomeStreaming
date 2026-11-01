import { useState } from 'preact/hooks';
import { api, prefs, setToken, User } from '../api';
import { toast, useAsync } from '../components/ui';
import { applyModeClasses, isTV } from '../device';
import { t } from '../i18n/en';
import { resetProfileCache } from '../player/capabilities';
import { navigate } from '../router';
import { setState, useAppState } from '../state';

const LANGS = [
  ['', '—'],
  ['eng', 'English'],
  ['rus', 'Russian'],
  ['tur', 'Turkish'],
  ['ukr', 'Ukrainian'],
  ['ger', 'German'],
  ['fre', 'French'],
  ['spa', 'Spanish'],
  ['ita', 'Italian'],
  ['jpn', 'Japanese'],
  ['kor', 'Korean'],
];

interface DeviceSession {
  id: string;
  deviceName: string;
  ip: string;
  lastSeenAt: string;
  current: boolean;
}

export function Settings() {
  const { user } = useAppState();
  const [cur, setCur] = useState('');
  const [nw, setNw] = useState('');
  const [tv, setTv] = useState(isTV());
  const [maxBitrate, setMaxBitrate] = useState(prefs.get('maxBitrate') || '0');
  const [force, setForce] = useState(prefs.get('forceTranscode') === '1');
  const [engine, setEngine] = useState(prefs.get('engine') || '');
  const sessions = useAsync(() => api<{ items: DeviceSession[] }>('GET', '/auth/sessions'), []);
  if (!user) return null;

  const savePrefs = async (patch: Partial<User['preferences']>) => {
    const u = await api<User>('PATCH', '/auth/me/preferences', patch);
    setState({ user: u });
    toast(t('saved'));
  };
  const changePassword = async (e: Event) => {
    e.preventDefault();
    try {
      await api('POST', '/auth/me/password', { currentPassword: cur, newPassword: nw });
      setCur('');
      setNw('');
      toast(t('passwordChanged'));
      sessions.reload();
    } catch (err: any) {
      toast(err.message);
    }
  };
  const logout = async () => {
    try {
      await api('POST', '/auth/logout', {});
    } catch (_) {
      /* ignore */
    }
    setToken(null);
    setState({ user: null });
    navigate('/login', true);
  };

  return (
    <div class="page narrow">
      <h1>{t('settingsTitle')}</h1>

      <section class="card-section">
        <h2>{t('preferences')}</h2>
        <label class="field">
          <span>{t('audioLanguage')}</span>
          <select value={user.preferences.audioLanguage} onChange={(e) => savePrefs({ audioLanguage: (e.target as HTMLSelectElement).value })}>
            {LANGS.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <label class="field">
          <span>{t('subtitleMode')}</span>
          <select value={user.preferences.subtitleMode} onChange={(e) => savePrefs({ subtitleMode: (e.target as HTMLSelectElement).value as any })}>
            <option value="off">{t('subModeOff')}</option>
            <option value="preferred">{t('subModePreferred')}</option>
            <option value="always">{t('subModeAlways')}</option>
          </select>
        </label>
        <label class="field">
          <span>{t('subtitleLanguage')}</span>
          <select value={user.preferences.subtitleLanguage} onChange={(e) => savePrefs({ subtitleLanguage: (e.target as HTMLSelectElement).value })}>
            {LANGS.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
      </section>

      <section class="card-section">
        <h2>{t('playback')}</h2>
        <label class="field">
          <span>{t('maxBitrate')}</span>
          <select
            value={maxBitrate}
            onChange={(e) => {
              const v = (e.target as HTMLSelectElement).value;
              setMaxBitrate(v);
              prefs.set('maxBitrate', v === '0' ? null : v);
            }}
          >
            {['0', '20000', '10000', '8000', '4000', '2000', '1000'].map((v) => (
              <option key={v} value={v}>
                {v === '0' ? t('qualityAuto') : parseInt(v, 10) / 1000 + ' Mbps'}
              </option>
            ))}
          </select>
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={force}
            onChange={(e) => {
              const v = (e.target as HTMLInputElement).checked;
              setForce(v);
              prefs.set('forceTranscode', v ? '1' : null);
            }}
          />
          <span>{t('forceTranscode')}</span>
        </label>
        <label class="field">
          <span>{t('engine')}</span>
          <select
            value={engine}
            onChange={(e) => {
              const v = (e.target as HTMLSelectElement).value;
              setEngine(v);
              prefs.set('engine', v || null);
              resetProfileCache();
            }}
          >
            <option value="">{t('engineAuto')}</option>
            <option value="native">Native HLS</option>
            <option value="hlsjs">hls.js</option>
          </select>
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={tv}
            onChange={(e) => {
              const v = (e.target as HTMLInputElement).checked;
              setTv(v);
              prefs.set('tvMode', v ? '1' : '0');
              applyModeClasses();
            }}
          />
          <span>{t('tvMode')}</span>
        </label>
      </section>

      <section class="card-section">
        <h2>{t('devices')}</h2>
        <div class="list">
          {(sessions.data ? sessions.data.items : []).map((s) => (
            <div class="list-row" key={s.id}>
              <div>
                <div>
                  {s.deviceName} {s.current ? <span class="badge">{t('thisDevice')}</span> : null}
                </div>
                <div class="muted small">
                  {s.ip} · {new Date(s.lastSeenAt).toLocaleString()}
                </div>
              </div>
              {!s.current ? (
                <button class="btn btn-small" onClick={() => api('DELETE', '/auth/sessions/' + s.id).then(sessions.reload)}>
                  {t('revoke')}
                </button>
              ) : null}
            </div>
          ))}
        </div>
      </section>

      <section class="card-section">
        <h2>{t('account')}</h2>
        <p class="muted">{user.username}</p>
        <form onSubmit={changePassword}>
          <label class="field">
            <span>{t('currentPassword')}</span>
            <input type="password" autocomplete="current-password" value={cur} onInput={(e) => setCur((e.target as HTMLInputElement).value)} />
          </label>
          <label class="field">
            <span>{t('newPassword')}</span>
            <input type="password" autocomplete="new-password" value={nw} onInput={(e) => setNw((e.target as HTMLInputElement).value)} />
          </label>
          <div class="actions">
            <button class="btn" type="submit" disabled={nw.length < 8 || !cur}>
              {t('changePassword')}
            </button>
            <button class="btn btn-danger" type="button" onClick={logout}>
              {t('signOut')}
            </button>
          </div>
        </form>
      </section>
    </div>
  );
}

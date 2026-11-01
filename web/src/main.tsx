import { render } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import { api, ApiError, Connection, setToken, setUnauthorizedHandler, SystemInfo, User } from './api';
import { ErrorBox, Spinner, ToastHost } from './components/ui';
import { applyModeClasses, isTV } from './device';
import { t } from './i18n/en';
import { focusFirst, installSpatialNav } from './nav/spatial';
import { Player } from './player/Player';
import { href, navigate, useRoute } from './router';
import { Admin } from './screens/Admin';
import { Home } from './screens/Home';
import { Item } from './screens/Item';
import { Library, Search } from './screens/Library';
import { Login } from './screens/Login';
import { Settings } from './screens/Settings';
import { Setup } from './screens/Setup';
import { setState, useAppState } from './state';
import './styles/app.css';

function NavBar() {
  const { user, info } = useAppState();
  const route = useRoute();
  const [libs, setLibs] = useState<{ id: string; name: string }[]>([]);
  useEffect(() => {
    if (user) api<{ items: { id: string; name: string }[] }>('GET', '/libraries').then((r) => setLibs(r.items), () => undefined);
  }, [user && user.id]);
  const [updateReady, setUpdateReady] = useState(false);
  useEffect(() => {
    if (user && user.isAdmin) api<{ available: boolean }>('GET', '/admin/update').then((r) => setUpdateReady(r.available), () => undefined);
  }, [user && user.id, route.path === '/admin']);
  const active = (p: string) => (route.path === p || (p !== '/' && route.path.indexOf(p) === 0) ? ' active' : '');
  return (
    <header class="nav" data-nav-section>
      <a class="brand" href={href('/')} aria-label={t('navHome')}>
        <span class="brand-mark">▶</span>
        <span class="brand-name">{info ? info.serverName : t('appName')}</span>
      </a>
      <nav class="nav-links">
        <a class={'nav-link' + active('/')} href={href('/')}>
          {t('navHome')}
        </a>
        {libs.map((l) => (
          <a key={l.id} class={'nav-link' + active('/library/' + l.id)} href={href('/library/' + l.id)}>
            {l.name}
          </a>
        ))}
        <a class={'nav-link' + active('/search')} href={href('/search')}>
          {t('navSearch')}
        </a>
      </nav>
      <div class="nav-right">
        {user && user.isAdmin ? (
          <a class={'nav-link' + active('/admin')} href={href('/admin')}>
            {t('navAdmin')}
            {updateReady ? <span class="nav-dot" title={t('updateAvailable')} aria-label={t('updateAvailable')} data-testid="update-dot" /> : null}
          </a>
        ) : null}
        <a class={'nav-link' + active('/settings')} href={href('/settings')}>
          {t('navSettings')}
        </a>
      </div>
    </header>
  );
}

function App() {
  const route = useRoute();
  const { user, info } = useAppState();
  const [boot, setBoot] = useState<'loading' | 'ready' | 'error'>('loading');
  const [bootErr, setBootErr] = useState('');

  const bootstrap = async () => {
    setBoot('loading');
    try {
      const i = await api<SystemInfo>('GET', '/system/info');
      setState({ info: i });
      if (i.setupRequired) {
        setBoot('ready');
        navigate('/setup', true);
        return;
      }
      try {
        const me = await api<{ user: User; connection: Connection }>('GET', '/auth/me');
        setState({ user: me.user, connection: me.connection });
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          setToken(null);
          if (route.path !== '/login') navigate('/login', true);
        } else throw e;
      }
      setBoot('ready');
    } catch (e: any) {
      setBootErr(e instanceof ApiError && e.code === 'offline' ? t('offline') : e.message);
      setBoot('error');
    }
  };

  useEffect(() => {
    applyModeClasses();
    installSpatialNav();
    setUnauthorizedHandler(() => {
      setToken(null);
      setState({ user: null });
      navigate('/login', true);
    });
    bootstrap();
    if ('serviceWorker' in navigator && location.protocol === 'https:') {
      navigator.serviceWorker.register('/sw.js').catch(() => undefined);
    }
  }, []);

  // Move focus into each new screen on the TV.
  useEffect(() => {
    if (isTV()) setTimeout(() => focusFirst(), 120);
    window.scrollTo(0, 0);
  }, [route.path]);

  if (boot === 'loading') return <Spinner label={t('loading')} />;
  if (boot === 'error') return <ErrorBox message={bootErr} onRetry={bootstrap} />;

  const seg = route.segments[0] || '';
  if (seg === 'setup' || (info && info.setupRequired)) return <Setup onDone={bootstrap} />;
  if (seg === 'login' || !user) return <Login />;
  if (seg === 'play') return <Player key={route.segments[1]} route={route} />;

  let screen;
  switch (seg) {
    case '':
      screen = <Home />;
      break;
    case 'library':
      screen = <Library key={route.segments[1]} route={route} />;
      break;
    case 'item':
      screen = <Item key={route.segments[1]} route={route} />;
      break;
    case 'search':
      screen = <Search route={route} />;
      break;
    case 'settings':
      screen = <Settings />;
      break;
    case 'admin':
      screen = user.isAdmin ? <Admin /> : <Home />;
      break;
    default:
      screen = <Home />;
  }
  return (
    <div class="app">
      <NavBar />
      <main class="main">{screen}</main>
    </div>
  );
}

function Root() {
  return (
    <>
      <App />
      <ToastHost />
    </>
  );
}

render(<Root />, document.getElementById('app')!);

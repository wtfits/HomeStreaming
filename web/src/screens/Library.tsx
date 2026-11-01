import { useEffect, useRef, useState } from 'preact/hooks';
import { api, ItemSummary, Library as Lib } from '../api';
import { ErrorBox, PosterCard, Spinner } from '../components/ui';
import { t } from '../i18n/en';
import { focusFirst } from '../nav/spatial';
import { navigate, qs, Route } from '../router';

const PAGE = 60;

export function Library({ route }: { route: Route }) {
  const id = route.segments[1];
  const tab = route.query.tab === 'shows' ? 'shows' : route.query.tab === 'movies' ? 'movies' : '';
  const sort = route.query.sort || 'title';
  const [lib, setLib] = useState<Lib | null>(null);
  const [items, setItems] = useState<ItemSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [search, setSearch] = useState(route.query.q || '');
  const sentinel = useRef<HTMLDivElement>(null);

  const effectiveTab = tab || (lib && lib.type === 'shows' ? 'shows' : lib && lib.type === 'movies' ? 'movies' : lib && lib.counts.movies === 0 ? 'shows' : 'movies');
  const type = effectiveTab === 'shows' ? 'series' : 'movie';

  useEffect(() => {
    api<Lib>('GET', '/libraries/' + id).then(setLib, (e) => setErr(e.message));
  }, [id]);

  const load = async (offset: number) => {
    setLoading(true);
    try {
      const order = sort === 'added' ? 'desc' : sort === 'year' ? 'desc' : 'asc';
      const res = await api<{ items: ItemSummary[]; total: number }>(
        'GET',
        `/items?libraryId=${id}&type=${type}&sort=${sort}&order=${order}&offset=${offset}&limit=${PAGE}` + (search ? '&search=' + encodeURIComponent(search) : '')
      );
      setItems((prev) => (offset === 0 ? res.items : prev.concat(res.items)));
      setTotal(res.total);
      if (offset === 0) setTimeout(() => focusFirst(), 50);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!lib) return;
    load(0);
  }, [lib, type, sort, search]);

  useEffect(() => {
    const el = sentinel.current;
    if (!el || typeof IntersectionObserver === 'undefined') return;
    const io = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting && !loading && items.length < total) load(items.length);
    });
    io.observe(el);
    return () => io.disconnect();
  }, [items, total, loading]);

  if (err) return <ErrorBox message={err} />;
  if (!lib) return <Spinner />;
  const setQuery = (patch: Record<string, string>) => navigate('/library/' + id + qs({ tab: effectiveTab, sort, q: search, ...patch }), true);
  const showTabs = lib.type === 'mixed' && lib.counts.movies > 0 && lib.counts.series > 0;
  return (
    <div class="page">
      <div class="toolbar">
        <h1>{lib.name}</h1>
        {showTabs ? (
          <div class="tabs" data-nav-section>
            <button class={'tab' + (effectiveTab === 'movies' ? ' active' : '')} onClick={() => setQuery({ tab: 'movies' })}>
              {t('movies')} <small>{lib.counts.movies}</small>
            </button>
            <button class={'tab' + (effectiveTab === 'shows' ? ' active' : '')} onClick={() => setQuery({ tab: 'shows' })}>
              {t('shows')} <small>{lib.counts.series}</small>
            </button>
          </div>
        ) : null}
        <div class="toolbar-right" data-nav-section>
          <input
            class="search-input"
            type="search"
            placeholder={t('searchPlaceholder')}
            value={search}
            onChange={(e) => setSearch((e.target as HTMLInputElement).value)}
          />
          <select value={sort} onChange={(e) => setQuery({ sort: (e.target as HTMLSelectElement).value })}>
            <option value="title">{t('sortTitle')}</option>
            <option value="added">{t('sortAdded')}</option>
            <option value="year">{t('sortYear')}</option>
          </select>
        </div>
      </div>
      {!items.length && !loading ? <p class="empty">{t('noResults')}</p> : null}
      <div class="grid" data-nav-section>
        {items.map((it) => (
          <PosterCard key={it.id} it={it} />
        ))}
      </div>
      <div ref={sentinel} class="sentinel" />
      {loading ? <Spinner /> : null}
    </div>
  );
}

export function Search({ route }: { route: Route }) {
  const [q, setQ] = useState(route.query.q || '');
  const [items, setItems] = useState<ItemSummary[] | null>(null);
  useEffect(() => {
    if (!q.trim()) {
      setItems(null);
      return;
    }
    const id = setTimeout(() => {
      api<{ items: ItemSummary[] }>('GET', '/items?search=' + encodeURIComponent(q.trim()) + '&limit=100').then((r) => setItems(r.items));
      navigate('/search' + qs({ q }), true);
    }, 250);
    return () => clearTimeout(id);
  }, [q]);
  return (
    <div class="page">
      <div class="toolbar">
        <h1>{t('navSearch')}</h1>
      </div>
      <input
        class="search-input search-big"
        type="search"
        autoFocus
        data-autofocus
        placeholder={t('searchPlaceholder')}
        value={q}
        onInput={(e) => setQ((e.target as HTMLInputElement).value)}
      />
      {items && !items.length ? <p class="empty">{t('noResults')}</p> : null}
      <div class="grid" data-nav-section>
        {(items || []).map((it) => (
          <PosterCard key={it.id} it={it} />
        ))}
      </div>
    </div>
  );
}

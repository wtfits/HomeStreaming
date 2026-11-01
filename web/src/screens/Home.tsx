import { useEffect } from 'preact/hooks';
import { api, ItemSummary, Library } from '../api';
import { ErrorBox, PosterCard, Row, Spinner, ThumbCard, useAsync } from '../components/ui';
import { t } from '../i18n/en';
import { focusFirst } from '../nav/spatial';

interface HomeData {
  continueWatching: ItemSummary[];
  nextUp: ItemSummary[];
  recentlyAdded: ItemSummary[];
  libraries: { library: Library; sections: { type: string; count: number }[] }[];
}

interface Section {
  key: string;
  title: string;
  libraryId: string;
  type: string;
  items: ItemSummary[];
}

async function loadHome(): Promise<{ home: HomeData; sections: Section[] }> {
  const home = await api<HomeData>('GET', '/home');
  const sections: Section[] = [];
  for (const l of home.libraries) {
    for (const s of l.sections) {
      const res = await api<{ items: ItemSummary[] }>('GET', `/items?libraryId=${l.library.id}&type=${s.type}&sort=added&limit=20`);
      const label = s.type === 'movie' ? t('movies') : t('shows');
      sections.push({ key: l.library.id + s.type, title: home.libraries.length > 1 ? l.library.name + ' · ' + label : label, libraryId: l.library.id, type: s.type, items: res.items });
    }
  }
  return { home, sections };
}

export function Home() {
  const { data, error, loading, reload } = useAsync(loadHome, []);
  useEffect(() => {
    if (data) setTimeout(() => focusFirst(), 50);
  }, [data]);
  // Refresh while items are still being processed.
  useEffect(() => {
    if (!data) return;
    const pending = data.sections.some((s) => s.items.some((i) => i.status === 'pending'));
    if (!pending) return;
    const id = setTimeout(reload, 5000);
    return () => clearTimeout(id);
  }, [data]);

  if (loading && !data) return <Spinner label={t('loading')} />;
  if (error) return <ErrorBox message={error} onRetry={reload} />;
  if (!data) return null;
  const { home, sections } = data;
  const empty = !home.continueWatching.length && !home.nextUp.length && !sections.length;
  return (
    <div class="page home">
      {empty ? <p class="empty">{t('emptyHome')}</p> : null}
      {home.continueWatching.length ? (
        <Row title={t('continueWatching')}>
          {home.continueWatching.map((it) => (
            <ThumbCard key={it.id} it={it} />
          ))}
        </Row>
      ) : null}
      {home.nextUp.length ? (
        <Row title={t('nextUp')}>
          {home.nextUp.map((it) => (
            <ThumbCard key={it.id} it={it} />
          ))}
        </Row>
      ) : null}
      {home.recentlyAdded.length ? (
        <Row title={t('recentlyAdded')}>
          {home.recentlyAdded.map((it) => (
            <ThumbCard key={it.id} it={it} />
          ))}
        </Row>
      ) : null}
      {sections.map((s) => (
        <Row key={s.key} title={s.title} more={`/library/${s.libraryId}?tab=${s.type === 'movie' ? 'movies' : 'shows'}`}>
          {s.items.map((it) => (
            <PosterCard key={it.id} it={it} />
          ))}
        </Row>
      ))}
    </div>
  );
}

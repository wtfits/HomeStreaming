import { useEffect, useState } from 'preact/hooks';
import { api } from '../api';
import { Modal, Spinner } from '../components/ui';
import { t } from '../i18n/en';

interface FsResponse {
  path: string;
  parent: string | null;
  entries: { name: string; path: string }[];
  roots: { name: string; path: string }[];
}

/** Server-side directory browser (admin only). */
export function FolderPicker({ initial, onPick, onClose }: { initial?: string; onPick: (p: string) => void; onClose: () => void }) {
  const [path, setPath] = useState<string>(initial || '');
  const [data, setData] = useState<FsResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    setErr(null);
    api<FsResponse>('GET', '/admin/fs' + (path ? '?path=' + encodeURIComponent(path) : '')).then(setData, (e) => {
      setErr(e.message);
      if (path) setPath('');
    });
  }, [path]);

  return (
    <Modal title={t('browse')} onClose={onClose}>
      <div class="picker">
        <div class="picker-path">{data ? data.path || '—' : ''}</div>
        {err ? <p class="form-error">{err}</p> : null}
        {!data ? (
          <Spinner />
        ) : (
          <div class="menu-list picker-list">
            {data.parent !== null ? (
              <button class="menu-item" onClick={() => setPath(data.parent || '')}>
                ⬑ ..
              </button>
            ) : null}
            {data.entries.map((e) => (
              <button class="menu-item" key={e.path} onClick={() => setPath(e.path)}>
                📁 {e.name}
              </button>
            ))}
          </div>
        )}
        <div class="actions">
          <button class="btn" onClick={onClose}>
            {t('cancel')}
          </button>
          <button class="btn btn-primary" disabled={!data || !data.path} onClick={() => data && onPick(data.path)}>
            {t('chooseFolder')}
          </button>
        </div>
      </div>
    </Modal>
  );
}

// Minimal hash router (works when served by the server and from file:// in
// the future webOS package).
import { useEffect, useState } from 'preact/hooks';

export interface Route {
  path: string;
  segments: string[];
  query: Record<string, string>;
}

export function parseHash(hash: string): Route {
  let h = hash.replace(/^#/, '');
  if (!h.startsWith('/')) h = '/' + h;
  const qi = h.indexOf('?');
  const path = qi >= 0 ? h.slice(0, qi) : h;
  const query: Record<string, string> = {};
  if (qi >= 0) {
    for (const part of h.slice(qi + 1).split('&')) {
      if (!part) continue;
      const eq = part.indexOf('=');
      const k = decodeURIComponent(eq >= 0 ? part.slice(0, eq) : part);
      query[k] = eq >= 0 ? decodeURIComponent(part.slice(eq + 1)) : '';
    }
  }
  return { path, segments: path.split('/').filter(Boolean), query };
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseHash(location.hash));
  useEffect(() => {
    const on = () => setRoute(parseHash(location.hash));
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return route;
}

export function navigate(path: string, replace = false) {
  const target = '#' + path;
  if (replace) {
    history.replaceState(null, '', target);
    window.dispatchEvent(new HashChangeEvent('hashchange'));
  } else if (location.hash !== target) {
    location.hash = target;
  }
}

export function href(path: string): string {
  return '#' + path;
}

export function qs(params: Record<string, string | number | undefined | null>): string {
  const parts: string[] = [];
  for (const k of Object.keys(params)) {
    const v = params[k];
    if (v !== undefined && v !== null && v !== '') parts.push(encodeURIComponent(k) + '=' + encodeURIComponent(String(v)));
  }
  return parts.length ? '?' + parts.join('&') : '';
}

// Device/platform detection and TV mode.
import { prefs } from './api';

export type Platform = 'ios' | 'webos' | 'desktop' | 'android' | 'other';

export function platform(): Platform {
  const override = prefs.get('platform') || new URLSearchParamsLite(location.search).get('platform');
  if (override === 'ios' || override === 'webos' || override === 'desktop' || override === 'android') return override;
  const ua = navigator.userAgent;
  if (/Web0S|webOS/.test(ua)) return 'webos';
  if (/iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return 'ios';
  if (/Android/.test(ua)) return 'android';
  if (/Windows|Macintosh|Linux|CrOS/.test(ua)) return 'desktop';
  return 'other';
}

// URLSearchParams exists in Chrome 79, but keep a tiny parser for clarity.
class URLSearchParamsLite {
  private m: Record<string, string> = {};
  constructor(s: string) {
    s.replace(/^\?/, '')
      .split('&')
      .forEach((p) => {
        if (!p) return;
        const i = p.indexOf('=');
        this.m[decodeURIComponent(i >= 0 ? p.slice(0, i) : p)] = i >= 0 ? decodeURIComponent(p.slice(i + 1)) : '';
      });
  }
  get(k: string) {
    return this.m[k] || '';
  }
}

export function isTV(): boolean {
  const q = new URLSearchParamsLite(location.search).get('tv');
  if (q === '1') prefs.set('tvMode', '1');
  if (q === '0') prefs.set('tvMode', '0');
  const stored = prefs.get('tvMode');
  if (stored === '1') return true;
  if (stored === '0') return false;
  return /Web0S|webOS/.test(navigator.userAgent);
}

export function isIOS(): boolean {
  return platform() === 'ios';
}

export function applyModeClasses() {
  const root = document.documentElement;
  root.classList.toggle('tv', isTV());
  root.classList.toggle('ios', isIOS());
}

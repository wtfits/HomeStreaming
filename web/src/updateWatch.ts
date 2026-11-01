// Keeps long-open pages (the TV browser, the iPhone home-screen app) on the
// server's current version: when the server reports a different version than
// the one this page loaded with, the page reloads itself — right away when
// nothing is playing and no text is being typed, otherwise as soon as that is
// no longer the case (leaving the player, closing the keyboard).
import { api, SystemInfo } from './api';
import { getState, setState } from './state';

const CHECK_EVERY_MS = 60000;
const RELOADED_KEY = 'hs.updatedTo';

let loadedVersion = '';
let pending = '';
let checking = false;
let started = false;

function inPlayer(): boolean {
  return location.hash.indexOf('#/play/') === 0;
}

function typing(): boolean {
  const a = document.activeElement as HTMLInputElement | null;
  if (!a) return false;
  if (a.tagName === 'TEXTAREA' || a.tagName === 'SELECT') return true;
  if (a.tagName !== 'INPUT') return false;
  return ['range', 'checkbox', 'radio', 'button', 'submit'].indexOf(a.type) < 0;
}

function store(k: string, v: string | null) {
  try {
    if (v === null) sessionStorage.removeItem(k);
    else sessionStorage.setItem(k, v);
  } catch (_) {
    /* private mode */
  }
}

function read(k: string): string | null {
  try {
    return sessionStorage.getItem(k);
  } catch (_) {
    return null;
  }
}

/** Reloads now if it is safe; otherwise remembers to do it later. */
function maybeReload() {
  if (!pending) return;
  if (inPlayer() || typing() || document.visibilityState === 'hidden') {
    if (getState().updatedTo !== pending) setState({ updatedTo: pending });
    return;
  }
  store(RELOADED_KEY, pending);
  location.reload();
}

async function check() {
  if (checking || pending) return;
  checking = true;
  try {
    const info = await api<SystemInfo>('GET', '/system/info');
    if (!loadedVersion) loadedVersion = info.version;
    else if (info.version && info.version !== loadedVersion) pending = info.version;
  } catch (_) {
    /* server restarting or offline: try again later */
  }
  checking = false;
  maybeReload();
}

/** Starts watching; call once after the first /system/info succeeded. */
export function startUpdateWatch() {
  if (started) return;
  started = true;
  const info = getState().info;
  loadedVersion = info ? info.version : '';
  setInterval(() => {
    if (document.visibilityState !== 'hidden') check();
  }, CHECK_EVERY_MS);
  const soon = () => {
    if (pending) maybeReload();
    else check();
  };
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') soon();
  });
  window.addEventListener('focus', soon);
  window.addEventListener('online', soon);
  window.addEventListener('pageshow', soon);
  window.addEventListener('hashchange', () => setTimeout(maybeReload, 0));
  document.addEventListener('focusout', () => setTimeout(maybeReload, 0));
}

/** Reload requested from the update bar. */
export function reloadNow() {
  if (pending) store(RELOADED_KEY, pending);
  location.reload();
}

/** The version this page just reloaded into, once (for a "Updated to …" toast). */
export function takeReloadedVersion(): string | null {
  const v = read(RELOADED_KEY);
  if (v) store(RELOADED_KEY, null);
  return v;
}

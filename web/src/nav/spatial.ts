// D-pad spatial navigation for TV mode (LG webOS remote): arrow keys move
// focus to the best candidate in that direction, Enter clicks, Back pops.
import { isTV } from '../device';

export const KEY = {
  LEFT: 37,
  UP: 38,
  RIGHT: 39,
  DOWN: 40,
  ENTER: 13,
  BACK_WEBOS: 461,
  ESC: 27,
  BACKSPACE: 8,
  PLAY: 415,
  PAUSE: 19,
  STOP: 413,
  REWIND: 412,
  FAST_FORWARD: 417,
};

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"]), [data-focusable]';

type BackHandler = () => boolean;
const backHandlers: BackHandler[] = [];

/** Register a handler for Back; return true from it to consume the key. */
export function pushBackHandler(fn: BackHandler): () => void {
  backHandlers.push(fn);
  return () => {
    const i = backHandlers.lastIndexOf(fn);
    if (i >= 0) backHandlers.splice(i, 1);
  };
}

export function isBackKey(e: KeyboardEvent): boolean {
  const target = e.target as HTMLElement | null;
  const typing = !!target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA');
  return e.keyCode === KEY.BACK_WEBOS || e.keyCode === KEY.ESC || (e.keyCode === KEY.BACKSPACE && !typing);
}

function visible(el: HTMLElement): boolean {
  const r = el.getBoundingClientRect();
  if (r.width === 0 || r.height === 0) return false;
  const st = getComputedStyle(el);
  return st.visibility !== 'hidden' && st.display !== 'none';
}

function scope(): ParentNode {
  // Modals/menus trap focus.
  const traps = document.querySelectorAll('[data-focus-trap]');
  return traps.length ? (traps[traps.length - 1] as HTMLElement) : document;
}

export function focusables(root: ParentNode = scope()): HTMLElement[] {
  return Array.prototype.slice.call(root.querySelectorAll(FOCUSABLE)).filter((el: HTMLElement) => visible(el) && !el.closest('[data-nav-skip]'));
}

type Dir = 'left' | 'right' | 'up' | 'down';

function score(from: DOMRect, to: DOMRect, dir: Dir): number {
  const fcx = from.left + from.width / 2;
  const fcy = from.top + from.height / 2;
  const tcx = to.left + to.width / 2;
  const tcy = to.top + to.height / 2;
  let primary: number, overlap: number, ortho: number;
  switch (dir) {
    case 'right':
      primary = to.left - from.right;
      if (tcx <= fcx) return Infinity;
      overlap = Math.min(from.bottom, to.bottom) - Math.max(from.top, to.top);
      ortho = Math.abs(tcy - fcy);
      break;
    case 'left':
      primary = from.left - to.right;
      if (tcx >= fcx) return Infinity;
      overlap = Math.min(from.bottom, to.bottom) - Math.max(from.top, to.top);
      ortho = Math.abs(tcy - fcy);
      break;
    case 'down':
      primary = to.top - from.bottom;
      if (tcy <= fcy) return Infinity;
      overlap = Math.min(from.right, to.right) - Math.max(from.left, to.left);
      ortho = Math.abs(tcx - fcx);
      break;
    default:
      primary = from.top - to.bottom;
      if (tcy >= fcy) return Infinity;
      overlap = Math.min(from.right, to.right) - Math.max(from.left, to.left);
      ortho = Math.abs(tcx - fcx);
  }
  if (primary < -4) primary = Math.max(0, primary + 4);
  // Left/Right stay within the visual row (like TV launchers): at the end of a
  // row nothing happens instead of jumping to an unrelated element.
  if ((dir === 'left' || dir === 'right') && overlap <= 0) return Infinity;
  // Up/Down prefer candidates overlapping on the perpendicular axis, then distance.
  const overlapBonus = overlap > 0 ? 0 : 100000;
  return overlapBonus + Math.max(primary, 0) * 2 + ortho;
}

const sectionMemory = new WeakMap<Element, HTMLElement>();

export function move(dir: Dir): boolean {
  const current = document.activeElement as HTMLElement | null;
  const list = focusables();
  if (!list.length) return false;
  if (!current || current === document.body || list.indexOf(current) < 0) {
    focusEl(list[0]);
    return true;
  }
  const from = current.getBoundingClientRect();
  let best: HTMLElement | null = null;
  let bestScore = Infinity;
  for (const el of list) {
    if (el === current) continue;
    const s = score(from, el.getBoundingClientRect(), dir);
    if (s < bestScore) {
      bestScore = s;
      best = el;
    }
  }
  if (!best) return false;
  // Entering another section: restore its remembered child.
  const fromSec = current.closest('[data-nav-section]');
  const toSec = best.closest('[data-nav-section]');
  if (toSec && toSec !== fromSec) {
    const remembered = sectionMemory.get(toSec);
    if (remembered && document.body.contains(remembered) && visible(remembered) && (dir === 'up' || dir === 'down')) best = remembered;
  }
  focusEl(best);
  return true;
}

export function focusEl(el: HTMLElement) {
  el.focus({ preventScroll: true } as FocusOptions);
  const sec = el.closest('[data-nav-section]');
  if (sec) sectionMemory.set(sec, el);
  try {
    el.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'smooth' } as ScrollIntoViewOptions);
  } catch (_) {
    el.scrollIntoView(false);
  }
}

/** Focus the first focusable inside root (or the page) — used after navigation in TV mode. */
export function focusFirst(root?: ParentNode) {
  if (!isTV()) return;
  const list = focusables(root || scope());
  const preferred = list.find((el) => el.hasAttribute('data-autofocus'));
  if (preferred || list[0]) focusEl((preferred || list[0]) as HTMLElement);
}

let installed = false;
let playerKeyHandler: ((e: KeyboardEvent) => boolean) | null = null;

/** The player takes over keys while it is open. */
export function setPlayerKeys(fn: ((e: KeyboardEvent) => boolean) | null) {
  playerKeyHandler = fn;
}

export function installSpatialNav() {
  if (installed) return;
  installed = true;
  document.addEventListener(
    'keydown',
    (e) => {
      if (playerKeyHandler && playerKeyHandler(e)) {
        e.preventDefault();
        return;
      }
      if (isBackKey(e)) {
        for (let i = backHandlers.length - 1; i >= 0; i--) {
          if (backHandlers[i]()) {
            e.preventDefault();
            return;
          }
        }
        if (e.keyCode !== KEY.BACKSPACE || isTV()) {
          if (location.hash && location.hash !== '#/' && history.length > 1) {
            e.preventDefault();
            history.back();
          }
        }
        return;
      }
      if (!isTV()) return;
      const target = e.target as HTMLElement;
      const typing = target && (target.tagName === 'INPUT' || target.tagName === 'SELECT' || target.tagName === 'TEXTAREA');
      let dir: Dir | null = null;
      if (e.keyCode === KEY.LEFT) dir = 'left';
      if (e.keyCode === KEY.RIGHT) dir = 'right';
      if (e.keyCode === KEY.UP) dir = 'up';
      if (e.keyCode === KEY.DOWN) dir = 'down';
      if (dir) {
        if (typing && (dir === 'left' || dir === 'right') && target.tagName === 'INPUT') return;
        if (target && target.tagName === 'SELECT' && (dir === 'up' || dir === 'down')) return;
        if (move(dir)) e.preventDefault();
        return;
      }
      if (e.keyCode === KEY.ENTER && target && target.hasAttribute('data-focusable') && target.tagName !== 'A' && target.tagName !== 'BUTTON') {
        e.preventDefault();
        target.click();
      }
    },
    true
  );
  // Magic Remote pointer: hovering focuses.
  document.addEventListener('mouseover', (e) => {
    if (!isTV()) return;
    const el = (e.target as HTMLElement).closest(FOCUSABLE) as HTMLElement | null;
    if (el && el !== document.activeElement) el.focus({ preventScroll: true } as FocusOptions);
  });
}

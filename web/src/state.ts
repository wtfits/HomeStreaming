// Tiny global state: current user, server info, connection.
import { useEffect, useState } from 'preact/hooks';
import { Connection, SystemInfo, User } from './api';

export interface AppState {
  info: SystemInfo | null;
  user: User | null;
  connection: Connection | null;
  /** Set when the server runs a newer version than this page (see updateWatch). */
  updatedTo: string | null;
}

let state: AppState = { info: null, user: null, connection: null, updatedTo: null };
const listeners = new Set<() => void>();

export function getState(): AppState {
  return state;
}

export function setState(patch: Partial<AppState>) {
  state = { ...state, ...patch };
  listeners.forEach((l) => l());
}

export function useAppState(): AppState {
  const [, force] = useState(0);
  useEffect(() => {
    const l = () => force((x) => x + 1);
    listeners.add(l);
    return () => {
      listeners.delete(l);
    };
  }, []);
  return state;
}

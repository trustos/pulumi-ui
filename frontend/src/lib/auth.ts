import { writable } from 'svelte/store';
import type { User } from './types';

// currentUser is null while loading, undefined if not authenticated, or the User object.
export const currentUser = writable<User | null | undefined>(undefined);

export async function authStatus(): Promise<{ hasUsers: boolean }> {
  const res = await fetch('/api/auth/status');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function register(username: string, password: string): Promise<User> {
  const res = await fetch('/api/auth/register', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  const user: User = await res.json();
  currentUser.set(user);
  return user;
}

export async function login(username: string, password: string): Promise<User> {
  const res = await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  const user: User = await res.json();
  currentUser.set(user);
  return user;
}

export async function logout(): Promise<void> {
  await fetch('/api/auth/logout', { method: 'POST' });
  currentUser.set(null);
}

export async function importSetup(database: File, key: File): Promise<{ ok: boolean; message: string }> {
  const form = new FormData();
  form.append('database', database);
  form.append('key', key);
  const res = await fetch('/api/auth/import', { method: 'POST', body: form });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function fetchMe(): Promise<User | null> {
  const res = await fetch('/api/auth/me');
  if (res.status === 401) {
    currentUser.set(null);
    return null;
  }
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const user: User = await res.json();
  currentUser.set(user);
  return user;
}

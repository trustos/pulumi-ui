import { writable } from 'svelte/store';

function createRouter() {
  const { subscribe, set } = writable(
    typeof window !== 'undefined' ? window.location.pathname : '/'
  );

  if (typeof window !== 'undefined') {
    window.addEventListener('popstate', () => {
      set(window.location.pathname);
    });
  }

  return {
    subscribe,
    navigate(path: string) {
      if (typeof window !== 'undefined') {
        window.history.pushState({}, '', path);
        set(path);
      }
    },
  };
}

export const router = createRouter();

export function navigate(path: string) {
  router.navigate(path);
}

<script lang="ts">
  import '../app.css';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { QueryClient, QueryClientProvider } from '@tanstack/svelte-query';
  import { session } from '$lib/session.svelte';
  import { theme } from '$lib/theme.svelte';
  import Sidebar from '$lib/components/Sidebar.svelte';
  import Header from '$lib/components/Header.svelte';
  import ToastHost from '$lib/components/ToastHost.svelte';
  import Icon from '$lib/components/Icon.svelte';

  let { children } = $props();

  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 20_000,
        retry: 1,
        refetchOnWindowFocus: false
      }
    }
  });

  let sidebarOpen = $state(false);

  onMount(() => {
    theme.init();
    session.refresh();
  });

  $effect(() => {
    if (session.loading) return;
    const onLogin = page.url.pathname === '/login';
    if (!session.current && !onLogin) {
      goto('/login');
    } else if (session.current && onLogin) {
      goto('/');
    }
  });

  const isLogin = $derived(page.url.pathname === '/login');
</script>

<QueryClientProvider client={queryClient}>
  {#if isLogin}
    {@render children()}
  {:else if session.loading || !session.current}
    <div class="boot">
      <div class="boot-mark" aria-hidden="true"><span></span><span></span></div>
      <div class="boot-copy">
        <strong>AccessNow</strong>
        <p>{session.loading ? 'Opening your workspace…' : (session.lastError ?? 'Returning to sign in…')}</p>
      </div>
      {#if session.loading}<Icon name="spinner" class="spin" size="1.05em" />{/if}
    </div>
  {:else}
    <div class="app-shell">
      <div class="desktop-sidebar">
        <Sidebar onnavigate={() => (sidebarOpen = false)} />
      </div>
      <div class="main">
        <Header onmenu={() => (sidebarOpen = true)} />
        <main>
          {@render children()}
        </main>
      </div>
    </div>
  {/if}

  {#if sidebarOpen}
    <div
      class="drawer-backdrop"
      role="presentation"
      onclick={(event) => {
        if (event.target === event.currentTarget) sidebarOpen = false;
      }}
      onkeydown={(event) => {
        if (event.key === 'Escape') sidebarOpen = false;
      }}
    >
      <div class="drawer" role="dialog" aria-modal="true" aria-label="Navigation" tabindex="-1">
        <Sidebar onnavigate={() => (sidebarOpen = false)} />
        <button class="drawer-close" type="button" aria-label="Close navigation" onclick={() => (sidebarOpen = false)}>
          <Icon name="xmark" />
        </button>
      </div>
    </div>
  {/if}
</QueryClientProvider>

<style>
  .app-shell {
    display: flex;
    min-height: 100vh;
  }

  .main {
    min-width: 0;
    flex: 1;
  }

  main {
    min-height: calc(100vh - var(--header-height));
  }

  .boot {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 15px;
    min-height: 100vh;
    padding: 24px;
    color: var(--ink-soft);
  }

  .boot-mark {
    display: flex;
    align-items: flex-end;
    gap: 3px;
    width: 40px;
    height: 40px;
    padding: 9px;
    border-radius: 12px;
    background: var(--brand);
    box-shadow: 0 10px 28px rgba(217, 84, 45, 0.3);
  }

  .boot-mark span {
    width: 8px;
    border-radius: 2px;
    background: #fff;
  }

  .boot-mark span:first-child {
    height: 21px;
  }

  .boot-mark span:last-child {
    height: 13px;
    opacity: 0.62;
  }

  .boot-copy strong {
    display: block;
    color: var(--ink);
    font-size: 14px;
    font-weight: 820;
  }

  .boot-copy p {
    margin-top: 2px;
    font-size: 11px;
  }

  :global(.spin) {
    animation: spin 900ms linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .drawer-backdrop {
    position: fixed;
    inset: 0;
    z-index: 70;
    background: rgba(8, 15, 25, 0.5);
    backdrop-filter: blur(3px);
  }

  .drawer {
    position: relative;
    width: min(var(--sidebar-width), 86vw);
    height: 100%;
    box-shadow: var(--shadow-lg);
  }

  .drawer-close {
    position: absolute;
    top: 18px;
    right: -46px;
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    border: 1px solid rgba(255, 255, 255, 0.18);
    border-radius: 9px;
    background: rgba(16, 26, 43, 0.72);
    color: #fff;
  }

  @media (min-width: 1081px) {
    .drawer-backdrop {
      display: none;
    }
  }

  @media (max-width: 1080px) {
    .desktop-sidebar {
      display: none;
    }
  }
</style>

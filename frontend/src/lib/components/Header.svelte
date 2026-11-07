<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { session } from '$lib/session.svelte';
  import { theme } from '$lib/theme.svelte';
  import { findNavItem } from '$lib/nav';
  import { initials } from '$lib/format';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  let { onmenu }: { onmenu: () => void } = $props();

  const section = $derived(
    page.url.pathname === '/'
      ? 'Overview'
      : page.url.pathname.startsWith('/visitors')
        ? 'Visitors'
        : (findNavItem(page.url.pathname)?.label ?? 'Workspace')
  );

  const today = new Intl.DateTimeFormat('en-GB', {
    weekday: 'short',
    day: '2-digit',
    month: 'short'
  }).format(new Date());

  const userName = $derived(
    [session.user?.first_name, session.user?.last_name].filter(Boolean).join(' ') || 'Account'
  );

  const canCreateVisit = $derived(session.can('visit.create') && session.moduleOn('visitor_management'));
  const canSeeNotifications = $derived(
    session.can('notification.view') && session.moduleOn('notifications')
  );

  async function signOut() {
    await session.logout();
    await goto('/login');
  }
</script>

<header class="topbar">
  <div class="left">
    <button class="menu" type="button" aria-label="Open navigation" onclick={onmenu}>
      <Icon name="menu" size="1.05em" />
    </button>
    <div class="context">
      <span class="section">{section}</span>
      <span class="date">{today}</span>
    </div>
  </div>

  <div class="right">
    {#if canSeeNotifications}
      <a class="icon-button" href="/workspace/notifications" aria-label="Notifications" title="Notifications">
        <Icon name="bell" size="1em" />
      </a>
    {/if}
    <button
      class="icon-button"
      type="button"
      aria-label="Toggle colour theme"
      title="Toggle light and dark"
      onclick={() => theme.toggle()}
    >
      <Icon name={theme.value === 'light' ? 'moon' : 'sun'} size="1em" />
    </button>

    {#if canCreateVisit}
      <span class="new-visit"><Button href="/visitors/new" icon="plus" size="sm">New visit</Button></span>
    {/if}

    <details class="user-menu">
      <summary aria-label="Open account menu">
        <span class="avatar">{initials(userName)}</span>
        <span class="identity">
          <strong>{userName}</strong>
          <small>{session.site?.name ?? session.tenant?.name ?? ''}</small>
        </span>
        <Icon name="chevronDown" size="0.7em" />
      </summary>
      <div class="menu-panel">
        <div class="menu-head">
          <p>{userName}</p>
          <span>{session.user?.email ?? session.user?.username ?? 'Signed in'}</span>
        </div>
        <div class="menu-meta">
          <span>Workspace</span>
          <strong>{session.tenant?.name}</strong>
        </div>
        {#if session.isReadOnly}
          <div class="readonly-note"><Icon name="lock" size="0.8em" /> Read-only auditor account</div>
        {/if}
        <button type="button" class="signout" onclick={signOut}>
          <Icon name="signOut" size="0.88em" />
          Sign out
        </button>
      </div>
    </details>
  </div>
</header>

<style>
  .topbar {
    position: sticky;
    top: 0;
    z-index: 30;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 18px;
    height: var(--header-height);
    padding: 0 34px;
    border-bottom: 1px solid var(--line);
    background: color-mix(in srgb, var(--bg) 86%, transparent);
    backdrop-filter: blur(14px);
  }

  .left,
  .right {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .menu {
    display: none;
    place-items: center;
    width: 36px;
    height: 36px;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: var(--surface);
    color: var(--ink-soft);
  }

  .context {
    display: flex;
    align-items: baseline;
    gap: 10px;
  }

  .section {
    font-size: 12px;
    font-weight: 800;
    letter-spacing: -0.01em;
  }

  .date {
    color: var(--ink-faint);
    font-size: 10.5px;
    font-weight: 650;
  }

  .icon-button {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: var(--surface);
    color: var(--ink-soft);
    transition:
      border-color 150ms ease,
      color 150ms ease,
      background 150ms ease;
  }

  .icon-button:hover {
    border-color: var(--line-strong);
    background: var(--surface-muted);
    color: var(--ink);
  }

  .user-menu {
    position: relative;
    margin-left: 3px;
  }

  summary {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 4px 6px 4px 4px;
    border: 1px solid transparent;
    border-radius: 11px;
    list-style: none;
    cursor: pointer;
  }

  summary::-webkit-details-marker {
    display: none;
  }

  summary:hover {
    border-color: var(--line);
    background: var(--surface);
  }

  .avatar {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    border-radius: 9px;
    background: var(--ink);
    color: var(--bg);
    font-size: 10.5px;
    font-weight: 850;
    letter-spacing: 0.02em;
  }

  .identity {
    display: flex;
    flex-direction: column;
    max-width: 150px;
    line-height: 1.2;
  }

  .identity strong {
    overflow: hidden;
    font-size: 11px;
    font-weight: 800;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .identity small {
    overflow: hidden;
    margin-top: 2px;
    color: var(--ink-faint);
    font-size: 9.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .menu-panel {
    position: absolute;
    top: calc(100% + 9px);
    right: 0;
    width: 250px;
    padding: 8px;
    border: 1px solid var(--line);
    border-radius: 13px;
    background: var(--surface-raised);
    box-shadow: var(--shadow-md);
  }

  .menu-head {
    padding: 10px;
  }

  .menu-head p {
    font-size: 12.5px;
    font-weight: 800;
  }

  .menu-head span {
    display: block;
    margin-top: 2px;
    color: var(--ink-faint);
    font-size: 10.5px;
  }

  .menu-meta {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 9px 10px;
    border-top: 1px solid var(--line);
    color: var(--ink-faint);
    font-size: 10px;
  }

  .menu-meta strong {
    overflow: hidden;
    color: var(--ink-soft);
    font-weight: 700;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .readonly-note {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 2px 4px 6px;
    padding: 7px 8px;
    border-radius: 7px;
    background: var(--amber-soft);
    color: var(--amber);
    font-size: 10px;
    font-weight: 700;
  }

  .signout {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 9px 10px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: var(--danger);
    font-size: 11.5px;
    font-weight: 750;
    text-align: left;
  }

  .signout:hover {
    background: var(--danger-soft);
  }

  @media (max-width: 1080px) {
    .topbar {
      padding: 0 22px;
    }

    .menu {
      display: grid;
    }

    .identity,
    summary > :global(.fa-icon) {
      display: none;
    }
  }

  @media (max-width: 640px) {
    .topbar {
      padding: 0 16px;
    }

    .date,
    .new-visit {
      display: none;
    }
  }
</style>

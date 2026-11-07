<script lang="ts">
  import { page } from '$app/state';
  import { session } from '$lib/session.svelte';
  import { visibleNavigation, navigation } from '$lib/nav';
  import Icon from './Icon.svelte';

  let { onnavigate }: { onnavigate: () => void } = $props();

  const groups = $derived(
    visibleNavigation(navigation, (permission) => session.can(permission), (module) => session.moduleOn(module))
  );

  function active(href: string): boolean {
    return page.url.pathname === href || page.url.pathname.startsWith(href + '/');
  }
</script>

<aside class="sidebar">
  <a class="brand" href="/" onclick={onnavigate} aria-label="AccessNow home">
    <span class="mark" aria-hidden="true">
      <span></span>
      <span></span>
    </span>
    <span class="wordmark">
      <strong>AccessNow</strong>
      <small>Site access control</small>
    </span>
  </a>

  <nav aria-label="Primary">
    <a class="nav-item overview" class:active={page.url.pathname === '/'} href="/" onclick={onnavigate}>
      <Icon name="gauge" size="1.02em" />
      <span>Overview</span>
    </a>

    {#each groups as group}
      <div class="group">
        <p class="group-label">{group.label}</p>
        {#each group.items as item}
          <a
            class="nav-item"
            class:active={active(item.href)}
            href={item.href}
            title={item.description}
            onclick={onnavigate}
          >
            <Icon name={item.icon} size="1.02em" />
            <span>{item.label}</span>
            {#if item.module === 'visitor_management' && session.can('visit.approve')}
              <span class="live-dot" title="Approvals available"></span>
            {/if}
          </a>
        {/each}
      </div>
    {/each}
  </nav>

  <div class="sidebar-foot">
    <div class="workspace">
      <span class="workspace-icon"><Icon name="building" size="0.95em" /></span>
      <div>
        <p>{session.tenant?.name ?? 'Workspace'}</p>
        <span>{session.site?.name ?? 'All sites'}</span>
      </div>
    </div>
    <div class="trust">
      <Icon name="shield" size="0.8em" />
      <span>Permissions enforced server-side</span>
    </div>
  </div>
</aside>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    width: var(--sidebar-width);
    height: 100vh;
    padding: 22px 14px 18px;
    position: sticky;
    top: 0;
    overflow: hidden;
    background:
      radial-gradient(circle at 20% 0%, rgba(255, 122, 77, 0.16), transparent 15rem),
      var(--sidebar);
    color: var(--sidebar-ink);
  }

  .sidebar::after {
    content: '';
    position: absolute;
    inset: 0 0 0 auto;
    width: 1px;
    background: var(--sidebar-line);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 8px 23px;
    color: inherit;
  }

  .mark {
    position: relative;
    display: flex;
    align-items: flex-end;
    justify-content: center;
    gap: 3px;
    width: 36px;
    height: 36px;
    padding: 8px;
    border-radius: 11px;
    background: var(--brand);
    box-shadow: 0 8px 22px rgba(217, 84, 45, 0.34);
  }

  .mark span {
    display: block;
    width: 7px;
    border-radius: 2px;
    background: #fff;
  }

  .mark span:first-child {
    height: 19px;
  }

  .mark span:last-child {
    height: 12px;
    opacity: 0.62;
  }

  .wordmark {
    display: flex;
    flex-direction: column;
    line-height: 1.15;
  }

  .wordmark strong {
    font-size: 16px;
    font-weight: 820;
    letter-spacing: -0.035em;
  }

  .wordmark small {
    margin-top: 3px;
    color: var(--sidebar-muted);
    font-size: 9.5px;
    font-weight: 650;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  nav {
    flex: 1;
    overflow-y: auto;
    padding-right: 3px;
    scrollbar-width: thin;
    scrollbar-color: var(--sidebar-line) transparent;
  }

  .overview {
    margin-bottom: 17px;
  }

  .group {
    margin-bottom: 14px;
  }

  .group-label {
    padding: 0 11px 6px;
    color: var(--sidebar-muted);
    font-size: 9px;
    font-weight: 800;
    letter-spacing: 0.14em;
    text-transform: uppercase;
  }

  .nav-item {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 37px;
    margin: 1px 0;
    padding: 0 11px;
    border-radius: 9px;
    color: var(--sidebar-muted);
    font-size: 12.5px;
    font-weight: 650;
    transition:
      background 150ms ease,
      color 150ms ease;
  }

  .nav-item:hover {
    background: rgba(255, 255, 255, 0.055);
    color: var(--sidebar-ink);
  }

  .nav-item.active {
    background: rgba(255, 255, 255, 0.09);
    color: #fff;
  }

  .nav-item.active::before {
    content: '';
    position: absolute;
    left: -14px;
    width: 3px;
    height: 18px;
    border-radius: 0 3px 3px 0;
    background: var(--brand);
  }

  .live-dot {
    width: 5px;
    height: 5px;
    margin-left: auto;
    border-radius: 50%;
    background: var(--brand);
    box-shadow: 0 0 0 3px rgba(217, 84, 45, 0.18);
  }

  .sidebar-foot {
    padding-top: 14px;
    border-top: 1px solid var(--sidebar-line);
  }

  .workspace {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 8px;
  }

  .workspace-icon {
    display: grid;
    place-items: center;
    width: 31px;
    height: 31px;
    border: 1px solid var(--sidebar-line);
    border-radius: 8px;
    background: var(--sidebar-raised);
    color: var(--sidebar-muted);
  }

  .workspace p {
    max-width: 160px;
    overflow: hidden;
    color: var(--sidebar-ink);
    font-size: 11.5px;
    font-weight: 750;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .workspace span {
    display: block;
    margin-top: 1px;
    color: var(--sidebar-muted);
    font-size: 10px;
  }

  .trust {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 9px 9px 1px;
    color: var(--sidebar-muted);
    font-size: 9.5px;
  }
</style>

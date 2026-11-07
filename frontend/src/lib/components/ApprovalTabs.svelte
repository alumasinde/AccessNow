<script lang="ts">
  import { page } from '$app/state';
  import Icon from './Icon.svelte';

  const tabs = [
    { label: 'Inbox', href: '/approvals', icon: 'bell' as const },
    { label: 'Chain register', href: '/approvals/register', icon: 'list' as const },
    { label: 'Flow rules', href: '/approvals/flows', icon: 'scale' as const },
    { label: 'Delegations', href: '/approvals/delegations', icon: 'users' as const }
  ];

  const path = $derived(page.url.pathname);

  function active(href: string): boolean {
    if (href === '/approvals') {
      return path === '/approvals' || /^\/approvals\/\d+$/.test(path);
    }
    return path === href || path.startsWith(href + '/');
  }
</script>

<nav class="approval-tabs" aria-label="Approval workspace">
  {#each tabs as tab}
    <a href={tab.href} class:active={active(tab.href)}>
      <Icon name={tab.icon} size="0.85em" />
      {tab.label}
    </a>
  {/each}
</nav>

<style>
  .approval-tabs {
    display: flex;
    gap: 3px;
    margin: -8px 0 24px;
    padding: 4px;
    width: fit-content;
    max-width: 100%;
    overflow-x: auto;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface-muted);
    scrollbar-width: none;
  }

  .approval-tabs::-webkit-scrollbar {
    display: none;
  }

  a {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    min-height: 34px;
    padding: 0 12px;
    border: 1px solid transparent;
    border-radius: 8px;
    color: var(--ink-faint);
    font-size: 11px;
    font-weight: 750;
    white-space: nowrap;
  }

  a:hover {
    color: var(--ink);
  }

  a.active {
    border-color: var(--line);
    background: var(--surface);
    color: var(--ink);
    box-shadow: var(--shadow-sm);
  }

  @media (max-width: 640px) {
    .approval-tabs {
      width: 100%;
    }

    a {
      padding: 0 10px;
      font-size: 10.5px;
    }
  }
</style>

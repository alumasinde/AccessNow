<script lang="ts">
  import { goto } from '$app/navigation';
  import { createQuery } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { Visit } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { navigation } from '$lib/nav';
  import { formatDateTime, formatWindow, initials, statusMeta } from '$lib/format';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import DataTable from '$lib/components/DataTable.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const canSeeVisits = $derived(session.can('visit.view') && session.moduleOn('visitor_management'));

  const visitsQuery = createQuery(() => ({
    queryKey: ['visits', 'overview'],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/visits', {
        params: { query: { limit: 100 } }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    enabled: canSeeVisits
  }));

  const visits = $derived(visitsQuery.data?.items ?? []);
  const recent = $derived(visits.slice(0, 5));
  const visitorName = $derived(session.user ? `${session.user.first_name} ${session.user.last_name}` : 'there');

  function count(status: string): number {
    return visits.filter((visit) => visit.status === status).length;
  }

  const metrics = $derived([
    { label: 'Waiting decision', value: count('pending'), icon: 'clock' as const, tone: 'amber' },
    { label: 'Approved to arrive', value: count('approved'), icon: 'circleCheck' as const, tone: 'teal' },
    { label: 'On site now', value: count('on_site'), icon: 'doorOpen' as const, tone: 'success' },
    { label: 'Completed', value: count('completed'), icon: 'check' as const, tone: 'neutral' }
  ]);

  const enabledModules = $derived(
    navigation.flatMap((group) => group.items).filter((item) => session.moduleOn(item.module) && session.can(item.permission))
  );

  const capabilities = $derived([
    { label: 'Create visits', on: session.can('visit.create') },
    { label: 'Approve or reject', on: session.can('visit.approve') },
    { label: 'Check visitors in and out', on: session.can('visit.update') },
    { label: 'Cancel a visit', on: session.can('visit.cancel') }
  ]);

  const hour = new Date().getHours();
  const greeting = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';

  const columns = [
    { label: 'Visitor', width: '25%' },
    { label: 'Purpose', hideOn: 'tablet' as const },
    { label: 'Window', hideOn: 'mobile' as const },
    { label: 'Status', align: 'right' as const }
  ];

  function openVisit(visit: Visit) {
    goto(`/visitors/${visit.id}`);
  }
</script>

{#snippet cells(visit: Visit)}
  <td>
    <div class="visitor-cell">
      <span class="mini-avatar">{initials(visit.visitor_name)}</span>
      <div>
        <strong>{visit.visitor_name}</strong>
        <small>{visit.host_name ? `Host · ${visit.host_name}` : visit.pass_no}</small>
      </div>
    </div>
  </td>
  <td class="hide-tablet purpose">{visit.purpose}</td>
  <td class="hide-mobile mono">{formatWindow(visit.expected_from, visit.expected_until, session.tenant?.timezone)}</td>
  <td class="right"><Badge tone={statusMeta(visit.status).tone} dot>{statusMeta(visit.status).label}</Badge></td>
{/snippet}

<svelte:head><title>Overview · AccessNow</title></svelte:head>

<div class="page">
  <PageHeader
    eyebrow={new Intl.DateTimeFormat('en-GB', { weekday: 'long', day: '2-digit', month: 'long' }).format(new Date())}
    title="{greeting}, {visitorName.split(' ')[0]}."
    description={canSeeVisits
      ? 'A live read of the visitor register, shaped by what your account is allowed to see.'
      : 'Your workspace at a glance, shaped by the modules and permissions in your session.'}
  >
    {#snippet actions()}
      {#if session.can('visit.create') && session.moduleOn('visitor_management')}
        <Button href="/visitors/new" icon="plus">Register a visit</Button>
      {/if}
      {#if canSeeVisits}
        <Button variant="secondary" href="/visitors" trailingIcon="arrowRight">Open register</Button>
      {/if}
    {/snippet}
  </PageHeader>

  {#if canSeeVisits}
    <section class="metrics" aria-label="Visitor register summary">
      {#each metrics as metric, index}
        <article class="metric" style="--delay: {index * 55}ms">
          <div class="metric-top">
            <span class="metric-icon {metric.tone}"><Icon name={metric.icon} size="0.95em" /></span>
            <span class="metric-label">{metric.label}</span>
          </div>
          {#if visitsQuery.isPending}
            <span class="skeleton number-skeleton"></span>
          {:else}
            <strong class="metric-value mono">{metric.value}</strong>
          {/if}
          <span class="metric-foot">in the current register read</span>
        </article>
      {/each}
    </section>

    <div class="dashboard-grid">
      <Card title="Recent visitor movement" eyebrow="Live register" padded={false} class="recent-card">
        {#snippet action()}
          <Button href="/visitors" variant="ghost" size="sm" trailingIcon="arrowRight">View all</Button>
        {/snippet}
        {#if visitsQuery.isPending}
          <div class="table-skeleton">
            {#each Array(4) as _}
              <div><span class="skeleton avatar-skeleton"></span><span class="skeleton line-skeleton"></span></div>
            {/each}
          </div>
        {:else if visitsQuery.isError}
          <div class="query-error">
            <Icon name="circleExclamation" size="1.1em" />
            <p>{visitsQuery.error?.message ?? 'The register could not be loaded.'}</p>
            <Button variant="secondary" size="sm" onclick={() => visitsQuery.refetch()}>Try again</Button>
          </div>
        {:else if recent.length === 0}
          <div class="empty-register">
            <span><Icon name="users" size="1.3em" /></span>
            <h3>The register is clear</h3>
            <p>No visits have been registered yet. Start with the person at the desk.</p>
            {#if session.can('visit.create')}
              <Button href="/visitors/new" icon="plus" size="sm">Register a visit</Button>
            {/if}
          </div>
        {:else}
          <DataTable columns={columns} rows={recent} rowKey={(visit) => visit.id} onrowclick={openVisit} cell={cells} />
        {/if}
      </Card>

      <div class="side-stack">
        <Card eyebrow="Access snapshot" title="What your account can do">
          <div class="access-list">
            {#each capabilities as capability}
              <div class:off={!capability.on}>
                <span class="access-icon">
                  <Icon name={capability.on ? 'check' : 'lock'} size="0.72em" />
                </span>
                <span>{capability.label}</span>
              </div>
            {/each}
          </div>
          <div class="readonly-line">
            <Badge tone={session.isReadOnly ? 'amber' : 'neutral'} icon={session.isReadOnly ? 'lock' : 'shield'}>
              {session.isReadOnly ? 'Read-only auditor' : 'Standard permissions'}
            </Badge>
            {#if session.isSuperAdmin}<Badge tone="violet">Super admin</Badge>{/if}
          </div>
        </Card>

        <Card eyebrow="Workspace" title="{enabledModules.length} enabled modules">
          <p class="modules-copy">
            Navigation is generated from these module flags and your permissions, not from a role name.
          </p>
          <div class="module-cloud">
            {#each enabledModules.slice(0, 7) as item}
              <a href={item.href} title={item.description}>
                <Icon name={item.icon} size="0.78em" />
                {item.label}
              </a>
            {/each}
            {#if enabledModules.length > 7}<span class="more">+{enabledModules.length - 7} more</span>{/if}
          </div>
        </Card>
      </div>
    </div>
  {:else}
    <div class="no-visitors">
      <Card>
        <div class="welcome">
          <span class="welcome-icon"><Icon name="shield" size="1.5em" /></span>
          <p class="eyebrow">Session loaded</p>
          <h2>Your workspace is ready.</h2>
          <p>
            The visitor register is not available to this account. The navigation on the left reflects
            the modules and permissions your session actually carries.
          </p>
          <div class="welcome-actions">
            {#each enabledModules.slice(0, 3) as item}
              <Button href={item.href} variant="secondary">{item.label}</Button>
            {/each}
          </div>
        </div>
      </Card>
    </div>
  {/if}
</div>

<style>
  .metrics {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 13px;
    margin-bottom: 18px;
  }

  .metric {
    padding: 17px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
    animation: rise 380ms cubic-bezier(0.2, 0.75, 0.3, 1) both;
    animation-delay: var(--delay);
  }

  .metric-top {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .metric-icon {
    display: grid;
    place-items: center;
    width: 27px;
    height: 27px;
    border-radius: 8px;
    background: var(--surface-muted);
    color: var(--ink-soft);
  }

  .metric-icon.amber {
    background: var(--amber-soft);
    color: var(--amber);
  }

  .metric-icon.teal {
    background: var(--teal-soft);
    color: var(--teal);
  }

  .metric-icon.success {
    background: var(--success-soft);
    color: var(--success);
  }

  .metric-label {
    color: var(--ink-soft);
    font-size: 10.5px;
    font-weight: 750;
  }

  .metric-value {
    display: block;
    margin-top: 12px;
    font-size: 34px;
    font-weight: 760;
    letter-spacing: -0.055em;
    line-height: 1;
  }

  .number-skeleton {
    display: block;
    width: 54px;
    height: 33px;
    margin-top: 12px;
  }

  .metric-foot {
    display: block;
    margin-top: 7px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .dashboard-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.55fr) minmax(300px, 0.75fr);
    gap: 18px;
    align-items: start;
  }

  .side-stack {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .recent-card {
    overflow: hidden;
  }

  .recent-card :global(table) {
    min-width: 560px;
  }

  .visitor-cell {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .mini-avatar {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    flex: 0 0 auto;
    border-radius: 9px;
    background: var(--brand-soft);
    color: var(--brand-strong);
    font-size: 9px;
    font-weight: 850;
  }

  .visitor-cell strong {
    display: block;
    font-size: 12px;
    font-weight: 800;
  }

  .visitor-cell small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 10px;
  }

  .purpose {
    max-width: 220px;
    overflow: hidden;
    color: var(--ink-soft);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .right {
    text-align: right;
  }

  .table-skeleton {
    padding: 12px 18px 18px;
  }

  .table-skeleton > div {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 0;
    border-bottom: 1px solid var(--line);
  }

  .avatar-skeleton {
    width: 32px;
    height: 32px;
    border-radius: 9px;
  }

  .line-skeleton {
    width: 42%;
    height: 11px;
  }

  .query-error,
  .empty-register {
    display: flex;
    align-items: center;
    flex-direction: column;
    padding: 42px 24px;
    text-align: center;
  }

  .query-error {
    gap: 10px;
    color: var(--danger);
    font-size: 12px;
  }

  .query-error p {
    color: var(--ink-soft);
  }

  .empty-register > span {
    display: grid;
    place-items: center;
    width: 46px;
    height: 46px;
    margin-bottom: 12px;
    border-radius: 13px;
    background: var(--surface-muted);
    color: var(--ink-faint);
  }

  .empty-register h3 {
    font-size: 15px;
  }

  .empty-register p {
    max-width: 300px;
    margin: 6px 0 15px;
    color: var(--ink-soft);
    font-size: 11.5px;
  }

  .access-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .access-list > div {
    display: flex;
    align-items: center;
    gap: 9px;
    color: var(--ink-soft);
    font-size: 11.5px;
    font-weight: 650;
  }

  .access-list > div.off {
    color: var(--ink-faint);
    opacity: 0.7;
  }

  .access-icon {
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    border-radius: 6px;
    background: var(--success-soft);
    color: var(--success);
  }

  .off .access-icon {
    background: var(--surface-muted);
    color: var(--ink-faint);
  }

  .readonly-line {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px solid var(--line);
  }

  .modules-copy {
    color: var(--ink-soft);
    font-size: 11px;
    line-height: 1.6;
  }

  .module-cloud {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 13px;
  }

  .module-cloud a {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 5px 8px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: var(--surface-muted);
    color: var(--ink-soft);
    font-size: 9.5px;
    font-weight: 700;
  }

  .module-cloud a:hover {
    border-color: var(--line-strong);
    color: var(--ink);
  }

  .more {
    padding: 5px 3px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .no-visitors {
    max-width: 700px;
  }

  .welcome {
    max-width: 570px;
    padding: 17px 5px;
  }

  .welcome-icon {
    display: grid;
    place-items: center;
    width: 52px;
    height: 52px;
    margin-bottom: 18px;
    border-radius: 15px;
    background: var(--teal-soft);
    color: var(--teal);
  }

  .welcome h2 {
    margin-top: 7px;
    font-size: 27px;
  }

  .welcome > p:not(.eyebrow) {
    max-width: 520px;
    margin-top: 10px;
    color: var(--ink-soft);
    font-size: 12.5px;
    line-height: 1.7;
  }

  .welcome-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 20px;
  }

  @media (max-width: 1180px) {
    .dashboard-grid {
      grid-template-columns: 1fr;
    }

    .side-stack {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 900px) {
    .metrics {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 640px) {
    .side-stack {
      grid-template-columns: 1fr;
    }

    .metrics {
      gap: 9px;
    }

    .metric {
      padding: 14px;
    }

    .metric-value {
      font-size: 29px;
    }
  }

  .hide-tablet,
  .hide-mobile {
    display: table-cell;
  }

  @media (max-width: 760px) {
    .hide-tablet {
      display: none;
    }
  }

  @media (max-width: 560px) {
    .hide-mobile {
      display: none;
    }
  }
</style>

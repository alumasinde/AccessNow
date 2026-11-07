<script lang="ts">
  import { goto } from '$app/navigation';
  import { createQuery } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { Visit } from '$lib/api/types';
  import { VISIT_STATUSES } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { formatWindow, initials, statusMeta, titleCase } from '$lib/format';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import DataTable from '$lib/components/DataTable.svelte';
  import Pagination from '$lib/components/Pagination.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const allowed = $derived(session.can('visit.view'));
  const moduleOn = $derived(session.moduleOn('visitor_management'));

  let status = $state('');
  let cursor = $state<string | undefined>(undefined);
  let history = $state<(string | undefined)[]>([]);
  let page = $state(1);

  const visitsQuery = createQuery(() => ({
    queryKey: ['visits', 'list', status, cursor ?? 'first'],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/visits', {
        params: {
          query: {
            limit: 25,
            ...(status ? { status } : {}),
            ...(cursor ? { cursor } : {})
          }
        }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    enabled: allowed && moduleOn
  }));

  const items = $derived(visitsQuery.data?.items ?? []);

  function changeStatus(next: string) {
    status = next;
    cursor = undefined;
    history = [];
    page = 1;
  }

  function nextPage() {
    if (!visitsQuery.data?.next_cursor) return;
    history = [...history, cursor];
    cursor = visitsQuery.data.next_cursor;
    page += 1;
  }

  function previousPage() {
    if (history.length === 0) return;
    const copy = [...history];
    cursor = copy.pop();
    history = copy;
    page -= 1;
  }

  function openVisit(visit: Visit) {
    goto(`/visitors/${visit.id}`);
  }

  const columns = [
    { label: 'Visitor', width: '24%' },
    { label: 'Purpose', hideOn: 'tablet' as const },
    { label: 'Host', hideOn: 'tablet' as const },
    { label: 'Window', hideOn: 'mobile' as const },
    { label: 'Status', align: 'right' as const }
  ];
</script>

{#snippet cells(visit: Visit)}
  <td>
    <div class="person">
      <span class="avatar">{initials(visit.visitor_name)}</span>
      <div>
        <strong>{visit.visitor_name}</strong>
        <small>{visit.pass_no} · {visit.people_count} {visit.people_count === 1 ? 'person' : 'people'}</small>
      </div>
    </div>
  </td>
  <td class="purpose hide-tablet">{visit.purpose}</td>
  <td class="host hide-tablet">{#if visit.host_name}{visit.host_name}{:else}<span class="faint">Unassigned</span>{/if}</td>
  <td class="mono window hide-mobile">{formatWindow(visit.expected_from, visit.expected_until, session.tenant?.timezone)}</td>
  <td class="align-right">
    <Badge tone={statusMeta(visit.status).tone} dot>{statusMeta(visit.status).label}</Badge>
  </td>
{/snippet}

<svelte:head><title>Visitors · AccessNow</title></svelte:head>

<div class="page">
  {#if !allowed}
    <AccessDenied moduleOff={!moduleOn} />
  {:else}
    <PageHeader
      eyebrow="Visitor management"
      title="Visitor register"
      description="Every person expected, on site or recently departed. Open a record to make a decision or move them through the gate."
    >
      {#snippet actions()}
        <Button
          variant="secondary"
          icon="refresh"
          loading={visitsQuery.isFetching}
          onclick={() => visitsQuery.refetch()}
        >
          Refresh
        </Button>
        {#if session.can('visit.create')}
          <Button href="/visitors/new" icon="plus">Register visit</Button>
        {/if}
      {/snippet}
    </PageHeader>

    <Card padded={false} class="register">
      <div class="toolbar">
        <div class="filters" role="group" aria-label="Filter by status">
          <button class:active={status === ''} type="button" onclick={() => changeStatus('')}>All</button>
          {#each VISIT_STATUSES as option}
            <button class:active={status === option} type="button" onclick={() => changeStatus(option)}>
              {titleCase(option)}
            </button>
          {/each}
        </div>
        <div class="count">
          <Icon name="filter" size="0.78em" />
          <span>{status ? titleCase(status) : 'All statuses'}</span>
        </div>
      </div>

      {#if visitsQuery.isPending}
        <div class="loading-table">
          {#each Array(6) as _}
            <div class="loading-row">
              <span class="skeleton avatar-skeleton"></span>
              <span class="skeleton line-skeleton"></span>
              <span class="skeleton pill-skeleton"></span>
            </div>
          {/each}
        </div>
      {:else if visitsQuery.isError}
        <div class="error-state">
          <span><Icon name="circleExclamation" size="1.3em" /></span>
          <h3>The register could not be loaded</h3>
          <p>{visitsQuery.error?.message ?? 'Try again in a moment.'}</p>
          <Button variant="secondary" onclick={() => visitsQuery.refetch()}>Try again</Button>
        </div>
      {:else if items.length === 0}
        <EmptyState
          icon="users"
          title={status ? `No ${status.replaceAll('_', ' ')} visits` : 'The register is clear'}
          description={status
            ? 'There are no visits in this status right now. Try another filter.'
            : 'No visits have been registered yet. Start with the person at the desk.'}
        >
          {#snippet actions()}
            {#if status}
              <Button variant="secondary" onclick={() => changeStatus('')}>Show all visits</Button>
            {:else if session.can('visit.create')}
              <Button href="/visitors/new" icon="plus">Register a visit</Button>
            {/if}
          {/snippet}
        </EmptyState>
      {:else}
        <DataTable columns={columns} rows={items} rowKey={(visit) => visit.id} onrowclick={openVisit} cell={cells} />
        <Pagination
          {page}
          hasMore={Boolean(visitsQuery.data?.has_more)}
          count={items.length}
          loading={visitsQuery.isFetching}
          onNext={nextPage}
          onPrev={previousPage}
        />
      {/if}
    </Card>
  {/if}
</div>

<style>
  :global(.register) {
    overflow: hidden;
  }

  .toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    padding: 14px 17px;
    border-bottom: 1px solid var(--line);
    background: var(--surface-muted);
  }

  .filters {
    display: flex;
    gap: 3px;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .filters::-webkit-scrollbar {
    display: none;
  }

  .filters button {
    min-height: 30px;
    padding: 0 10px;
    border: 1px solid transparent;
    border-radius: 7px;
    background: transparent;
    color: var(--ink-faint);
    font-size: 10.5px;
    font-weight: 750;
    white-space: nowrap;
  }

  .filters button:hover {
    color: var(--ink);
  }

  .filters button.active {
    border-color: var(--line);
    background: var(--surface);
    color: var(--ink);
    box-shadow: var(--shadow-sm);
  }

  .count {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 0 0 auto;
    color: var(--ink-faint);
    font-size: 9.5px;
    font-weight: 700;
  }

  .person {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .avatar {
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    flex: 0 0 auto;
    border-radius: 9px;
    background: var(--brand-soft);
    color: var(--brand-strong);
    font-size: 9.5px;
    font-weight: 850;
  }

  .person strong {
    display: block;
    font-size: 12px;
    font-weight: 800;
  }

  .person small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .purpose {
    max-width: 210px;
    overflow: hidden;
    color: var(--ink-soft);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .host {
    max-width: 150px;
    overflow: hidden;
    color: var(--ink-soft);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .window {
    color: var(--ink-soft);
    font-size: 10.5px;
  }

  .align-right {
    text-align: right;
  }

  .loading-table {
    padding: 8px 17px 17px;
  }

  .loading-row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 13px 0;
    border-bottom: 1px solid var(--line);
  }

  .avatar-skeleton {
    width: 34px;
    height: 34px;
    border-radius: 9px;
  }

  .line-skeleton {
    width: 38%;
    height: 12px;
  }

  .pill-skeleton {
    width: 74px;
    height: 22px;
    margin-left: auto;
    border-radius: 999px;
  }

  .error-state {
    display: flex;
    align-items: center;
    flex-direction: column;
    padding: 54px 24px;
    text-align: center;
  }

  .error-state > span {
    display: grid;
    place-items: center;
    width: 48px;
    height: 48px;
    margin-bottom: 13px;
    border-radius: 14px;
    background: var(--danger-soft);
    color: var(--danger);
  }

  .error-state h3 {
    font-size: 16px;
  }

  .error-state p {
    max-width: 380px;
    margin: 6px 0 16px;
    color: var(--ink-soft);
    font-size: 11.5px;
  }

  .hide-tablet,
  .hide-mobile {
    display: table-cell;
  }

  @media (max-width: 760px) {
    .hide-tablet {
      display: none;
    }

    .toolbar {
      align-items: flex-start;
      flex-direction: column;
    }

    .count {
      display: none;
    }
  }

  @media (max-width: 560px) {
    .hide-mobile {
      display: none;
    }

    .filters {
      width: 100%;
    }
  }
</style>

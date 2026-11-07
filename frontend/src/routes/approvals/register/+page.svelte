<script lang="ts">
  import { goto } from '$app/navigation';
  import { createQuery } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { ApprovalChainSummary } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { formatDateTime, titleCase } from '$lib/format';
  import { approvalStatusMeta, formatValue, SOURCE_MODULES, sourceModuleLabel } from '$lib/approval';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import DataTable from '$lib/components/DataTable.svelte';
  import Pagination from '$lib/components/Pagination.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ApprovalTabs from '$lib/components/ApprovalTabs.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const allowed = $derived(session.can('approval.view') && session.moduleOn('approval_workflow'));

  let status = $state('');
  let module = $state('');
  let openOnly = $state(false);
  let cursor = $state<string | undefined>(undefined);
  let history = $state<(string | undefined)[]>([]);
  let page = $state(1);

  const chainsQuery = createQuery(() => ({
    queryKey: ['approvals', 'register', status, module, openOnly, cursor ?? 'first'],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/approvals', {
        params: {
          query: {
            limit: 25,
            ...(status ? { status } : {}),
            ...(module ? { module } : {}),
            ...(openOnly ? { open: true } : {}),
            ...(cursor ? { cursor } : {})
          }
        }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    enabled: allowed
  }));

  const items = $derived(chainsQuery.data?.items ?? []);

  function resetPaging() {
    cursor = undefined;
    history = [];
    page = 1;
  }

  function nextPage() {
    if (!chainsQuery.data?.next_cursor) return;
    history = [...history, cursor];
    cursor = chainsQuery.data.next_cursor;
    page += 1;
  }

  function previousPage() {
    if (history.length === 0) return;
    const copy = [...history];
    cursor = copy.pop();
    history = copy;
    page -= 1;
  }

  function openChain(chain: ApprovalChainSummary) {
    goto(`/approvals/${chain.id}`);
  }

  const columns = [
    { label: 'Chain', width: '16%' },
    { label: 'Pass', width: '16%' },
    { label: 'Progress', hideOn: 'tablet' as const },
    { label: 'Started', hideOn: 'tablet' as const },
    { label: 'Deadline', hideOn: 'mobile' as const },
    { label: 'Status', align: 'right' as const }
  ];
</script>

{#snippet cells(chain: ApprovalChainSummary)}
  <td>
    <div class="chain-cell">
      <span class="chain-icon"><Icon name="stamp" size="0.85em" /></span>
      <div><strong>Chain #{chain.id}</strong><small>{chain.approval_flow_id ? `Flow #${chain.approval_flow_id}` : 'Dynamic route'}</small></div>
    </div>
  </td>
  <td>
    <strong class="pass-no">#{chain.pass_id}</strong>
    <small class="value">{formatValue(chain.value_amount, chain.currency)}</small>
  </td>
  <td class="hide-tablet">
    <div class="progress-cell">
      <div class="progress-track"><span style="width: {chain.total_steps ? Math.min(100, (chain.current_step_no / chain.total_steps) * 100) : 0}%"></span></div>
      <small>Step {chain.current_step_no} of {chain.total_steps}</small>
    </div>
  </td>
  <td class="date-cell hide-tablet">
    <span>{formatDateTime(chain.started_at, session.tenant?.timezone)}</span>
    <small>{chain.escalation_count > 0 ? `${chain.escalation_count} escalation${chain.escalation_count === 1 ? '' : 's'}` : 'No escalations'}</small>
  </td>
  <td class="date-cell hide-mobile">
    {#if chain.deadline_at}
      <span class:late={Boolean(chain.is_open && new Date(chain.deadline_at) < new Date())}>{formatDateTime(chain.deadline_at, session.tenant?.timezone)}</span>
      <small>{chain.is_open ? 'Live deadline' : 'Closed'}</small>
    {:else}
      <span class="faint">No deadline</span>
    {/if}
  </td>
  <td class="right">
    <Badge tone={approvalStatusMeta(chain.status).tone} icon={approvalStatusMeta(chain.status).icon}>
      {approvalStatusMeta(chain.status).label}
    </Badge>
  </td>
{/snippet}

<svelte:head><title>Chain register · AccessNow</title></svelte:head>

<div class="page">
  {#if !allowed}
    <AccessDenied moduleOff={!session.moduleOn('approval_workflow')} />
  {:else}
    <PageHeader
      eyebrow="Approval workflow"
      title="Chain register"
      description="Every approval story in this workspace: what is moving, what is late, and what has already been decided."
    >
      {#snippet actions()}
        <Button variant="secondary" href="/approvals" icon="bell">My inbox</Button>
        <Button
          variant="secondary"
          icon="refresh"
          loading={chainsQuery.isFetching}
          onclick={() => chainsQuery.refetch()}
        >
          Refresh
        </Button>
      {/snippet}
    </PageHeader>

    <ApprovalTabs />

    <Card padded={false} class="register-card">
      <div class="filters">
        <div class="filter-group">
          <label for="status-filter">State</label>
          <select id="status-filter" class="filter-select" bind:value={status} onchange={resetPaging}>
            <option value="">All states</option>
            {#each ['not_required', 'pending', 'in_progress', 'approved', 'rejected', 'returned', 'cancelled', 'expired', 'escalated'] as option}
              <option value={option}>{titleCase(option)}</option>
            {/each}
          </select>
        </div>
        <div class="filter-group">
          <label for="module-filter">Source</label>
          <select id="module-filter" class="filter-select" bind:value={module} onchange={resetPaging}>
            <option value="">All sources</option>
            {#each SOURCE_MODULES as option}
              <option value={option.key}>{option.label}</option>
            {/each}
          </select>
        </div>
        <label class="check-filter">
          <input type="checkbox" bind:checked={openOnly} onchange={resetPaging} />
          <span class="checkmark"><Icon name="check" size="0.65em" /></span>
          <span>Open only</span>
        </label>
        {#if status || module || openOnly}
          <button
            class="clear"
            type="button"
            onclick={() => { status = ''; module = ''; openOnly = false; resetPaging(); }}
          >
            Clear filters
          </button>
        {/if}
        <span class="filter-note"><Icon name="filter" size="0.72em" /> {items.length} on this page</span>
      </div>

      {#if chainsQuery.isPending}
        <div class="loading">
          {#each Array(6) as _}
            <div><span class="skeleton dot-skeleton"></span><span class="skeleton line-skeleton"></span><span class="skeleton pill-skeleton"></span></div>
          {/each}
        </div>
      {:else if chainsQuery.isError}
        <div class="error-state">
          <span><Icon name="circleExclamation" size="1.3em" /></span>
          <h3>The register could not be loaded</h3>
          <p>{chainsQuery.error?.message ?? 'Try again in a moment.'}</p>
          <Button variant="secondary" onclick={() => chainsQuery.refetch()}>Try again</Button>
        </div>
      {:else if items.length === 0}
        <EmptyState
          icon="stamp"
          title="No chains match those filters"
          description="Try a different state or source, or clear the filters to read the whole register."
        >
          {#snippet actions()}
            <Button variant="secondary" onclick={() => { status = ''; module = ''; openOnly = false; resetPaging(); }}>Clear filters</Button>
          {/snippet}
        </EmptyState>
      {:else}
        <DataTable columns={columns} rows={items} rowKey={(chain) => chain.id} onrowclick={openChain} cell={cells} />
        <Pagination
          {page}
          hasMore={Boolean(chainsQuery.data?.has_more)}
          count={items.length}
          loading={chainsQuery.isFetching}
          onNext={nextPage}
          onPrev={previousPage}
        />
      {/if}
    </Card>
  {/if}
</div>

<style>
  :global(.register-card) {
    overflow: hidden;
  }

  .filters {
    display: flex;
    align-items: flex-end;
    gap: 12px;
    padding: 14px 17px;
    border-bottom: 1px solid var(--line);
    background: var(--surface-muted);
  }

  .filter-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .filter-group label {
    color: var(--ink-faint);
    font-size: 8.5px;
    font-weight: 800;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .filter-select {
    min-width: 140px;
    height: 34px;
    padding: 0 30px 0 10px;
    border: 1px solid var(--line-strong);
    border-radius: 8px;
    appearance: none;
    background: var(--surface)
      linear-gradient(45deg, transparent 50%, var(--ink-soft) 50%) calc(100% - 14px) 14px / 4px 4px no-repeat,
      linear-gradient(135deg, var(--ink-soft) 50%, transparent 50%) calc(100% - 10px) 14px / 4px 4px no-repeat;
    color: var(--ink);
    font-size: 11px;
    font-weight: 650;
  }

  .check-filter {
    display: flex;
    align-items: center;
    gap: 7px;
    height: 34px;
    padding: 0 10px;
    border: 1px solid var(--line-strong);
    border-radius: 8px;
    background: var(--surface);
    color: var(--ink-soft);
    font-size: 10.5px;
    font-weight: 700;
    cursor: pointer;
  }

  .check-filter input {
    position: absolute;
    opacity: 0;
  }

  .checkmark {
    display: grid;
    place-items: center;
    width: 15px;
    height: 15px;
    border: 1px solid var(--line-strong);
    border-radius: 4px;
    color: transparent;
  }

  .check-filter input:checked + .checkmark {
    border-color: var(--teal);
    background: var(--teal);
    color: #fff;
  }

  .clear {
    height: 34px;
    padding: 0 10px;
    border: 0;
    background: transparent;
    color: var(--brand-strong);
    font-size: 10px;
    font-weight: 750;
  }

  .filter-note {
    display: flex;
    align-items: center;
    gap: 5px;
    margin-left: auto;
    padding-bottom: 9px;
    color: var(--ink-faint);
    font-size: 9.5px;
    font-weight: 700;
    white-space: nowrap;
  }

  .chain-cell {
    display: flex;
    align-items: center;
    gap: 9px;
  }

  .chain-icon {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    flex: 0 0 auto;
    border-radius: 9px;
    background: var(--brand-soft);
    color: var(--brand-strong);
  }

  .chain-cell strong,
  .pass-no,
  .date-cell span {
    display: block;
    color: var(--ink);
    font-size: 11px;
    font-weight: 800;
  }

  .chain-cell small,
  .value,
  .date-cell small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .pass-no {
    font-variant-numeric: tabular-nums;
  }

  .value {
    max-width: 150px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .progress-cell {
    min-width: 105px;
  }

  .progress-track {
    width: 100%;
    height: 4px;
    overflow: hidden;
    border-radius: 999px;
    background: var(--line);
  }

  .progress-track span {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: var(--teal);
  }

  .progress-cell small {
    display: block;
    margin-top: 4px;
    color: var(--ink-faint);
    font-size: 9px;
  }

  .late {
    color: var(--danger) !important;
  }

  .right {
    text-align: right;
  }

  .loading {
    padding: 8px 17px 17px;
  }

  .loading > div {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 13px 0;
    border-bottom: 1px solid var(--line);
  }

  .dot-skeleton {
    width: 32px;
    height: 32px;
    border-radius: 9px;
  }

  .line-skeleton {
    width: 38%;
    height: 12px;
  }

  .pill-skeleton {
    width: 72px;
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
    margin: 6px 0 16px;
    color: var(--ink-soft);
    font-size: 11.5px;
  }

  @media (max-width: 860px) {
    .filters {
      flex-wrap: wrap;
      align-items: center;
    }

    .filter-note {
      margin-left: 0;
      padding-bottom: 0;
    }
  }

  @media (max-width: 560px) {
    .filter-group {
      flex: 1;
    }

    .filter-select {
      width: 100%;
      min-width: 0;
    }

    .check-filter,
    .clear {
      height: 32px;
    }

    .filter-note {
      display: none;
    }
  }
</style>

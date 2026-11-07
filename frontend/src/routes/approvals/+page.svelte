<script lang="ts">
  import { goto } from '$app/navigation';
  import { createQuery } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { ApprovalInboxItem } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { formatDateTime, relativeTime, titleCase } from '$lib/format';
  import { modeLabel, sourceModuleLabel } from '$lib/approval';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import DataTable from '$lib/components/DataTable.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ApprovalTabs from '$lib/components/ApprovalTabs.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const allowed = $derived(session.can('approval.view') && session.moduleOn('approval_workflow'));

  const inboxQuery = createQuery(() => ({
    queryKey: ['approvals', 'inbox'],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/approvals/inbox', {
        params: { query: { limit: 100 } }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    enabled: allowed
  }));

  const items = $derived(inboxQuery.data?.items ?? []);
  const overdue = $derived(items.filter((item) => item.is_overdue).length);
  const escalated = $derived(items.filter((item) => item.escalated).length);
  const dueSoon = $derived(
    items.filter((item) => {
      if (!item.due_at) return false;
      const hours = (new Date(item.due_at).getTime() - Date.now()) / 3_600_000;
      return hours >= 0 && hours <= 24;
    }).length
  );

  function open(item: ApprovalInboxItem) {
    goto(`/approvals/${item.approval_request_id}`);
  }

  const columns = [
    { label: 'Request', width: '28%' },
    { label: 'From', hideOn: 'tablet' as const },
    { label: 'Step', hideOn: 'tablet' as const },
    { label: 'Due', hideOn: 'mobile' as const },
    { label: 'State', align: 'right' as const }
  ];
</script>

{#snippet cells(item: ApprovalInboxItem)}
  <td>
    <div class="request-cell">
      <span class="request-icon"><Icon name="stamp" size="0.9em" /></span>
      <div>
        <strong>{item.subject_name ?? item.pass_no}</strong>
        <small>{item.pass_no} · {sourceModuleLabel(item.source_module_key)}</small>
      </div>
    </div>
  </td>
  <td class="from-cell hide-tablet">
    <strong>{item.requester_name ?? 'Unknown requester'}</strong>
    <small>{titleCase(item.pass_type_key)}</small>
  </td>
  <td class="step-cell hide-tablet">
    <strong>{item.step_name ?? `Step ${item.step_no}`}</strong>
    <small>Step {item.step_no} of {item.total_steps} · {modeLabel(item.mode)}</small>
  </td>
  <td class="due-cell hide-mobile">
    {#if item.due_at}
      <strong class:overdue={item.is_overdue}>{formatDateTime(item.due_at, session.tenant?.timezone)}</strong>
      <small>{relativeTime(item.due_at)}</small>
    {:else}
      <span class="faint">No deadline</span>
    {/if}
  </td>
  <td class="right">
    {#if item.is_overdue}
      <Badge tone="danger" icon="clock">Overdue</Badge>
    {:else if item.escalated}
      <Badge tone="violet" icon="arrowRight">Escalated</Badge>
    {:else}
      <Badge tone="amber" dot>Your turn</Badge>
    {/if}
  </td>
{/snippet}

<svelte:head><title>Approval inbox · AccessNow</title></svelte:head>

<div class="page">
  {#if !allowed}
    <AccessDenied moduleOff={!session.moduleOn('approval_workflow')} />
  {:else}
    <PageHeader
      eyebrow="Approval workflow"
      title="Approval inbox"
      description="The asks addressed to you by name or to a role you hold today. An empty inbox is a good answer."
    >
      {#snippet actions()}
        <Button variant="secondary" href="/approvals/register" trailingIcon="arrowRight">Open register</Button>
        <Button
          variant="secondary"
          icon="refresh"
          loading={inboxQuery.isFetching}
          onclick={() => inboxQuery.refetch()}
        >
          Refresh
        </Button>
      {/snippet}
    </PageHeader>

    <ApprovalTabs />

    <section class="summary" aria-label="Inbox summary">
      <div>
        <span class="summary-icon brand"><Icon name="bell" size="0.9em" /></span>
        <p><strong class="mono">{items.length}</strong><span>waiting on you</span></p>
      </div>
      <div>
        <span class="summary-icon danger"><Icon name="clock" size="0.9em" /></span>
        <p><strong class="mono">{overdue}</strong><span>overdue</span></p>
      </div>
      <div>
        <span class="summary-icon amber"><Icon name="clock" size="0.9em" /></span>
        <p><strong class="mono">{dueSoon}</strong><span>due within 24h</span></p>
      </div>
      <div>
        <span class="summary-icon violet"><Icon name="arrowRight" size="0.9em" /></span>
        <p><strong class="mono">{escalated}</strong><span>escalated</span></p>
      </div>
    </section>

    <Card padded={false} class="inbox-card">
      <div class="card-bar">
        <div>
          <p class="eyebrow">Your queue</p>
          <h2>Waiting for your answer</h2>
        </div>
        <span class="queue-note"><Icon name="shield" size="0.72em" /> Server checked</span>
      </div>

      {#if inboxQuery.isPending}
        <div class="loading">
          {#each Array(5) as _}
            <div><span class="skeleton avatar-skeleton"></span><span class="skeleton line-skeleton"></span><span class="skeleton pill-skeleton"></span></div>
          {/each}
        </div>
      {:else if inboxQuery.isError}
        <div class="error-state">
          <span><Icon name="circleExclamation" size="1.3em" /></span>
          <h3>The inbox could not be loaded</h3>
          <p>{inboxQuery.error?.message ?? 'Try again in a moment.'}</p>
          <Button variant="secondary" onclick={() => inboxQuery.refetch()}>Try again</Button>
        </div>
      {:else if items.length === 0}
        <EmptyState
          icon="circleCheck"
          title="You are all caught up"
          description="No approval is waiting on you right now. New asks will appear here as chains reach your step."
        >
          {#snippet actions()}
            <Button variant="secondary" href="/approvals/register" trailingIcon="arrowRight">Browse the register</Button>
          {/snippet}
        </EmptyState>
      {:else}
        <DataTable columns={columns} rows={items} rowKey={(item) => item.request_step_id} onrowclick={open} cell={cells} />
      {/if}
    </Card>
  {/if}
</div>

<style>
  .summary {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 10px;
    margin-bottom: 18px;
  }

  .summary > div {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 13px 14px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .summary-icon {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    flex: 0 0 auto;
    border-radius: 9px;
    background: var(--surface-muted);
    color: var(--ink-soft);
  }

  .summary-icon.brand {
    background: var(--brand-soft);
    color: var(--brand-strong);
  }

  .summary-icon.danger {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .summary-icon.amber {
    background: var(--amber-soft);
    color: var(--amber);
  }

  .summary-icon.violet {
    background: var(--violet-soft);
    color: var(--violet);
  }

  .summary p {
    display: flex;
    align-items: baseline;
    gap: 6px;
  }

  .summary strong {
    font-size: 19px;
    font-weight: 780;
    letter-spacing: -0.04em;
  }

  .summary p span {
    color: var(--ink-faint);
    font-size: 9.5px;
    font-weight: 700;
  }

  :global(.inbox-card) {
    overflow: hidden;
  }

  .card-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    padding: 18px 18px 15px;
    border-bottom: 1px solid var(--line);
  }

  .card-bar h2 {
    margin-top: 3px;
    font-size: 16px;
    font-weight: 800;
  }

  .queue-note {
    display: flex;
    align-items: center;
    gap: 5px;
    color: var(--ink-faint);
    font-size: 9.5px;
    font-weight: 700;
  }

  .request-cell {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .request-icon {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    flex: 0 0 auto;
    border-radius: 9px;
    background: var(--brand-soft);
    color: var(--brand-strong);
  }

  .request-cell strong,
  .from-cell strong,
  .step-cell strong,
  .due-cell strong {
    display: block;
    font-size: 11.5px;
    font-weight: 800;
  }

  .request-cell small,
  .from-cell small,
  .step-cell small,
  .due-cell small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .from-cell,
  .step-cell {
    color: var(--ink-soft);
  }

  .due-cell {
    color: var(--ink-soft);
    font-size: 10.5px;
  }

  .overdue {
    color: var(--danger);
  }

  .right {
    text-align: right;
  }

  .loading {
    padding: 8px 18px 18px;
  }

  .loading > div {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 13px 0;
    border-bottom: 1px solid var(--line);
  }

  .avatar-skeleton {
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

  @media (max-width: 900px) {
    .summary {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 560px) {
    .summary {
      gap: 8px;
    }

    .summary > div {
      padding: 11px;
    }

    .summary p {
      flex-direction: column;
      gap: 0;
    }

    .queue-note {
      display: none;
    }
  }
</style>

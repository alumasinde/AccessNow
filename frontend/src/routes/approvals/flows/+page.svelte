<script lang="ts">
  import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { ApprovalFlowView } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { toasts } from '$lib/stores/toast.svelte';
  import { formatDateTime } from '$lib/format';
  import { approverLabel, formatValue, modeLabel } from '$lib/approval';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import Card from '$lib/components/Card.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ApprovalTabs from '$lib/components/ApprovalTabs.svelte';
  import ApprovalFlowModal from '$lib/components/ApprovalFlowModal.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const allowed = $derived(session.can('approval.view') && session.moduleOn('approval_workflow'));
  const canManage = $derived(session.can('approval_flow.manage') && !session.isReadOnly);
  const queryClient = useQueryClient();

  const flowsQuery = createQuery(() => ({
    queryKey: ['approval-flows'],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/approvals/flows');
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    enabled: allowed
  }));

  const flows = $derived(flowsQuery.data?.items ?? []);
  const activeCount = $derived(flows.filter((flow) => flow.is_pickable).length);

  let modalOpen = $state(false);
  let editing = $state<ApprovalFlowView | null>(null);
  let deleting = $state<ApprovalFlowView | null>(null);

  function createFlow() {
    editing = null;
    modalOpen = true;
  }

  function editFlow(flow: ApprovalFlowView) {
    editing = flow;
    modalOpen = true;
  }

  const remove = createMutation(() => ({
    mutationFn: async (flow: ApprovalFlowView) => {
      const { error, response } = await api.DELETE('/api/v1/approvals/flows/{flowID}', {
        params: { path: { flowID: flow.id } }
      });
      if (!response.ok) throw new Error(errorMessage(error, response));
      return flow;
    },
    onSuccess: (flow) => {
      toasts.success('Flow deleted', `${flow.name} no longer routes new passes.`);
      deleting = null;
      queryClient.invalidateQueries({ queryKey: ['approval-flows'] });
    },
    onError: (error: Error) => toasts.error('The flow could not be deleted', error.message)
  }));

  const totalSteps = $derived(flows.reduce((sum, flow) => sum + (flow.steps?.length ?? 0), 0));
</script>

<svelte:head><title>Flow rules · AccessNow</title></svelte:head>

<div class="page">
  {#if !allowed}
    <AccessDenied moduleOff={!session.moduleOn('approval_workflow')} />
  {:else}
    <PageHeader
      eyebrow="Approval workflow"
      title="Flow rules"
      description="The routing rules that decide who signs a pass. New chains freeze their step list the moment a pass arrives."
    >
      {#snippet actions()}
        {#if canManage}
          <Button icon="plus" onclick={createFlow}>New flow</Button>
        {/if}
        <Button variant="secondary" icon="refresh" loading={flowsQuery.isFetching} onclick={() => flowsQuery.refetch()}>Refresh</Button>
      {/snippet}
    </PageHeader>

    <ApprovalTabs />

    <div class="rule-summary">
      <div><span class="summary-icon"><Icon name="scale" size="0.9em" /></span><p><strong class="mono">{flows.length}</strong><span>routing rules</span></p></div>
      <div><span class="summary-icon teal"><Icon name="check" size="0.9em" /></span><p><strong class="mono">{activeCount}</strong><span>pickable now</span></p></div>
      <div><span class="summary-icon violet"><Icon name="list" size="0.9em" /></span><p><strong class="mono">{totalSteps}</strong><span>approval steps</span></p></div>
      <p class="summary-note">Rules are matched by pass type, category, site, department and value. The highest priority match wins; a default rule catches the rest.</p>
    </div>

    {#if flowsQuery.isPending}
      <div class="flow-grid">
        {#each Array(3) as _}
          <div class="flow-skeleton skeleton"></div>
        {/each}
      </div>
    {:else if flowsQuery.isError}
      <Card>
        <div class="error-state">
          <span><Icon name="circleExclamation" size="1.3em" /></span>
          <h3>Flow rules could not be loaded</h3>
          <p>{flowsQuery.error?.message ?? 'Try again in a moment.'}</p>
          <Button variant="secondary" onclick={() => flowsQuery.refetch()}>Try again</Button>
        </div>
      </Card>
    {:else if flows.length === 0}
      <Card>
        <EmptyState
          icon="scale"
          title="No flow rules yet"
          description="Without a rule, a pass can still be decided at the desk, but no approval chain is started automatically."
        >
          {#snippet actions()}
            {#if canManage}<Button icon="plus" onclick={createFlow}>Create the first flow</Button>{/if}
          {/snippet}
        </EmptyState>
      </Card>
    {:else}
      <div class="flow-grid">
        {#each flows as flow (flow.id)}
          <article class="flow-card" class:inactive={flow.status !== 'active'}>
            <div class="flow-head">
              <div class="flow-badges">
                <Badge tone={flow.status === 'active' ? 'success' : 'neutral'} dot>{flow.status === 'active' ? 'Active' : 'Inactive'}</Badge>
                {#if flow.is_default}<Badge tone="violet" icon="shield">Default</Badge>{/if}
                {#if flow.outside_hours_only}<Badge tone="amber" icon="clock">Outside hours</Badge>{/if}
              </div>
              <span class="priority">P{flow.priority}</span>
            </div>

            <div class="flow-title">
              <h2>{flow.name}</h2>
              <p>{flow.description ?? 'No description written for this rule.'}</p>
            </div>

            <div class="conditions">
              {#if flow.pass_type_id}<span><Icon name="idCard" size="0.65em" /> Pass type #{flow.pass_type_id}</span>{:else}<span class="muted">Any pass type</span>{/if}
              {#if flow.category_key}<span><Icon name="filter" size="0.65em" /> {flow.category_key}</span>{/if}
              {#if flow.department_id}<span><Icon name="building" size="0.65em" /> Dept #{flow.department_id}</span>{/if}
              {#if flow.site_id}<span><Icon name="mapPin" size="0.65em" /> Site #{flow.site_id}</span>{/if}
              {#if (flow.min_value !== null && flow.min_value !== undefined) || (flow.max_value !== null && flow.max_value !== undefined)}
                <span><Icon name="scale" size="0.65em" />
                  {flow.min_value !== null && flow.min_value !== undefined ? formatValue(flow.min_value, flow.currency) : 'Any'}
                  –
                  {flow.max_value !== null && flow.max_value !== undefined ? formatValue(flow.max_value, flow.currency) : 'Any'}
                </span>
              {/if}
            </div>

            <div class="steps-preview">
              <p class="preview-label">Chain · {flow.steps?.length ?? 0} steps</p>
              <ol>
                {#each flow.steps ?? [] as step (step.step_no)}
                  <li>
                    <span class="step-no">{step.step_no}</span>
                    <div>
                      <strong>{step.name}</strong>
                      <small>{approverLabel(step.approver_type)}{step.approver_role_key ? `: ${step.approver_role_key.replaceAll('_', ' ')}` : ''} · {modeLabel(step.mode)}</small>
                    </div>
                    {#if step.sla_hours}<span class="sla">{step.sla_hours}h</span>{/if}
                  </li>
                {:else}
                  <li class="no-steps">No steps defined.</li>
                {/each}
              </ol>
            </div>

            <footer>
              <span class="updated">Updated {formatDateTime(flow.updated_at, session.tenant?.timezone)}</span>
              {#if canManage}
                <div class="row-actions">
                  <Button variant="ghost" size="sm" icon="xmark" ariaLabel={`Delete ${flow.name}`} onclick={() => (deleting = flow)} />
                  <Button variant="secondary" size="sm" icon="pencil" onclick={() => editFlow(flow)}>Edit</Button>
                </div>
              {/if}
            </footer>
          </article>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<ApprovalFlowModal
  open={modalOpen}
  flow={editing}
  onclose={() => (modalOpen = false)}
  onsaved={() => queryClient.invalidateQueries({ queryKey: ['approval-flows'] })}
/>

<Modal
  open={deleting !== null}
  title="Delete this flow?"
  description={deleting ? `${deleting.name} will stop routing new passes. Chains already running keep the route they were frozen under.` : ''}
  onclose={() => { if (!remove.isPending) deleting = null; }}
>
  <div class="delete-warning">
    <Icon name="triangle" size="1em" />
    <p>This cannot be undone. A flow that has started chains stays in their history, but it will not be pickable again.</p>
  </div>

  {#snippet footer()}
    <Button variant="ghost" disabled={remove.isPending} onclick={() => (deleting = null)}>Keep flow</Button>
    <Button variant="danger" icon="xmark" loading={remove.isPending} onclick={() => deleting && remove.mutate(deleting)}>Delete flow</Button>
  {/snippet}
</Modal>

<style>
  .rule-summary {
    display: flex;
    align-items: center;
    gap: 15px;
    margin-bottom: 18px;
    padding: 12px 14px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface);
  }

  .rule-summary > div {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 0 0 auto;
  }

  .summary-icon {
    display: grid;
    place-items: center;
    width: 29px;
    height: 29px;
    border-radius: 8px;
    background: var(--brand-soft);
    color: var(--brand-strong);
  }

  .summary-icon.teal {
    background: var(--success-soft);
    color: var(--success);
  }

  .summary-icon.violet {
    background: var(--violet-soft);
    color: var(--violet);
  }

  .rule-summary p {
    display: flex;
    align-items: baseline;
    gap: 5px;
  }

  .rule-summary strong {
    font-size: 17px;
    font-weight: 780;
    letter-spacing: -0.04em;
  }

  .rule-summary p span {
    color: var(--ink-faint);
    font-size: 9px;
    font-weight: 700;
  }

  .rule-summary .summary-note {
    margin-left: auto;
    max-width: 48ch;
    color: var(--ink-faint);
    font-size: 9px;
    line-height: 1.5;
    text-align: right;
  }

  .flow-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px;
  }

  .flow-skeleton {
    height: 380px;
    border-radius: var(--radius);
  }

  .flow-card {
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .flow-card.inactive {
    opacity: 0.78;
  }

  .flow-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 14px 16px 0;
  }

  .flow-badges {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }

  .priority {
    color: var(--ink-faint);
    font-family: var(--mono);
    font-size: 9.5px;
    font-weight: 800;
  }

  .flow-title {
    padding: 11px 16px 0;
  }

  .flow-title h2 {
    font-size: 16px;
    font-weight: 820;
  }

  .flow-title p {
    margin-top: 4px;
    color: var(--ink-soft);
    font-size: 10px;
    line-height: 1.55;
  }

  .conditions {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--line);
  }

  .conditions span {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 6px;
    border-radius: 5px;
    background: var(--surface-muted);
    color: var(--ink-soft);
    font-size: 8px;
    font-weight: 650;
  }

  .conditions span.muted {
    color: var(--ink-faint);
    font-style: italic;
  }

  .steps-preview {
    flex: 1;
    padding: 12px 16px;
  }

  .preview-label {
    margin-bottom: 8px;
    color: var(--ink-faint);
    font-size: 8px;
    font-weight: 800;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .steps-preview ol {
    display: flex;
    flex-direction: column;
    gap: 7px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .steps-preview li {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .step-no {
    display: grid;
    place-items: center;
    width: 20px;
    height: 20px;
    flex: 0 0 auto;
    border-radius: 6px;
    background: var(--surface-muted);
    color: var(--ink-faint);
    font-size: 8px;
    font-weight: 800;
  }

  .steps-preview li > div {
    flex: 1;
    min-width: 0;
  }

  .steps-preview strong {
    display: block;
    overflow: hidden;
    color: var(--ink-soft);
    font-size: 9.5px;
    font-weight: 750;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .steps-preview small {
    display: block;
    margin-top: 1px;
    overflow: hidden;
    color: var(--ink-faint);
    font-size: 8px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sla {
    flex: 0 0 auto;
    padding: 2px 5px;
    border-radius: 4px;
    background: var(--amber-soft);
    color: var(--amber);
    font-size: 8px;
    font-weight: 750;
  }

  .no-steps {
    color: var(--ink-faint);
    font-size: 9px;
  }

  .flow-card footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 10px 16px;
    border-top: 1px solid var(--line);
    background: var(--surface-muted);
  }

  .updated {
    color: var(--ink-faint);
    font-size: 8px;
  }

  .row-actions {
    display: flex;
    gap: 4px;
  }

  .error-state {
    display: flex;
    align-items: center;
    flex-direction: column;
    padding: 45px 24px;
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

  .delete-warning {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    padding: 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 25%, var(--line));
    border-radius: 9px;
    background: var(--danger-soft);
    color: var(--danger);
  }

  .delete-warning p {
    font-size: 10.5px;
    line-height: 1.55;
  }

  @media (max-width: 900px) {
    .flow-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 700px) {
    .rule-summary {
      align-items: flex-start;
      flex-wrap: wrap;
    }

    .rule-summary .summary-note {
      width: 100%;
      margin-left: 0;
      text-align: left;
    }
  }
</style>

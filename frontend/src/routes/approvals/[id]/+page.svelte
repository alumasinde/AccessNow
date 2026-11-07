<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { ApprovalChainView } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { toasts } from '$lib/stores/toast.svelte';
  import { formatDateTime, titleCase } from '$lib/format';
  import { approvalStatusMeta, askStatusMeta, approverLabel, formatValue, modeLabel, sourceModuleLabel } from '$lib/approval';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import ApprovalTabs from '$lib/components/ApprovalTabs.svelte';
  import Icon from '$lib/components/Icon.svelte';

  type Ask = NonNullable<ApprovalChainView['asks']>[number];
  type DecisionKind = 'approve' | 'reject' | 'return';

  const requestID = $derived(Number(page.params.id));
  const allowed = $derived(session.can('approval.view') && session.moduleOn('approval_workflow'));
  const queryClient = useQueryClient();

  const chainQuery = createQuery(() => ({
    queryKey: ['approvals', 'chain', requestID],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/approvals/{requestID}', {
        params: { path: { requestID } }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    enabled: allowed && requestID > 0
  }));

  const chain = $derived(chainQuery.data);
  const status = $derived(chain ? approvalStatusMeta(chain.request.status) : null);
  const canDecide = $derived(session.can('approval.decide') && !session.isReadOnly);
  const canCancel = $derived(
    Boolean(chain?.request.is_open) &&
      !session.isReadOnly &&
      (session.isSuperAdmin || (chain?.pass.requester_user_id ?? 0) === (session.user?.id ?? 0))
  );
  const canReset = $derived(
    Boolean(chain) && canDecide && !chain?.request.is_open && chain?.request.status !== 'cancelled'
  );

  type StepGroup = {
    stepNo: number;
    name: string;
    mode: string;
    memo?: string | null;
    approverType: string;
    roleKey?: string | null;
    slaHours?: number | null;
    asks: Ask[];
  };

  const stepGroups = $derived.by<StepGroup[]>(() => {
    if (!chain) return [];
    const numbers = [...new Set([...(chain.steps ?? []).map((step) => step.step_no), ...(chain.asks ?? []).map((ask) => ask.step_no)])].sort((a, b) => a - b);
    return numbers.map((stepNo) => {
      const definition = chain.steps?.find((step) => step.step_no === stepNo);
      const asks = (chain.asks ?? []).filter((ask) => ask.step_no === stepNo);
      const first = asks[0];
      return {
        stepNo,
        name: definition?.name ?? first?.name ?? `Step ${stepNo}`,
        mode: definition?.mode ?? first?.mode ?? 'sequential',
        memo: definition?.memo,
        approverType: definition?.approver_type ?? 'role',
        roleKey: definition?.approver_role_key,
        slaHours: definition?.sla_hours,
        asks
      };
    });
  });

  function stepTone(asks: Ask[]): 'done' | 'active' | 'stopped' | 'idle' {
    if (asks.some((ask) => ask.is_waiting)) return 'active';
    if (asks.some((ask) => ask.status === 'rejected' || ask.status === 'returned' || ask.status === 'expired')) return 'stopped';
    if (asks.length > 0 && asks.every((ask) => ['approved', 'auto_approved', 'skipped', 'delegated'].includes(ask.status))) return 'done';
    return 'idle';
  }

  let decisionAsk = $state<Ask | null>(null);
  let decisionAction = $state<DecisionKind>('approve');
  let comment = $state('');
  let decisionError = $state<string | null>(null);

  let cancelOpen = $state(false);
  let cancelReason = $state('');
  let cancelError = $state<string | null>(null);

  let resetOpen = $state(false);
  let resetReason = $state('');
  let resetError = $state<string | null>(null);

  function openDecision(ask: Ask, action: DecisionKind) {
    decisionAsk = ask;
    decisionAction = action;
    comment = '';
    decisionError = null;
  }

  function refresh() {
    queryClient.invalidateQueries({ queryKey: ['approvals'] });
  }

  const decide = createMutation(() => ({
    mutationFn: async (input: { stepID: number; action: DecisionKind; comment: string }) => {
      const { data, error, response } = await api.POST('/api/v1/approvals/steps/{stepID}/decision', {
        params: { path: { stepID: input.stepID } },
        body: { action: input.action, comment: input.comment }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: (_, input) => {
      const labels: Record<DecisionKind, string> = {
        approve: 'Approval recorded',
        reject: 'Request rejected',
        return: 'Request returned'
      };
      toasts.success(labels[input.action], 'The chain and its pass have been updated.');
      decisionAsk = null;
      comment = '';
      refresh();
    },
    onError: (error: Error) => (decisionError = error.message)
  }));

  const cancelChain = createMutation(() => ({
    mutationFn: async (reason: string) => {
      const { data, error, response } = await api.POST('/api/v1/approvals/{requestID}/cancel', {
        params: { path: { requestID } },
        body: { reason }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: () => {
      toasts.success('Chain cancelled', 'The pass has been withdrawn from the approval queue.');
      cancelOpen = false;
      cancelReason = '';
      refresh();
    },
    onError: (error: Error) => (cancelError = error.message)
  }));

  const resetChain = createMutation(() => ({
    mutationFn: async (reason: string) => {
      if (!chain) throw new Error('The chain is no longer loaded.');
      const { data, error, response } = await api.POST('/api/v1/approvals/passes/{passID}/reset', {
        params: { path: { passID: chain.pass.id } },
        body: { reason }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: (fresh) => {
      toasts.success('Fresh chain filed', 'The pass is asking again under the current flow.');
      resetOpen = false;
      resetReason = '';
      queryClient.invalidateQueries({ queryKey: ['approvals'] });
      goto(`/approvals/${fresh.request.id}`);
    },
    onError: (error: Error) => (resetError = error.message)
  }));

  function submitDecision() {
    if (!decisionAsk) return;
    decisionError = null;
    if ((decisionAction === 'reject' || decisionAction === 'return') && !comment.trim()) {
      decisionError = `A ${decisionAction === 'reject' ? 'rejection' : 'return'} reason is required.`;
      return;
    }
    decide.mutate({ stepID: decisionAsk.id, action: decisionAction, comment: comment.trim() });
  }

  function submitCancel() {
    cancelError = null;
    if (!cancelReason.trim()) {
      cancelError = 'A cancellation reason is required.';
      return;
    }
    cancelChain.mutate(cancelReason.trim());
  }

  function submitReset() {
    resetError = null;
    if (!resetReason.trim()) {
      resetError = 'A restart reason is required.';
      return;
    }
    resetChain.mutate(resetReason.trim());
  }

  const decisionTitle = $derived(
    decisionAction === 'approve' ? 'Approve this step' : decisionAction === 'reject' ? 'Reject this request' : 'Return for correction'
  );
</script>

<svelte:head><title>{chain ? `${chain.pass.subject_name} · Approval` : 'Approval chain · AccessNow'}</title></svelte:head>

<div class="page">
  {#if !allowed}
    <AccessDenied moduleOff={!session.moduleOn('approval_workflow')} />
  {:else if chainQuery.isPending}
    <div class="detail-loading">
      <span class="skeleton title-skeleton"></span>
      <span class="skeleton card-skeleton"></span>
    </div>
  {:else if chainQuery.isError || !chain}
    <div class="load-error">
      <Icon name="circleExclamation" size="1.5em" />
      <h2>This chain could not be opened</h2>
      <p>{chainQuery.error?.message ?? 'It may not exist, or it belongs to another workspace.'}</p>
      <div class="row">
        <Button variant="secondary" href="/approvals" icon="arrowLeft">Back to inbox</Button>
        <Button onclick={() => chainQuery.refetch()}>Try again</Button>
      </div>
    </div>
  {:else}
    <PageHeader
      eyebrow={`${chain.pass.pass_no} · ${sourceModuleLabel(chain.pass.source_module_key)}`}
      title={chain.pass.subject_name}
      description={`Approval chain #${chain.request.id}. The route below was frozen when the pass was filed.`}
    >
      {#snippet actions()}
        <Button variant="ghost" href="/approvals" icon="arrowLeft">Inbox</Button>
        {#if canCancel}
          <Button variant="danger" icon="ban" onclick={() => { cancelOpen = true; cancelError = null; }}>Cancel chain</Button>
        {/if}
        {#if canReset}
          <Button variant="secondary" icon="refresh" onclick={() => { resetOpen = true; resetError = null; }}>Restart chain</Button>
        {/if}
        <Button variant="secondary" icon="refresh" loading={chainQuery.isFetching} onclick={() => chainQuery.refetch()}>Refresh</Button>
      {/snippet}
    </PageHeader>

    <ApprovalTabs />

    <div class="status-banner {chain.is_overdue ? 'danger' : (status?.tone ?? 'neutral')}">
      <span class="status-icon"><Icon name={status?.icon ?? 'circleInfo'} size="1em" /></span>
      <div>
        <strong>{status?.label}</strong>
        <p>{status?.description}</p>
      </div>
      {#if chain.waiting_on?.length}
        <span class="waiting-on"><Icon name="users" size="0.75em" /> Waiting on {chain.waiting_on.join(', ')}</span>
      {:else if !chain.request.is_open}
        <span class="waiting-on">Closed {chain.request.completed_at ? formatDateTime(chain.request.completed_at, session.tenant?.timezone) : ''}</span>
      {/if}
    </div>

    <div class="detail-grid">
      <div class="main-column">
        <Card padded={false} class="route-card">
          <div class="card-head">
            <div>
              <p class="eyebrow">Frozen route</p>
              <h2>The chain this pass is following</h2>
            </div>
            <Badge tone="neutral" icon="list">{chain.tally.asked} {chain.tally.asked === 1 ? 'ask' : 'asks'}</Badge>
          </div>

          <ol class="chain">
            {#each stepGroups as group (group.stepNo)}
              {@const tone = stepTone(group.asks)}
              <li class="chain-step {tone}">
                <div class="rail">
                  <span class="rail-dot">
                    {#if tone === 'done'}<Icon name="check" size="0.62em" />{:else}{group.stepNo}{/if}
                  </span>
                </div>
                <div class="step-body">
                  <div class="step-heading">
                    <div>
                      <h3>{group.name}</h3>
                      <p>
                        Step {group.stepNo} · {approverLabel(group.approverType)}
                        {#if group.roleKey}<span class="role-chip">{group.roleKey.replaceAll('_', ' ')}</span>{/if}
                        · {modeLabel(group.mode)}
                        {#if group.slaHours}· {group.slaHours}h SLA{/if}
                      </p>
                    </div>
                    {#if tone === 'active'}
                      <Badge tone="amber" dot>Waiting</Badge>
                    {:else if tone === 'done'}
                      <Badge tone="success" icon="check">Done</Badge>
                    {:else if tone === 'stopped'}
                      <Badge tone="danger" icon="xmark">Stopped</Badge>
                    {/if}
                  </div>

                  {#if group.memo}<p class="step-memo">{group.memo}</p>{/if}

                  <div class="asks">
                    {#each group.asks as ask (ask.id)}
                      {@const askStatus = askStatusMeta(ask.status)}
                      <div class="ask" class:actionable={ask.can_answer && canDecide}>
                        <div class="ask-person">
                          <span class="ask-avatar"><Icon name="user" size="0.75em" /></span>
                          <div>
                            <strong>{ask.assignee}</strong>
                            <small>
                              Assigned {formatDateTime(ask.assigned_at, session.tenant?.timezone)}
                              {#if ask.delegated_from_user_id}· covering user #{ask.delegated_from_user_id}{/if}
                              {#if ask.escalated_from_step_no}· escalated from step {ask.escalated_from_step_no}{/if}
                            </small>
                          </div>
                        </div>
                        <div class="ask-meta">
                          {#if ask.due_at}
                            <span class:late={ask.is_overdue}><Icon name="clock" size="0.68em" /> {formatDateTime(ask.due_at, session.tenant?.timezone)}</span>
                          {/if}
                          <Badge tone={askStatus.tone} icon={askStatus.icon}>{askStatus.label}</Badge>
                        </div>
                        {#if ask.comment}
                          <blockquote class="ask-comment">“{ask.comment}”</blockquote>
                        {/if}
                        {#if ask.can_answer && canDecide}
                          <div class="ask-actions">
                            <span class="turn-note"><Icon name="bell" size="0.7em" /> Your answer is needed</span>
                            <div>
                              <Button variant="ghost" size="sm" icon="arrowLeft" onclick={() => openDecision(ask, 'return')}>Return</Button>
                              <Button variant="danger" size="sm" icon="xmark" onclick={() => openDecision(ask, 'reject')}>Reject</Button>
                              <Button size="sm" icon="check" onclick={() => openDecision(ask, 'approve')}>Approve</Button>
                            </div>
                          </div>
                        {/if}
                      </div>
                    {:else}
                      <div class="ask empty-ask">No ask was issued for this step.</div>
                    {/each}
                  </div>
                </div>
              </li>
            {/each}
          </ol>
        </Card>
      </div>

      <div class="side-column">
        <Card title="Decision tally" eyebrow="Live count">
          <div class="tally">
            <div><strong class="mono">{chain.tally.asked}</strong><span>asked</span></div>
            <div><strong class="mono">{chain.tally.waiting}</strong><span>waiting</span></div>
            <div><strong class="mono">{chain.tally.approved}</strong><span>approved</span></div>
            <div><strong class="mono">{chain.tally.steps_remaining}</strong><span>steps left</span></div>
          </div>
          <div class="tally-bars">
            <div><span>Answered</span><strong>{chain.tally.answered} / {chain.tally.asked}</strong></div>
            <div class="bar"><span style="width: {chain.tally.asked ? (chain.tally.answered / chain.tally.asked) * 100 : 0}%"></span></div>
          </div>
        </Card>

        <Card title="Pass brief" eyebrow="What is being decided">
          <div class="facts">
            <div><span>Pass</span><strong class="mono">{chain.pass.pass_no}</strong></div>
            <div><span>Status</span><strong>{titleCase(chain.pass.status)}</strong></div>
            <div><span>Source</span><strong>{sourceModuleLabel(chain.pass.source_module_key)}</strong></div>
            <div><span>Value</span><strong>{chain.pass.value_amount !== null && chain.pass.value_amount !== undefined ? formatValue(chain.pass.value_amount, chain.pass.value_currency) : 'Not set'}</strong></div>
            <div><span>Valid from</span><strong>{formatDateTime(chain.pass.valid_from, session.tenant?.timezone)}</strong></div>
            <div><span>Valid until</span><strong>{chain.pass.valid_until ? formatDateTime(chain.pass.valid_until, session.tenant?.timezone) : 'No end'}</strong></div>
            <div><span>Requester</span><strong>{chain.pass.requester_user_id ? `User #${chain.pass.requester_user_id}` : 'Not recorded'}</strong></div>
            <div><span>Gate ready</span><strong>{chain.pass.is_scannable ? 'Yes' : 'Not yet'}</strong></div>
          </div>
        </Card>

        <Card title="Chain record" eyebrow="Identifiers">
          <div class="facts compact">
            <div><span>Chain</span><code>#{chain.request.id}</code></div>
            <div><span>Pass</span><code>#{chain.pass.id}</code></div>
            <div><span>Flow</span><code>{chain.request.approval_flow_id ? `#${chain.request.approval_flow_id}` : 'Dynamic'}</code></div>
            <div><span>Current step</span><code>{chain.request.current_step_no} / {chain.request.total_steps}</code></div>
            <div><span>Started</span><strong>{formatDateTime(chain.request.started_at, session.tenant?.timezone)}</strong></div>
            <div><span>Deadline</span><strong class:late={chain.is_overdue}>{chain.step_deadline_at ? formatDateTime(chain.step_deadline_at, session.tenant?.timezone) : 'None set'}</strong></div>
          </div>
        </Card>
      </div>
    </div>
  {/if}
</div>

<Modal
  open={decisionAsk !== null}
  title={decisionTitle}
  description={decisionAction === 'approve'
    ? 'Your answer moves the chain to the next step or releases the pass.'
    : decisionAction === 'reject'
      ? 'The request stops here and the pass is rejected. The reason is visible to the requester.'
      : 'The chain goes back to the requester for correction. The reason is required.'}
  onclose={() => { if (!decide.isPending) { decisionAsk = null; decisionError = null; } }}
>
  {#if decisionAsk}
    <div class="decision-context">
      <span class="context-icon"><Icon name="stamp" size="0.85em" /></span>
      <div>
        <strong>{decisionAsk.name}</strong>
        <small>{decisionAsk.assignee} · step {decisionAsk.step_no}</small>
      </div>
    </div>

    <label class="field-label" for="decision-comment">
      {decisionAction === 'approve' ? 'Decision note' : decisionAction === 'reject' ? 'Rejection reason' : 'Return reason'}
      {#if decisionAction !== 'approve'}<span class="required">*</span>{/if}
    </label>
    <textarea
      id="decision-comment"
      class="textarea"
      bind:value={comment}
      maxlength="2000"
      placeholder={decisionAction === 'approve' ? 'Optional context for the record…' : 'Explain what needs to change or why this is being declined…'}
    ></textarea>
    {#if decisionError}<p class="field-error">{decisionError}</p>{/if}
  {/if}

  {#snippet footer()}
    <Button variant="ghost" disabled={decide.isPending} onclick={() => (decisionAsk = null)}>Go back</Button>
    <Button
      variant={decisionAction === 'reject' ? 'danger' : decisionAction === 'return' ? 'secondary' : 'primary'}
      icon={decisionAction === 'approve' ? 'check' : decisionAction === 'reject' ? 'xmark' : 'arrowLeft'}
      loading={decide.isPending}
      onclick={submitDecision}
    >
      {decisionAction === 'approve' ? 'Approve step' : decisionAction === 'reject' ? 'Reject request' : 'Return request'}
    </Button>
  {/snippet}
</Modal>

<Modal
  open={cancelOpen}
  title="Cancel this approval chain"
  description="The pass is withdrawn from the queue. The reason is kept in the chain record and the audit trail."
  onclose={() => { if (!cancelChain.isPending) cancelOpen = false; }}
>
  <label class="field-label" for="cancel-reason">Cancellation reason <span class="required">*</span></label>
  <textarea id="cancel-reason" class="textarea" bind:value={cancelReason} maxlength="2000" placeholder="Why is this pass being withdrawn?"></textarea>
  {#if cancelError}<p class="field-error">{cancelError}</p>{/if}

  {#snippet footer()}
    <Button variant="ghost" disabled={cancelChain.isPending} onclick={() => (cancelOpen = false)}>Keep it open</Button>
    <Button variant="danger" icon="ban" loading={cancelChain.isPending} onclick={submitCancel}>Cancel chain</Button>
  {/snippet}
</Modal>

<Modal
  open={resetOpen}
  title="Restart this approval chain"
  description="The current chain becomes history and a fresh one is filed under the flow that matches the pass today. Use this when the paper changed after the decision."
  onclose={() => { if (!resetChain.isPending) resetOpen = false; }}
>
  <label class="field-label" for="reset-reason">Restart reason <span class="required">*</span></label>
  <textarea id="reset-reason" class="textarea" bind:value={resetReason} maxlength="2000" placeholder="What changed since this chain was decided?"></textarea>
  {#if resetError}<p class="field-error">{resetError}</p>{/if}

  {#snippet footer()}
    <Button variant="ghost" disabled={resetChain.isPending} onclick={() => (resetOpen = false)}>Leave closed</Button>
    <Button icon="refresh" loading={resetChain.isPending} onclick={submitReset}>Restart chain</Button>
  {/snippet}
</Modal>

<style>
  .detail-loading {
    display: flex;
    flex-direction: column;
    gap: 18px;
    padding-top: 20px;
  }

  .title-skeleton {
    width: 260px;
    height: 38px;
  }

  .card-skeleton {
    width: 100%;
    height: 320px;
    border-radius: var(--radius);
  }

  .load-error {
    display: flex;
    align-items: center;
    flex-direction: column;
    padding: 80px 20px;
    text-align: center;
  }

  .load-error :global(.fa-icon) {
    margin-bottom: 14px;
    color: var(--danger);
  }

  .load-error h2 {
    font-size: 23px;
  }

  .load-error p {
    margin: 7px 0 18px;
    color: var(--ink-soft);
    font-size: 12px;
  }

  .row {
    display: flex;
    gap: 8px;
  }

  .status-banner {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 18px;
    padding: 13px 15px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--surface);
  }

  .status-icon {
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    flex: 0 0 auto;
    border-radius: 10px;
    background: var(--surface-muted);
    color: var(--ink-soft);
  }

  .status-banner.amber .status-icon,
  .status-banner.violet .status-icon {
    background: var(--amber-soft);
    color: var(--amber);
  }

  .status-banner.teal .status-icon {
    background: var(--teal-soft);
    color: var(--teal);
  }

  .status-banner.success .status-icon {
    background: var(--success-soft);
    color: var(--success);
  }

  .status-banner.danger .status-icon {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .status-banner > div {
    flex: 1;
  }

  .status-banner strong {
    font-size: 11.5px;
    font-weight: 800;
  }

  .status-banner p {
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 10px;
  }

  .waiting-on {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--ink-faint);
    font-size: 10px;
    font-weight: 700;
    max-width: 38%;
    text-align: right;
  }

  .detail-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(270px, 0.58fr);
    gap: 18px;
    align-items: start;
  }

  .main-column,
  .side-column {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  :global(.route-card) {
    overflow: hidden;
  }

  .card-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 18px 18px 0;
  }

  .card-head h2 {
    margin-top: 3px;
    font-size: 16px;
    font-weight: 800;
  }

  .chain {
    margin: 0;
    padding: 18px 18px 6px;
    list-style: none;
  }

  .chain-step {
    display: flex;
    gap: 12px;
    padding-bottom: 20px;
  }

  .rail {
    display: flex;
    justify-content: center;
    width: 26px;
    flex: 0 0 auto;
  }

  .rail-dot {
    position: relative;
    z-index: 1;
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    border: 1px solid var(--line-strong);
    border-radius: 50%;
    background: var(--surface);
    color: var(--ink-faint);
    font-size: 9px;
    font-weight: 800;
  }

  .chain-step:not(:last-child) .rail {
    position: relative;
  }

  .chain-step:not(:last-child) .rail::after {
    content: '';
    position: absolute;
    top: 24px;
    bottom: -20px;
    left: 50%;
    width: 1px;
    background: var(--line);
  }

  .chain-step.done .rail-dot {
    border-color: var(--success);
    background: var(--success);
    color: #fff;
  }

  .chain-step.active .rail-dot {
    border-color: var(--amber);
    background: var(--amber);
    color: #fff;
    box-shadow: 0 0 0 4px var(--amber-soft);
  }

  .chain-step.stopped .rail-dot {
    border-color: var(--danger);
    background: var(--danger);
    color: #fff;
  }

  .step-body {
    flex: 1;
    min-width: 0;
    padding-bottom: 4px;
  }

  .step-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    min-height: 24px;
  }

  .step-heading h3 {
    font-size: 12.5px;
    font-weight: 800;
  }

  .step-heading p {
    margin-top: 2px;
    color: var(--ink-faint);
    font-size: 9.5px;
    line-height: 1.5;
  }

  .role-chip {
    display: inline-block;
    margin: 0 2px;
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--surface-muted);
    color: var(--ink-soft);
    font-size: 8.5px;
    font-weight: 800;
  }

  .step-memo {
    margin-top: 8px;
    color: var(--ink-soft);
    font-size: 10.5px;
    line-height: 1.55;
  }

  .asks {
    display: flex;
    flex-direction: column;
    gap: 7px;
    margin-top: 9px;
  }

  .ask {
    padding: 10px 11px;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--surface-muted);
  }

  .ask.actionable {
    border-color: color-mix(in srgb, var(--amber) 45%, var(--line));
    background: color-mix(in srgb, var(--amber-soft) 45%, var(--surface));
  }

  .ask-person {
    display: flex;
    align-items: center;
    gap: 9px;
  }

  .ask-avatar {
    display: grid;
    place-items: center;
    width: 27px;
    height: 27px;
    flex: 0 0 auto;
    border-radius: 8px;
    background: var(--surface);
    color: var(--ink-soft);
    border: 1px solid var(--line);
  }

  .ask-person > div {
    flex: 1;
    min-width: 0;
  }

  .ask-person strong {
    display: block;
    font-size: 10.5px;
    font-weight: 800;
  }

  .ask-person small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 8.5px;
    line-height: 1.4;
  }

  .ask-meta {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-top: 7px;
    color: var(--ink-faint);
    font-size: 8.5px;
    font-weight: 650;
  }

  .ask-meta > span {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }

  .ask-comment {
    margin-top: 7px;
    padding: 7px 9px;
    border-left: 2px solid var(--line-strong);
    color: var(--ink-soft);
    font-size: 9.5px;
    font-style: italic;
    line-height: 1.5;
  }

  .ask-actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 9px;
    margin-top: 10px;
    padding-top: 9px;
    border-top: 1px solid color-mix(in srgb, var(--amber) 25%, var(--line));
  }

  .ask-actions > div {
    display: flex;
    gap: 5px;
  }

  .turn-note {
    display: flex;
    align-items: center;
    gap: 5px;
    color: var(--amber);
    font-size: 8.5px;
    font-weight: 800;
  }

  .empty-ask {
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .tally {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1px;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: var(--line);
  }

  .tally > div {
    padding: 10px;
    background: var(--surface-muted);
  }

  .tally strong {
    display: block;
    font-size: 19px;
    font-weight: 780;
    letter-spacing: -0.04em;
  }

  .tally span {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 8.5px;
    font-weight: 700;
  }

  .tally-bars {
    margin-top: 13px;
  }

  .tally-bars > div:first-child {
    display: flex;
    justify-content: space-between;
    margin-bottom: 5px;
    color: var(--ink-faint);
    font-size: 9px;
    font-weight: 650;
  }

  .tally-bars strong {
    color: var(--ink-soft);
  }

  .bar {
    height: 5px;
    overflow: hidden;
    border-radius: 999px;
    background: var(--line);
  }

  .bar span {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: var(--teal);
  }

  .facts {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }

  .facts > div {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    font-size: 10.5px;
  }

  .facts span {
    color: var(--ink-faint);
  }

  .facts strong,
  .facts code {
    color: var(--ink-soft);
    font-size: 10px;
    font-weight: 750;
    text-align: right;
  }

  .facts.compact {
    gap: 8px;
  }

  code {
    padding: 2px 6px;
    border-radius: 5px;
    background: var(--surface-muted);
    font-size: 9px;
  }

  .late {
    color: var(--danger) !important;
  }

  .decision-context {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 17px;
    padding: 10px 11px;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: var(--surface-muted);
  }

  .context-icon {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 8px;
    background: var(--brand-soft);
    color: var(--brand-strong);
  }

  .decision-context strong {
    display: block;
    font-size: 11px;
    font-weight: 800;
  }

  .decision-context small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .field-label {
    display: block;
    margin-bottom: 6px;
    color: var(--ink-soft);
    font-size: 10px;
    font-weight: 800;
  }

  .required {
    color: var(--brand);
  }

  @media (max-width: 900px) {
    .detail-grid {
      grid-template-columns: 1fr;
    }

    .side-column {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 620px) {
    .status-banner {
      align-items: flex-start;
      flex-wrap: wrap;
    }

    .waiting-on {
      width: 100%;
      max-width: none;
      padding-left: 46px;
      text-align: left;
    }

    .side-column {
      grid-template-columns: 1fr;
    }

    .ask-actions {
      align-items: flex-start;
      flex-direction: column;
    }

    .ask-actions > div {
      width: 100%;
    }

    .ask-actions :global(.btn) {
      flex: 1;
    }

    .chain {
      padding-left: 14px;
      padding-right: 14px;
    }
  }
</style>

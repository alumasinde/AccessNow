<script lang="ts">
  import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { ApprovalDelegationBody, ApprovalDelegationView } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { toasts } from '$lib/stores/toast.svelte';
  import { formatDateTime, titleCase } from '$lib/format';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import Card from '$lib/components/Card.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Field from '$lib/components/Field.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ApprovalTabs from '$lib/components/ApprovalTabs.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const allowed = $derived(session.can('approval.view') && session.moduleOn('approval_workflow'));
  const canManage = $derived(session.can('approval.delegate') && !session.isReadOnly);
  const queryClient = useQueryClient();

  let scope = $state<'mine' | 'all'>('mine');
  const fromID = $derived(scope === 'mine' ? (session.user?.id ?? 0) : 0);

  const delegationsQuery = createQuery(() => ({
    queryKey: ['approval-delegations', scope, fromID],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/approvals/delegations', {
        params: { query: fromID > 0 ? { from_user_id: fromID } : {} }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    enabled: allowed
  }));

  const delegations = $derived(delegationsQuery.data?.items ?? []);
  const inForce = $derived(delegations.filter((delegation) => delegation.is_in_force).length);
  const scheduled = $derived(
    delegations.filter((delegation) => delegation.status === 'active' && new Date(delegation.starts_at) > new Date()).length
  );
  const ended = $derived(delegations.filter((delegation) => delegation.status !== 'active' || new Date(delegation.ends_at) <= new Date()).length);

  let createOpen = $state(false);
  let fromUser = $state('');
  let toUser = $state('');
  let startsAt = $state('');
  let endsAt = $state('');
  let flowID = $state('');
  let passTypeID = $state('');
  let departmentID = $state('');
  let reason = $state('');
  let formError = $state<string | null>(null);

  let ending = $state<ApprovalDelegationView | null>(null);

  function localInput(date: Date): string {
    const offset = date.getTimezoneOffset();
    return new Date(date.getTime() - offset * 60_000).toISOString().slice(0, 16);
  }

  function openCreate() {
    fromUser = String(session.user?.id ?? '');
    toUser = '';
    startsAt = localInput(new Date());
    endsAt = localInput(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000));
    flowID = '';
    passTypeID = '';
    departmentID = '';
    reason = '';
    formError = null;
    createOpen = true;
  }

  function integerOrZero(value: string): number {
    const parsed = Number.parseInt(value, 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
  }

  function isoOrNull(value: string): string | null {
    if (!value) return null;
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date.toISOString();
  }

  const createDelegation = createMutation(() => ({
    mutationFn: async (body: ApprovalDelegationBody) => {
      const { data, error, response } = await api.POST('/api/v1/approvals/delegations', { body });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: () => {
      toasts.success('Cover filed', 'The delegate can now answer the selected approvals in your name.');
      createOpen = false;
      queryClient.invalidateQueries({ queryKey: ['approval-delegations'] });
    },
    onError: (error: Error) => (formError = error.message)
  }));

  const endDelegation = createMutation(() => ({
    mutationFn: async (delegation: ApprovalDelegationView) => {
      const { error, response } = await api.POST('/api/v1/approvals/delegations/{delegationID}/end', {
        params: { path: { delegationID: delegation.id } }
      });
      if (!response.ok) throw new Error(errorMessage(error, response));
      return delegation;
    },
    onSuccess: () => {
      toasts.success('Cover ended', 'New asks will go back to the original approver.');
      ending = null;
      queryClient.invalidateQueries({ queryKey: ['approval-delegations'] });
    },
    onError: (error: Error) => toasts.error('The cover could not be ended', error.message)
  }));

  function submit() {
    formError = null;
    if (!fromUser || integerOrZero(fromUser) === 0) {
      formError = 'The delegating approver is required.';
      return;
    }
    if (!toUser || integerOrZero(toUser) === 0) {
      formError = 'The person taking cover needs a user ID.';
      return;
    }
    if (integerOrZero(fromUser) === integerOrZero(toUser)) {
      formError = 'An approver cannot delegate to themselves.';
      return;
    }
    const start = isoOrNull(startsAt);
    const end = isoOrNull(endsAt);
    if (startsAt && !start) {
      formError = 'The cover start is not a valid date.';
      return;
    }
    if (!end) {
      formError = 'The cover needs an end date.';
      return;
    }
    const startMs = start ? new Date(start).getTime() : Date.now();
    if (new Date(end).getTime() <= startMs) {
      formError = 'The cover has to end after it starts.';
      return;
    }

    createDelegation.mutate({
      from_user_id: integerOrZero(fromUser),
      to_user_id: integerOrZero(toUser),
      approval_flow_id: integerOrZero(flowID),
      pass_type_id: integerOrZero(passTypeID),
      department_id: integerOrZero(departmentID),
      starts_at: start,
      ends_at: end,
      reason: reason.trim()
    });
  }

  function canEnd(delegation: ApprovalDelegationView): boolean {
    return canManage && delegation.status === 'active' &&
      (session.isSuperAdmin || delegation.from_user_id === session.user?.id);
  }

  const endingScheduled = $derived(
    Boolean(ending && ending.status === 'active' && new Date(ending.starts_at) > new Date())
  );

  function statusOf(delegation: ApprovalDelegationView): { label: string; tone: 'success' | 'amber' | 'neutral'; icon: 'check' | 'clock' | 'ban' } {
    if (delegation.is_in_force) return { label: 'In force', tone: 'success', icon: 'check' };
    if (delegation.status === 'active' && new Date(delegation.starts_at) > new Date()) return { label: 'Scheduled', tone: 'amber', icon: 'clock' };
    if (delegation.status === 'active') return { label: 'Window passed', tone: 'neutral', icon: 'clock' };
    return { label: titleCase(delegation.status), tone: 'neutral', icon: 'ban' };
  }
</script>

<svelte:head><title>Delegations · AccessNow</title></svelte:head>

<div class="page">
  {#if !allowed}
    <AccessDenied moduleOff={!session.moduleOn('approval_workflow')} />
  {:else}
    <PageHeader
      eyebrow="Approval workflow"
      title="Delegations"
      description="Cover for an absence, handover or busy desk. A delegation lets another person answer the approvals that would otherwise wait for you."
    >
      {#snippet actions()}
        {#if canManage}
          <Button icon="plus" onclick={openCreate}>File cover</Button>
        {/if}
        <Button variant="secondary" icon="refresh" loading={delegationsQuery.isFetching} onclick={() => delegationsQuery.refetch()}>Refresh</Button>
      {/snippet}
    </PageHeader>

    <ApprovalTabs />

    <div class="board-bar">
      <div class="scope-toggle" role="group" aria-label="Delegation scope">
        <button type="button" class:active={scope === 'mine'} onclick={() => (scope = 'mine')}>
          <Icon name="user" size="0.72em" /> Mine
        </button>
        <button type="button" class:active={scope === 'all'} onclick={() => (scope = 'all')}>
          <Icon name="users" size="0.72em" /> Whole board
        </button>
      </div>
      <div class="stats">
        <span><strong class="mono">{inForce}</strong> in force</span>
        <span><strong class="mono">{scheduled}</strong> scheduled</span>
        <span><strong class="mono">{ended}</strong> ended</span>
      </div>
    </div>

    {#if delegationsQuery.isPending}
      <div class="delegation-grid">
        {#each Array(3) as _}
          <div class="delegation-skeleton skeleton"></div>
        {/each}
      </div>
    {:else if delegationsQuery.isError}
      <Card>
        <div class="error-state">
          <span><Icon name="circleExclamation" size="1.3em" /></span>
          <h3>The delegation board could not be loaded</h3>
          <p>{delegationsQuery.error?.message ?? 'Try again in a moment.'}</p>
          <Button variant="secondary" onclick={() => delegationsQuery.refetch()}>Try again</Button>
        </div>
      </Card>
    {:else if delegations.length === 0}
      <Card>
        <EmptyState
          icon="users"
          title={scope === 'mine' ? 'You have no cover filed' : 'No delegations on this board'}
          description={scope === 'mine'
            ? 'When you are away, file cover so your approval asks can be answered instead of waiting.'
            : 'No approver has handed over their queue yet.'}
        >
          {#snippet actions()}
            {#if canManage}
              <Button icon="plus" onclick={openCreate}>File cover</Button>
            {:else}
              <Button variant="secondary" onclick={() => (scope = 'all')}>Open whole board</Button>
            {/if}
          {/snippet}
        </EmptyState>
      </Card>
    {:else}
      <div class="delegation-grid">
        {#each delegations as delegation (delegation.id)}
          {@const state = statusOf(delegation)}
          <article class="delegation-card" class:in-force={delegation.is_in_force}>
            <div class="delegation-head">
              <Badge tone={state.tone} icon={state.icon}>{state.label}</Badge>
              <span class="delegation-id">Cover #{delegation.id}</span>
            </div>

            <div class="handover">
              <div class="person">
                <span class="avatar">{delegation.from_user_id === session.user?.id ? 'You' : `#${delegation.from_user_id}`}</span>
                <div><strong>{delegation.from_user_id === session.user?.id ? 'You' : `User #${delegation.from_user_id}`}</strong><small>Original approver</small></div>
              </div>
              <span class="handover-arrow"><Icon name="arrowRight" size="0.8em" /></span>
              <div class="person">
                <span class="avatar delegate">#{delegation.to_user_id}</span>
                <div><strong>User #{delegation.to_user_id}</strong><small>Answers in cover</small></div>
              </div>
            </div>

            <div class="window">
              <div>
                <span>Starts</span>
                <strong>{formatDateTime(delegation.starts_at, session.tenant?.timezone)}</strong>
              </div>
              <div>
                <span>Ends</span>
                <strong>{formatDateTime(delegation.ends_at, session.tenant?.timezone)}</strong>
              </div>
            </div>

            <div class="scope-tags">
              {#if delegation.approval_flow_id}<span><Icon name="scale" size="0.65em" /> Flow #{delegation.approval_flow_id}</span>{/if}
              {#if delegation.pass_type_id}<span><Icon name="idCard" size="0.65em" /> Pass type #{delegation.pass_type_id}</span>{/if}
              {#if delegation.department_id}<span><Icon name="building" size="0.65em" /> Dept #{delegation.department_id}</span>{/if}
              {#if !delegation.approval_flow_id && !delegation.pass_type_id && !delegation.department_id}
                <span><Icon name="users" size="0.65em" /> All approval asks</span>
              {/if}
            </div>

            {#if delegation.reason}<p class="reason">“{delegation.reason}”</p>{/if}

            <footer>
              <span>Filed {formatDateTime(delegation.created_at, session.tenant?.timezone)}</span>
              {#if canEnd(delegation)}
                <Button variant="ghost" size="sm" icon="ban" onclick={() => (ending = delegation)}>
                  {delegation.is_in_force ? 'End cover' : 'Cancel cover'}
                </Button>
              {/if}
            </footer>
          </article>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<Modal
  open={createOpen}
  title="File approval cover"
  description="The delegate can answer the matching asks while the window is open. The original approver stays on the chain."
  onclose={() => { if (!createDelegation.isPending) createOpen = false; }}
>
  {#if formError}
    <div class="form-error" role="alert"><Icon name="circleExclamation" size="0.85em" /> {formError}</div>
  {/if}

  <div class="form-grid">
    <Field label="From user ID" forId="delegation-from" required hint="Defaults to you. Admins can file for another approver.">
      <input id="delegation-from" class="input" type="number" min="1" bind:value={fromUser} />
    </Field>
    <Field label="To user ID" forId="delegation-to" required>
      <input id="delegation-to" class="input" type="number" min="1" bind:value={toUser} placeholder="Who takes cover" />
    </Field>
    <Field label="Cover starts" forId="delegation-start" hint="Leave blank to start now.">
      <input id="delegation-start" class="input" type="datetime-local" bind:value={startsAt} />
    </Field>
    <Field label="Cover ends" forId="delegation-end" required>
      <input id="delegation-end" class="input" type="datetime-local" bind:value={endsAt} />
    </Field>
  </div>

  <div class="scope-block">
    <p class="scope-title">What is covered?</p>
    <p class="scope-hint">Fill none of these to cover every ask. Fill one or more to narrow the handover.</p>
    <div class="form-grid three">
      <Field label="Flow ID" forId="delegation-flow">
        <input id="delegation-flow" class="input" type="number" min="1" bind:value={flowID} placeholder="Any flow" />
      </Field>
      <Field label="Pass type ID" forId="delegation-pass-type">
        <input id="delegation-pass-type" class="input" type="number" min="1" bind:value={passTypeID} placeholder="Any type" />
      </Field>
      <Field label="Department ID" forId="delegation-department">
        <input id="delegation-department" class="input" type="number" min="1" bind:value={departmentID} placeholder="Any dept" />
      </Field>
    </div>
  </div>

  <Field label="Reason" forId="delegation-reason" hint="Kept on the cover board and in the audit trail.">
    <input id="delegation-reason" class="input" bind:value={reason} maxlength="512" placeholder="e.g. Annual leave from Monday" />
  </Field>

  {#snippet footer()}
    <Button variant="ghost" disabled={createDelegation.isPending} onclick={() => (createOpen = false)}>Cancel</Button>
    <Button icon="check" loading={createDelegation.isPending} onclick={submit}>File cover</Button>
  {/snippet}
</Modal>

<Modal
  open={ending !== null}
  title={endingScheduled ? 'Cancel this scheduled cover?' : 'End this cover?'}
  description={ending
    ? endingScheduled
      ? `The cover for user #${ending.to_user_id} has not started, so cancelling it leaves the handover unused.`
      : `User #${ending.to_user_id} will stop answering for user #${ending.from_user_id} once this takes effect.`
    : ''}
  onclose={() => { if (!endDelegation.isPending) ending = null; }}
>
  <div class="end-warning">
    <Icon name="circleInfo" size="1em" />
    <p>
      {#if endingScheduled}
        This cover never takes effect. The original approver keeps the queue throughout the planned window.
      {:else}
        Asks already assigned to the delegate stay with them. New matching asks go back to the original approver.
      {/if}
    </p>
  </div>

  {#snippet footer()}
    <Button variant="ghost" disabled={endDelegation.isPending} onclick={() => (ending = null)}>Keep cover</Button>
    <Button variant="danger" icon="ban" loading={endDelegation.isPending} onclick={() => ending && endDelegation.mutate(ending)}>
      {endingScheduled ? 'Cancel cover' : 'End cover'}
    </Button>
  {/snippet}
</Modal>

<style>
  .board-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 16px;
  }

  .scope-toggle {
    display: inline-flex;
    gap: 2px;
    padding: 3px;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: var(--surface-muted);
  }

  .scope-toggle button {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    min-height: 29px;
    padding: 0 10px;
    border: 1px solid transparent;
    border-radius: 6px;
    background: transparent;
    color: var(--ink-faint);
    font-size: 9.5px;
    font-weight: 750;
  }

  .scope-toggle button.active {
    border-color: var(--line);
    background: var(--surface);
    color: var(--ink);
    box-shadow: var(--shadow-sm);
  }

  .stats {
    display: flex;
    align-items: center;
    gap: 14px;
  }

  .stats span {
    color: var(--ink-faint);
    font-size: 9px;
    font-weight: 650;
  }

  .stats strong {
    margin-right: 2px;
    color: var(--ink);
    font-size: 12px;
  }

  .delegation-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px;
  }

  .delegation-skeleton {
    height: 300px;
    border-radius: var(--radius);
  }

  .delegation-card {
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .delegation-card.in-force {
    border-top: 3px solid var(--success);
  }

  .delegation-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 14px 16px 0;
  }

  .delegation-id {
    color: var(--ink-faint);
    font-family: var(--mono);
    font-size: 8.5px;
  }

  .handover {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 13px 16px;
  }

  .person {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    flex: 1;
  }

  .avatar {
    display: grid;
    place-items: center;
    width: 31px;
    height: 31px;
    flex: 0 0 auto;
    border-radius: 9px;
    background: var(--surface-muted);
    color: var(--ink-faint);
    font-size: 8px;
    font-weight: 800;
  }

  .avatar.delegate {
    background: var(--teal-soft);
    color: var(--teal);
  }

  .person strong {
    display: block;
    overflow: hidden;
    font-size: 9.5px;
    font-weight: 800;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .person small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 8px;
  }

  .handover-arrow {
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    flex: 0 0 auto;
    border-radius: 50%;
    background: var(--surface-muted);
    color: var(--ink-faint);
  }

  .window {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1px;
    margin: 0 16px;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--line);
  }

  .window > div {
    padding: 8px 9px;
    background: var(--surface-muted);
  }

  .window span {
    display: block;
    color: var(--ink-faint);
    font-size: 8px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .window strong {
    display: block;
    margin-top: 2px;
    color: var(--ink-soft);
    font-size: 9px;
    font-weight: 750;
    line-height: 1.4;
  }

  .scope-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    padding: 11px 16px 0;
  }

  .scope-tags span {
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

  .reason {
    margin: 10px 16px 0;
    padding-left: 8px;
    border-left: 2px solid var(--line-strong);
    color: var(--ink-faint);
    font-size: 9px;
    font-style: italic;
    line-height: 1.5;
  }

  .delegation-card footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    margin-top: 12px;
    padding: 9px 16px;
    border-top: 1px solid var(--line);
    background: var(--surface-muted);
  }

  .delegation-card footer > span {
    color: var(--ink-faint);
    font-size: 8px;
  }

  .form-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 13px;
  }

  .form-grid.three {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .scope-block {
    margin: 17px 0;
    padding: 13px;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--surface-muted);
  }

  .scope-title {
    font-size: 10px;
    font-weight: 800;
  }

  .scope-hint {
    margin: 3px 0 11px;
    color: var(--ink-faint);
    font-size: 8.5px;
    line-height: 1.5;
  }

  .form-error {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-bottom: 17px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 28%, var(--line));
    border-radius: 9px;
    background: var(--danger-soft);
    color: var(--danger);
    font-size: 10.5px;
    font-weight: 650;
    line-height: 1.5;
  }

  .end-warning {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    padding: 12px;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: var(--surface-muted);
    color: var(--ink-soft);
  }

  .end-warning p {
    font-size: 10.5px;
    line-height: 1.55;
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

  @media (max-width: 900px) {
    .delegation-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 600px) {
    .board-bar {
      align-items: flex-start;
      flex-direction: column;
    }

    .stats {
      padding-left: 2px;
    }

    .form-grid,
    .form-grid.three {
      grid-template-columns: 1fr;
    }
  }
</style>

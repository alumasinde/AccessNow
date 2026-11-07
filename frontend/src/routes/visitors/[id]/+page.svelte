<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { VisitDetail } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { toasts } from '$lib/stores/toast.svelte';
  import { formatDateTime, formatWindow, initials, statusMeta } from '$lib/format';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import Icon from '$lib/components/Icon.svelte';

  type Action = 'approve' | 'reject' | 'cancel';

  const visitID = $derived(Number(page.params.id));
  const allowed = $derived(session.can('visit.view'));
  const moduleOn = $derived(session.moduleOn('visitor_management'));
  const queryClient = useQueryClient();

  const detailQuery = createQuery(() => ({
    queryKey: ['visit', visitID],
    queryFn: async () => {
      const { data, error, response } = await api.GET('/api/v1/visits/{visitID}', {
        params: { path: { visitID } }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data as VisitDetail;
    },
    enabled: allowed && moduleOn && visitID > 0
  }));

  const visit = $derived(detailQuery.data?.visit);
  const visitor = $derived(detailQuery.data?.visitor);
  const status = $derived(visit ? statusMeta(visit.status) : null);

  let action = $state<Action | null>(null);
  let note = $state('');
  let textError = $state<string | null>(null);

  function refresh() {
    queryClient.invalidateQueries({ queryKey: ['visit', visitID] });
    queryClient.invalidateQueries({ queryKey: ['visits'] });
  }

  const decide = createMutation(() => ({
    mutationFn: async (input: { action: 'approve' | 'reject'; note: string }) => {
      const { data, error, response } = await api.POST('/api/v1/visits/{visitID}/decision', {
        params: { path: { visitID } },
        body: input
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: (_, input) => {
      toasts.success(input.action === 'approve' ? 'Visit approved' : 'Visit rejected', 'The decision was recorded.');
      action = null;
      note = '';
      refresh();
    },
    onError: (error: Error) => (textError = error.message)
  }));

  const cancel = createMutation(() => ({
    mutationFn: async (reason: string) => {
      const { data, error, response } = await api.POST('/api/v1/visits/{visitID}/cancel', {
        params: { path: { visitID } },
        body: { reason }
      });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: () => {
      toasts.success('Visit cancelled', 'The register has been updated.');
      action = null;
      note = '';
      refresh();
    },
    onError: (error: Error) => (textError = error.message)
  }));

  const move = createMutation(() => ({
    mutationFn: async (kind: 'check-in' | 'check-out') => {
      const path = kind === 'check-in' ? '/api/v1/visits/{visitID}/check-in' : '/api/v1/visits/{visitID}/check-out';
      const { data, error, response } = await api.POST(path, { params: { path: { visitID } } });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: (_, kind) => {
      toasts.success(kind === 'check-in' ? 'Visitor checked in' : 'Visitor checked out', 'The gate decision was recorded.');
      refresh();
    },
    onError: (error: Error) => toasts.error('The gate could not move the visitor', error.message)
  }));

  function openAction(next: Action) {
    action = next;
    note = '';
    textError = null;
  }

  function submitAction() {
    textError = null;
    if (action === 'approve' || action === 'reject') {
      decide.mutate({ action, note: note.trim() });
    } else if (action === 'cancel') {
      if (!note.trim()) {
        textError = 'A cancellation reason is required.';
        return;
      }
      cancel.mutate(note.trim());
    }
  }

  const busy = $derived(decide.isPending || cancel.isPending || move.isPending);
</script>

<svelte:head><title>{visit ? `${visit.visitor_name} · AccessNow` : 'Visit · AccessNow'}</title></svelte:head>

<div class="page">
  {#if !allowed || !moduleOn}
    <AccessDenied moduleOff={!moduleOn} />
  {:else if detailQuery.isPending}
    <div class="detail-loading">
      <span class="skeleton title-skeleton"></span>
      <span class="skeleton card-skeleton"></span>
    </div>
  {:else if detailQuery.isError || !visit || !visitor}
    <div class="load-error">
      <Icon name="circleExclamation" size="1.5em" />
      <h2>This visit could not be opened</h2>
      <p>{detailQuery.error?.message ?? 'It may have been removed, or the link is no longer valid.'}</p>
      <div class="row">
        <Button variant="secondary" href="/visitors" icon="arrowLeft">Back to register</Button>
        <Button onclick={() => detailQuery.refetch()}>Try again</Button>
      </div>
    </div>
  {:else}
    <PageHeader
      eyebrow={`${visit.pass_no} · ${visit.is_walk_in ? 'Walk-in' : 'Pre-registered'}`}
      title={visit.visitor_name}
      description={visit.purpose}
    >
      {#snippet actions()}
        <Button variant="ghost" href="/visitors" icon="arrowLeft">Register</Button>
        <Button variant="secondary" icon="refresh" loading={detailQuery.isFetching} onclick={refresh}>Refresh</Button>
        {#if visit.status === 'pending' && session.can('visit.approve')}
          <Button variant="danger" icon="xmark" onclick={() => openAction('reject')}>Reject</Button>
          <Button icon="check" onclick={() => openAction('approve')}>Approve</Button>
        {:else if visit.status === 'approved' && session.can('visit.update')}
          <Button icon="doorOpen" loading={move.isPending} onclick={() => move.mutate('check-in')}>Check in</Button>
        {:else if visit.status === 'on_site' && session.can('visit.update')}
          <Button icon="doorClosed" loading={move.isPending} onclick={() => move.mutate('check-out')}>Check out</Button>
        {/if}
      {/snippet}
    </PageHeader>

    <div class="status-banner {status?.tone}">
      <span class="status-icon"><Icon name={status?.icon ?? 'circleInfo'} size="1em" /></span>
      <div>
        <strong>{status?.label}</strong>
        <p>{status?.description}</p>
      </div>
      {#if (visit.status === 'pending' || visit.status === 'approved') && session.can('visit.cancel')}
        <Button variant="ghost" size="sm" onclick={() => openAction('cancel')}>Cancel visit</Button>
      {/if}
    </div>

    <div class="detail-grid">
      <div class="main-column">
        <Card title="Visit brief" eyebrow="The record">
          <div class="identity">
            <span class="big-avatar">{initials(visit.visitor_name)}</span>
            <div>
              <h3>{visit.visitor_name}</h3>
              <p>{visitor.visit_count} {visitor.visit_count === 1 ? 'visit' : 'visits'} on record · {visitor.status}</p>
            </div>
            <Badge tone={status?.tone ?? 'neutral'} icon={status?.icon}>{status?.label}</Badge>
          </div>

          <div class="details-grid">
            <div><span>Host</span><strong>{visit.host_name ?? 'Unassigned'}</strong></div>
            <div><span>Window</span><strong class="mono">{formatWindow(visit.expected_from, visit.expected_until, session.tenant?.timezone)}</strong></div>
            <div><span>People</span><strong class="mono">{visit.people_count}</strong></div>
            <div><span>Escort</span><strong>{visit.escort_required ? 'Required' : 'Not required'}</strong></div>
            <div><span>Badge</span><strong>{visit.badge_no ?? 'Not assigned'}</strong></div>
            <div><span>Pass status</span><strong>{visit.pass_status.replaceAll('_', ' ')}</strong></div>
          </div>

          <div class="contact-grid">
            <div><Icon name="envelope" size="0.8em" /><span>{visitor.email ?? 'No email captured'}</span></div>
            <div><Icon name="phone" size="0.8em" /><span>{visitor.phone ?? 'No phone captured'}</span></div>
            <div><Icon name="idCard" size="0.8em" /><span>Visitor #{visitor.id}</span></div>
          </div>
        </Card>

        {#if visit.host_response}
          <Card title="Host response" eyebrow="Approval note">
            <p class="host-response">“{visit.host_response}”</p>
          </Card>
        {/if}
      </div>

      <div class="side-column">
        <Card title="Movement" eyebrow="Timeline">
          <ol class="timeline">
            <li class="done">
              <span></span>
              <div><strong>Registered</strong><small>{formatDateTime(visit.created_at, session.tenant?.timezone)}</small></div>
            </li>
            <li class:done={Boolean(visit.checked_in_at)}>
              <span></span>
              <div><strong>Checked in</strong><small>{visit.checked_in_at ? formatDateTime(visit.checked_in_at, session.tenant?.timezone) : 'Not yet'}</small></div>
            </li>
            <li class:done={Boolean(visit.checked_out_at)}>
              <span></span>
              <div><strong>Checked out</strong><small>{visit.checked_out_at ? formatDateTime(visit.checked_out_at, session.tenant?.timezone) : 'Not yet'}</small></div>
            </li>
          </ol>
        </Card>

        <Card title="Route" eyebrow="Identifiers">
          <div class="route-list">
            <div><span>Visit</span><code>#{visit.id}</code></div>
            <div><span>Pass</span><code>{visit.pass_no}</code></div>
            <div><span>Visitor</span><code>#{visit.visitor_id}</code></div>
          </div>
        </Card>
      </div>
    </div>
  {/if}
</div>

<Modal
  open={action !== null}
  title={action === 'approve' ? 'Approve this visit' : action === 'reject' ? 'Reject this visit' : 'Cancel this visit'}
  description={action === 'cancel'
    ? 'The visit leaves the active register. The reason is kept in the audit trail.'
    : 'Your decision is recorded against the visit and the approval chain.'}
  onclose={() => { if (!busy) { action = null; textError = null; } }}
>
  <label class="field-label" for="action-note">
    {action === 'cancel' ? 'Cancellation reason' : 'Decision note'}
    {#if action === 'cancel'}<span class="required">*</span>{/if}
  </label>
  <textarea
    id="action-note"
    class="textarea"
    bind:value={note}
    maxlength="512"
    placeholder={action === 'cancel' ? 'Why is this visit being called off?' : 'Optional context for the record…'}
  ></textarea>
  {#if textError}<p class="field-error">{textError}</p>{/if}
  {#if action === 'reject'}<p class="reject-hint"><Icon name="circleInfo" size="0.78em" /> The visitor will not be able to check in.</p>{/if}

  {#snippet footer()}
    <Button variant="ghost" disabled={busy} onclick={() => (action = null)}>Go back</Button>
    <Button
      variant={action === 'reject' || action === 'cancel' ? 'danger' : 'primary'}
      loading={decide.isPending || cancel.isPending}
      onclick={submitAction}
      icon={action === 'approve' ? 'check' : 'xmark'}
    >
      {action === 'approve' ? 'Approve visit' : action === 'reject' ? 'Reject visit' : 'Cancel visit'}
    </Button>
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
    height: 280px;
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

  .status-banner.amber .status-icon {
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

  .status-banner div {
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

  .detail-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(280px, 0.62fr);
    gap: 18px;
    align-items: start;
  }

  .main-column,
  .side-column {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .identity {
    display: flex;
    align-items: center;
    gap: 13px;
    padding-bottom: 18px;
    border-bottom: 1px solid var(--line);
  }

  .big-avatar {
    display: grid;
    place-items: center;
    width: 54px;
    height: 54px;
    flex: 0 0 auto;
    border-radius: 15px;
    background: var(--brand-soft);
    color: var(--brand-strong);
    font-size: 13px;
    font-weight: 850;
  }

  .identity div {
    flex: 1;
  }

  .identity h3 {
    font-size: 19px;
    font-weight: 820;
  }

  .identity p {
    margin-top: 3px;
    color: var(--ink-faint);
    font-size: 10.5px;
  }

  .details-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 18px;
    padding: 20px 0;
  }

  .details-grid span {
    display: block;
    color: var(--ink-faint);
    font-size: 9px;
    font-weight: 800;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .details-grid strong {
    display: block;
    margin-top: 4px;
    color: var(--ink-soft);
    font-size: 11.5px;
    font-weight: 750;
  }

  .contact-grid {
    display: flex;
    flex-wrap: wrap;
    gap: 9px 18px;
    padding-top: 16px;
    border-top: 1px solid var(--line);
  }

  .contact-grid > div {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--ink-faint);
    font-size: 10.5px;
  }

  .host-response {
    color: var(--ink-soft);
    font-size: 13px;
    font-style: italic;
    line-height: 1.7;
  }

  .timeline {
    display: flex;
    flex-direction: column;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .timeline li {
    position: relative;
    display: flex;
    gap: 11px;
    padding-bottom: 20px;
  }

  .timeline li:last-child {
    padding-bottom: 0;
  }

  .timeline li > span {
    position: relative;
    z-index: 1;
    width: 10px;
    height: 10px;
    margin-top: 4px;
    flex: 0 0 auto;
    border: 2px solid var(--line-strong);
    border-radius: 50%;
    background: var(--surface);
  }

  .timeline li:not(:last-child)::before {
    content: '';
    position: absolute;
    top: 14px;
    left: 4px;
    width: 1px;
    height: calc(100% - 4px);
    background: var(--line);
  }

  .timeline li.done > span {
    border-color: var(--teal);
    background: var(--teal);
  }

  .timeline strong {
    display: block;
    color: var(--ink-soft);
    font-size: 10.5px;
    font-weight: 800;
  }

  .timeline small {
    display: block;
    margin-top: 2px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  .route-list {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }

  .route-list > div {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    font-size: 10.5px;
  }

  .route-list span {
    color: var(--ink-faint);
  }

  code {
    padding: 2px 6px;
    border-radius: 5px;
    background: var(--surface-muted);
    color: var(--ink-soft);
    font-size: 9.5px;
  }

  .required {
    color: var(--brand);
  }

  .reject-hint {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 9px;
    color: var(--danger);
    font-size: 10px;
  }

  @media (max-width: 900px) {
    .detail-grid {
      grid-template-columns: 1fr;
    }

    .details-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 560px) {
    .details-grid {
      grid-template-columns: 1fr;
      gap: 13px;
    }

    .status-banner {
      align-items: flex-start;
      flex-wrap: wrap;
    }
  }
</style>

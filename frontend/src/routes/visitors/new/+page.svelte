<script lang="ts">
  import { goto } from '$app/navigation';
  import { createMutation } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { VisitCreate } from '$lib/api/types';
  import { session } from '$lib/session.svelte';
  import { toasts } from '$lib/stores/toast.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Field from '$lib/components/Field.svelte';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const allowed = $derived(session.can('visit.create') && session.moduleOn('visitor_management'));

  let firstName = $state('');
  let lastName = $state('');
  let email = $state('');
  let phone = $state('');
  let idType = $state<NonNullable<VisitCreate['id_type']>>('national_id');
  let idNumber = $state('');
  let purpose = $state('');
  let peopleCount = $state(1);
  let escortRequired = $state(false);
  let hostId = $state(session.user?.id ?? 0);
  let companyId = $state('');
  let departmentId = $state('');
  let badgeNo = $state('');
  let expectedFrom = $state('');
  let expectedUntil = $state('');
  let origin = $state<NonNullable<VisitCreate['origin']>>('walk_in');
  let formError = $state<string | null>(null);

  const createVisit = createMutation(() => ({
    mutationFn: async (body: VisitCreate) => {
      const { data, error, response } = await api.POST('/api/v1/visits', { body });
      if (!response.ok || !data) throw new Error(errorMessage(error, response));
      return data;
    },
    onSuccess: (visit) => {
      toasts.success('Visit registered', `${visit.visitor_name} is now ${visit.status.replaceAll('_', ' ')}.`);
      goto(`/visitors/${visit.id}`);
    },
    onError: (error: Error) => {
      formError = error.message;
    }
  }));

  function numberOrZero(value: string): number {
    const parsed = Number(value);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
  }

  function submit() {
    formError = null;
    if (!firstName.trim() || !lastName.trim() || !purpose.trim()) {
      formError = 'First name, last name and purpose are required.';
      return;
    }
    if (expectedFrom && expectedUntil && new Date(expectedFrom) >= new Date(expectedUntil)) {
      formError = 'The expected arrival must be before the expected departure.';
      return;
    }
    const body: VisitCreate = {
      first_name: firstName.trim(),
      last_name: lastName.trim(),
      email: email.trim(),
      phone: phone.trim(),
      id_type: idType,
      id_number: idNumber.trim(),
      visitor_company_id: numberOrZero(companyId),
      host_user_id: numberOrZero(String(hostId)),
      purpose: purpose.trim(),
      people_count: Math.max(1, Number(peopleCount) || 1),
      escort_required: escortRequired,
      badge_no: badgeNo.trim(),
      department_id: numberOrZero(departmentId),
      expected_from: expectedFrom ? new Date(expectedFrom).toISOString() : null,
      expected_until: expectedUntil ? new Date(expectedUntil).toISOString() : null,
      origin
    };
    createVisit.mutate(body);
  }
</script>

<svelte:head><title>Register a visit · AccessNow</title></svelte:head>

<div class="page">
  {#if !allowed}
    <AccessDenied moduleOff={!session.moduleOn('visitor_management')} />
  {:else}
    <PageHeader
      eyebrow="New visitor"
      title="Register a visit"
      description="Create the person, the pass and the visit in one record. The host defaults to you."
    >
      {#snippet actions()}
        <Button variant="secondary" href="/visitors" icon="arrowLeft">Cancel</Button>
      {/snippet}
    </PageHeader>

    <form onsubmit={(event) => { event.preventDefault(); submit(); }}>
      <div class="layout">
        <div class="form-column">
          <Card title="Visitor identity" eyebrow="1 · The person">
            <div class="form-grid">
              <Field label="First name" forId="first-name" required>
                <input id="first-name" class="input" bind:value={firstName} autocomplete="given-name" placeholder="Amara" required />
              </Field>
              <Field label="Last name" forId="last-name" required>
                <input id="last-name" class="input" bind:value={lastName} autocomplete="family-name" placeholder="Okoye" required />
              </Field>
              <Field label="Email" forId="email" hint="Optional, used for arrival notices.">
                <input id="email" class="input" type="email" bind:value={email} autocomplete="email" placeholder="amara@company.com" />
              </Field>
              <Field label="Phone" forId="phone">
                <input id="phone" class="input" type="tel" bind:value={phone} autocomplete="tel" placeholder="+254 700 000 000" />
              </Field>
              <Field label="Document type" forId="id-type">
                <select id="id-type" class="select" bind:value={idType}>
                  <option value="national_id">National ID</option>
                  <option value="passport">Passport</option>
                  <option value="drivers_licence">Driver's licence</option>
                  <option value="work_permit">Work permit</option>
                  <option value="refugee_id">Refugee ID</option>
                  <option value="other">Other</option>
                </select>
              </Field>
              <Field label="Document number" forId="id-number" hint="Read from the document at the desk.">
                <input id="id-number" class="input" bind:value={idNumber} placeholder="Document number" />
              </Field>
            </div>
          </Card>

          <Card title="Visit details" eyebrow="2 · The reason">
            <div class="form-grid">
              <div class="span-2">
                <Field label="Purpose of visit" forId="purpose" required>
                  <input id="purpose" class="input" bind:value={purpose} placeholder="Site inspection, interview, delivery meeting…" required />
                </Field>
              </div>
              <Field label="Expected arrival" forId="from">
                <input id="from" class="input" type="datetime-local" bind:value={expectedFrom} />
              </Field>
              <Field label="Expected departure" forId="until">
                <input id="until" class="input" type="datetime-local" bind:value={expectedUntil} />
              </Field>
              <Field label="People" forId="people">
                <input id="people" class="input" type="number" min="1" max="50" bind:value={peopleCount} />
              </Field>
              <Field label="Origin" forId="origin">
                <select id="origin" class="select" bind:value={origin}>
                  <option value="walk_in">Walk-in</option>
                  <option value="pre_registered">Pre-registered</option>
                  <option value="online">Online</option>
                  <option value="api">API</option>
                </select>
              </Field>
            </div>

            <label class="switch-row" for="escort">
              <span>
                <strong>Escort required</strong>
                <small>The visitor must stay with their host while on site.</small>
              </span>
              <span class="switch">
                <input id="escort" type="checkbox" bind:checked={escortRequired} />
                <span class="track"><span class="thumb"></span></span>
              </span>
            </label>
          </Card>

          <Card title="Host and access" eyebrow="3 · The approval path">
            <div class="form-grid">
              <Field label="Host user ID" forId="host" hint="Defaults to you. Change it when another person is hosting.">
                <input id="host" class="input mono" type="number" min="1" bind:value={hostId} />
              </Field>
              <Field label="Badge number" forId="badge" hint="Leave blank if the desk assigns one on arrival.">
                <input id="badge" class="input" bind:value={badgeNo} placeholder="e.g. V-1042" />
              </Field>
            </div>

            <details class="advanced">
              <summary>
                <span><Icon name="building" size="0.78em" /> Department and company IDs</span>
                <span class="optional">Optional</span>
              </summary>
              <div class="form-grid advanced-grid">
                <Field label="Department ID" forId="department" hint="Numeric identifier from your workspace directory.">
                  <input id="department" class="input mono" type="number" min="1" bind:value={departmentId} placeholder="Not set" />
                </Field>
                <Field label="Visitor company ID" forId="company" hint="Numeric identifier from the company register.">
                  <input id="company" class="input mono" type="number" min="1" bind:value={companyId} placeholder="Not set" />
                </Field>
              </div>
            </details>
          </Card>
        </div>

        <aside class="summary-column">
          <Card eyebrow="Registration" title="Ready to file">
            <div class="summary-person">
              <span class="avatar">{firstName[0]?.toUpperCase() ?? '?'}{lastName[0]?.toUpperCase() ?? ''}</span>
              <div>
                <strong>{firstName || 'Visitor'} {lastName}</strong>
                <p>{purpose || 'Purpose not entered'}</p>
              </div>
            </div>
            <div class="summary-rows">
              <div><span>Host</span><strong>{session.user?.first_name} {session.user?.last_name}</strong></div>
              <div><span>People</span><strong class="mono">{peopleCount}</strong></div>
              <div><span>Window</span><strong>{expectedFrom ? 'Scheduled' : 'On arrival'}</strong></div>
              <div><span>Escort</span><strong>{escortRequired ? 'Required' : 'Not required'}</strong></div>
            </div>

            {#if formError}
              <div class="form-error" role="alert"><Icon name="circleExclamation" size="0.85em" /> {formError}</div>
            {/if}

            <Button type="submit" full size="lg" loading={createVisit.isPending} trailingIcon="arrowRight">
              {createVisit.isPending ? 'Registering visit' : 'Register visit'}
            </Button>
            <p class="submit-note">The visit starts in the approval path your workspace has configured.</p>
          </Card>

          <div class="flow-card">
            <p class="eyebrow">What happens next</p>
            <ol>
              <li><span>1</span><p><strong>Pending</strong><small>The host or an approver is asked to decide.</small></p></li>
              <li><span>2</span><p><strong>Approved</strong><small>The visitor can be checked in at the gate.</small></p></li>
              <li><span>3</span><p><strong>On site</strong><small>Arrival opens a movement; exit closes it.</small></p></li>
            </ol>
          </div>
        </aside>
      </div>
    </form>
  {/if}
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1.55fr) minmax(285px, 0.62fr);
    gap: 18px;
    align-items: start;
  }

  .form-column,
  .summary-column {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .summary-column {
    position: sticky;
    top: calc(var(--header-height) + 18px);
  }

  .form-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }

  .span-2 {
    grid-column: span 2;
  }

  .switch-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 18px;
    margin-top: 17px;
    padding: 14px;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--surface-muted);
    cursor: pointer;
  }

  .switch-row strong {
    display: block;
    font-size: 11.5px;
    font-weight: 800;
  }

  .switch-row small {
    display: block;
    margin-top: 2px;
    color: var(--ink-faint);
    font-size: 10px;
  }

  .switch input {
    position: absolute;
    opacity: 0;
  }

  .track {
    display: block;
    width: 38px;
    height: 22px;
    padding: 3px;
    border-radius: 999px;
    background: var(--line-strong);
    transition: background 160ms ease;
  }

  .thumb {
    display: block;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #fff;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2);
    transition: transform 160ms ease;
  }

  .switch input:checked + .track {
    background: var(--teal);
  }

  .switch input:checked + .track .thumb {
    transform: translateX(16px);
  }

  .advanced {
    margin-top: 17px;
    border-top: 1px solid var(--line);
    padding-top: 15px;
  }

  .advanced summary {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    color: var(--ink-soft);
    font-size: 11px;
    font-weight: 750;
    list-style: none;
    cursor: pointer;
  }

  .advanced summary::-webkit-details-marker {
    display: none;
  }

  .advanced summary > span:first-child {
    display: flex;
    align-items: center;
    gap: 7px;
  }

  .optional {
    color: var(--ink-faint);
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  .advanced-grid {
    margin-top: 14px;
  }

  .summary-person {
    display: flex;
    align-items: center;
    gap: 11px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--line);
  }

  .avatar {
    display: grid;
    place-items: center;
    width: 43px;
    height: 43px;
    flex: 0 0 auto;
    border-radius: 12px;
    background: var(--brand-soft);
    color: var(--brand-strong);
    font-size: 11px;
    font-weight: 850;
  }

  .summary-person strong {
    display: block;
    font-size: 12.5px;
    font-weight: 800;
  }

  .summary-person p {
    max-width: 170px;
    margin-top: 2px;
    overflow: hidden;
    color: var(--ink-faint);
    font-size: 10px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .summary-rows {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 15px 0;
  }

  .summary-rows > div {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    color: var(--ink-faint);
    font-size: 10.5px;
  }

  .summary-rows strong {
    color: var(--ink-soft);
    font-size: 10.5px;
    font-weight: 750;
  }

  .form-error {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    margin-bottom: 12px;
    padding: 10px;
    border-radius: 8px;
    background: var(--danger-soft);
    color: var(--danger);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .submit-note {
    margin-top: 9px;
    color: var(--ink-faint);
    font-size: 9.5px;
    line-height: 1.5;
    text-align: center;
  }

  .flow-card {
    padding: 18px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--surface-muted);
  }

  .flow-card ol {
    display: flex;
    flex-direction: column;
    gap: 13px;
    margin: 14px 0 0;
    padding: 0;
    list-style: none;
  }

  .flow-card li {
    display: flex;
    align-items: flex-start;
    gap: 10px;
  }

  .flow-card li > span {
    display: grid;
    place-items: center;
    width: 21px;
    height: 21px;
    flex: 0 0 auto;
    border: 1px solid var(--line-strong);
    border-radius: 50%;
    background: var(--surface);
    color: var(--ink-faint);
    font-size: 8.5px;
    font-weight: 800;
  }

  .flow-card strong {
    display: block;
    color: var(--ink-soft);
    font-size: 10.5px;
    font-weight: 800;
  }

  .flow-card small {
    display: block;
    margin-top: 2px;
    color: var(--ink-faint);
    font-size: 9.5px;
    line-height: 1.45;
  }

  @media (max-width: 980px) {
    .layout {
      grid-template-columns: 1fr;
    }

    .summary-column {
      position: static;
    }
  }

  @media (max-width: 620px) {
    .form-grid {
      grid-template-columns: 1fr;
    }

    .span-2 {
      grid-column: auto;
    }
  }
</style>

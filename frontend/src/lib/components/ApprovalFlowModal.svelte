<script lang="ts">
  import { createMutation, useQueryClient } from '@tanstack/svelte-query';
  import { api, errorMessage } from '$lib/api/client';
  import type { ApprovalFlowBody, ApprovalFlowView } from '$lib/api/types';
  import { toasts } from '$lib/stores/toast.svelte';
  import { approverLabel, modeLabel } from '$lib/approval';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';

  type StepDraft = {
    name: string;
    approverType: string;
    roleKey: string;
    userId: string;
    mode: string;
    requiresAll: boolean;
    allowSelf: boolean;
    requireComment: boolean;
    memo: string;
    slaHours: string;
    escalateAfterHours: string;
    escalateRole: string;
    escalateUserId: string;
  };

  let {
    open,
    flow = null,
    onclose,
    onsaved
  }: {
    open: boolean;
    flow?: ApprovalFlowView | null;
    onclose: () => void;
    onsaved?: () => void;
  } = $props();

  const queryClient = useQueryClient();

  let name = $state('');
  let description = $state('');
  let status = $state<'active' | 'inactive'>('active');
  let priority = $state('0');
  let passTypeID = $state('');
  let categoryKey = $state('');
  let departmentID = $state('');
  let siteID = $state('');
  let minValue = $state('');
  let maxValue = $state('');
  let currency = $state('USD');
  let isDefault = $state(false);
  let outsideHoursOnly = $state(false);
  let steps = $state<StepDraft[]>([]);
  let error = $state<string | null>(null);

  function blankStep(): StepDraft {
    return {
      name: '',
      approverType: 'role',
      roleKey: '',
      userId: '',
      mode: 'sequential',
      requiresAll: true,
      allowSelf: false,
      requireComment: false,
      memo: '',
      slaHours: '',
      escalateAfterHours: '',
      escalateRole: '',
      escalateUserId: ''
    };
  }

  function toDraft(step: NonNullable<ApprovalFlowView['steps']>[number]): StepDraft {
    return {
      name: step.name,
      approverType: step.approver_type,
      roleKey: step.approver_role_key ?? '',
      userId: step.approver_user_id ? String(step.approver_user_id) : '',
      mode: step.mode,
      requiresAll: step.requires_all,
      allowSelf: step.allow_self_approval,
      requireComment: step.require_comment,
      memo: step.memo ?? '',
      slaHours: step.sla_hours ? String(step.sla_hours) : '',
      escalateAfterHours: step.escalate_after_hours ? String(step.escalate_after_hours) : '',
      escalateRole: step.escalate_to_role_key ?? '',
      escalateUserId: step.escalate_to_user_id ? String(step.escalate_to_user_id) : ''
    };
  }

  $effect(() => {
    if (!open) return;
    const current = flow;
    name = current?.name ?? '';
    description = current?.description ?? '';
    status = current?.status === 'inactive' ? 'inactive' : 'active';
    priority = String(current?.priority ?? 0);
    passTypeID = current?.pass_type_id ? String(current.pass_type_id) : '';
    categoryKey = current?.category_key ?? '';
    departmentID = current?.department_id ? String(current.department_id) : '';
    siteID = current?.site_id ? String(current.site_id) : '';
    minValue = current?.min_value !== null && current?.min_value !== undefined ? String(current.min_value) : '';
    maxValue = current?.max_value !== null && current?.max_value !== undefined ? String(current.max_value) : '';
    currency = current?.currency ?? 'USD';
    isDefault = current?.is_default ?? false;
    outsideHoursOnly = current?.outside_hours_only ?? false;
    steps = current?.steps?.length ? current.steps.map(toDraft) : [blankStep()];
    error = null;
  });

  function numberOrNull(value: string): number | null {
    if (!value.trim()) return null;
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : null;
  }

  function integerOrZero(value: string): number {
    const parsed = Number.parseInt(value, 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
  }

  function addStep() {
    steps = [...steps, blankStep()];
  }

  function removeStep(index: number) {
    if (steps.length === 1) return;
    steps = steps.filter((_, i) => i !== index);
  }

  const save = createMutation(() => ({
    mutationFn: async (body: ApprovalFlowBody) => {
      if (flow) {
        const { data, error: requestError, response } = await api.PUT('/api/v1/approvals/flows/{flowID}', {
          params: { path: { flowID: flow.id } },
          body
        });
        if (!response.ok || !data) throw new Error(errorMessage(requestError, response));
        return data;
      }
      const { data, error: requestError, response } = await api.POST('/api/v1/approvals/flows', {
        body
      });
      if (!response.ok || !data) throw new Error(errorMessage(requestError, response));
      return data;
    },
    onSuccess: (saved) => {
      toasts.success(flow ? 'Flow updated' : 'Flow created', `${saved.name} is ready to route new passes.`);
      queryClient.invalidateQueries({ queryKey: ['approval-flows'] });
      onsaved?.();
      onclose();
    },
    onError: (saveError: Error) => (error = saveError.message)
  }));

  function submit() {
    error = null;
    if (!name.trim()) {
      error = 'A flow needs a name.';
      return;
    }
    if (steps.length === 0) {
      error = 'A flow needs at least one approval step.';
      return;
    }
    for (const [index, step] of steps.entries()) {
      if (!step.name.trim()) {
        error = `Step ${index + 1} needs a name.`;
        return;
      }
      if (step.approverType === 'role' && !step.roleKey.trim()) {
        error = `Step ${index + 1} needs a role key.`;
        return;
      }
      if (step.approverType === 'user' && integerOrZero(step.userId) === 0) {
        error = `Step ${index + 1} needs a user ID.`;
        return;
      }
      if (numberOrNull(step.slaHours) !== null && Number(step.slaHours) < 1) {
        error = `Step ${index + 1} has an invalid SLA.`;
        return;
      }
    }
    if (currency.trim() && currency.trim().length !== 3) {
      error = 'Currency must be a three-letter code, such as USD or INR.';
      return;
    }
    const min = numberOrNull(minValue);
    const max = numberOrNull(maxValue);
    if (min !== null && max !== null && max < min) {
      error = 'The maximum value cannot be below the minimum value.';
      return;
    }

    const body: ApprovalFlowBody = {
      name: name.trim(),
      description: description.trim(),
      status,
      priority: Math.max(0, integerOrZero(priority)),
      pass_type_id: integerOrZero(passTypeID),
      category_key: categoryKey.trim(),
      department_id: integerOrZero(departmentID),
      site_id: integerOrZero(siteID),
      min_value: min,
      max_value: max,
      currency: currency.trim().toUpperCase(),
      is_default: isDefault,
      outside_hours_only: outsideHoursOnly,
      steps: steps.map((step, index) => ({
        step_no: index + 1,
        name: step.name.trim(),
        approver_type: step.approverType,
        approver_role_key: step.roleKey.trim(),
        approver_user_id: integerOrZero(step.userId),
        mode: step.mode,
        requires_all: step.requiresAll,
        sla_hours: numberOrNull(step.slaHours),
        escalate_after_hours: numberOrNull(step.escalateAfterHours),
        escalate_to_role_key: step.escalateRole.trim(),
        escalate_to_user_id: integerOrZero(step.escalateUserId),
        allow_self_approval: step.allowSelf,
        require_comment: step.requireComment,
        memo: step.memo.trim()
      }))
    };
    save.mutate(body);
  }
</script>

<Modal
  {open}
  title={flow ? `Edit ${flow.name}` : 'New approval flow'}
  description="A flow matches passes to an ordered chain. Existing chains keep the route they were frozen under."
  wide
  onclose={() => { if (!save.isPending) onclose(); }}
>
  {#if error}
    <div class="form-error" role="alert"><Icon name="circleExclamation" size="0.85em" /> {error}</div>
  {/if}

  <section class="form-section">
    <div class="section-heading">
      <div><h3>Rule identity</h3><p>How this route is named and whether the matcher can pick it.</p></div>
      <span class="step-count">{steps.length} {steps.length === 1 ? 'step' : 'steps'}</span>
    </div>
    <div class="form-grid">
      <Field label="Flow name" forId="flow-name" required class="span-2">
        <input id="flow-name" class="input" bind:value={name} maxlength="128" placeholder="e.g. High-value visitor approval" />
      </Field>
      <Field label="Description" forId="flow-description" hint="Shown to flow editors, not to approvers." class="span-2">
        <input id="flow-description" class="input" bind:value={description} maxlength="512" placeholder="What this route is for…" />
      </Field>
      <Field label="Status" forId="flow-status">
        <select id="flow-status" class="select" bind:value={status}>
          <option value="active">Active</option>
          <option value="inactive">Inactive</option>
        </select>
      </Field>
      <Field label="Priority" forId="flow-priority" hint="Higher numbers win when several flows match.">
        <input id="flow-priority" class="input" type="number" min="0" bind:value={priority} />
      </Field>
    </div>
  </section>

  <section class="form-section">
    <div class="section-heading">
      <div><h3>Matching band</h3><p>All filled conditions narrow the passes this rule can pick up.</p></div>
    </div>
    <div class="form-grid">
      <Field label="Pass type ID" forId="flow-pass-type" hint="Blank matches any pass type.">
        <input id="flow-pass-type" class="input" type="number" min="1" bind:value={passTypeID} placeholder="Any" />
      </Field>
      <Field label="Category key" forId="flow-category" hint="Blank matches any category.">
        <input id="flow-category" class="input" bind:value={categoryKey} maxlength="32" placeholder="Any category" />
      </Field>
      <Field label="Department ID" forId="flow-department">
        <input id="flow-department" class="input" type="number" min="1" bind:value={departmentID} placeholder="Any" />
      </Field>
      <Field label="Site ID" forId="flow-site">
        <input id="flow-site" class="input" type="number" min="1" bind:value={siteID} placeholder="Any" />
      </Field>
      <Field label="Minimum value" forId="flow-min">
        <input id="flow-min" class="input" type="number" min="0" step="0.01" bind:value={minValue} placeholder="No minimum" />
      </Field>
      <Field label="Maximum value" forId="flow-max">
        <input id="flow-max" class="input" type="number" min="0" step="0.01" bind:value={maxValue} placeholder="No maximum" />
      </Field>
      <Field label="Currency" forId="flow-currency" hint="Three-letter ISO code.">
        <input id="flow-currency" class="input" bind:value={currency} maxlength="3" placeholder="USD" />
      </Field>
      <div class="toggle-stack">
        <label class="toggle">
          <input type="checkbox" bind:checked={isDefault} />
          <span class="track"><span></span></span>
          <span><strong>Default rule</strong><small>Fallback when nothing else matches</small></span>
        </label>
        <label class="toggle">
          <input type="checkbox" bind:checked={outsideHoursOnly} />
          <span class="track"><span></span></span>
          <span><strong>Outside hours only</strong><small>Route after-hours passes</small></span>
        </label>
      </div>
    </div>
  </section>

  <section class="form-section steps-section">
    <div class="section-heading">
      <div><h3>Approval chain</h3><p>Steps run in order. Every step needs somebody the system can resolve when a pass arrives.</p></div>
      <Button variant="secondary" size="sm" icon="plus" onclick={addStep}>Add step</Button>
    </div>

    <div class="steps">
      {#each steps as step, index}
        <div class="step-card">
          <div class="step-head">
            <span class="step-number">{index + 1}</span>
            <div class="step-title">
              <strong>{step.name.trim() || `Step ${index + 1}`}</strong>
              <small>{approverLabel(step.approverType)} · {modeLabel(step.mode)}</small>
            </div>
            <Button
              variant="ghost"
              size="sm"
              icon="xmark"
              ariaLabel={`Remove step ${index + 1}`}
              disabled={steps.length === 1}
              onclick={() => removeStep(index)}
            />
          </div>

          <div class="step-grid">
            <Field label="Step name" forId={`step-name-${index}`} required class="span-2">
              <input id={`step-name-${index}`} class="input" bind:value={step.name} maxlength="128" placeholder="e.g. Department head" />
            </Field>
            <Field label="Approver type" forId={`step-type-${index}`}>
              <select id={`step-type-${index}`} class="select" bind:value={step.approverType}>
                <option value="role">Role</option>
                <option value="user">Named person</option>
                <option value="supervisor">Supervisor</option>
                <option value="host">Host</option>
                <option value="department_head">Department head</option>
                <option value="requester_manager">Requester’s manager</option>
              </select>
            </Field>
            {#if step.approverType === 'role'}
              <Field label="Role key" forId={`step-role-${index}`} required>
                <input id={`step-role-${index}`} class="input" bind:value={step.roleKey} maxlength="64" placeholder="e.g. approver" />
              </Field>
            {:else if step.approverType === 'user'}
              <Field label="User ID" forId={`step-user-${index}`} required>
                <input id={`step-user-${index}`} class="input" type="number" min="1" bind:value={step.userId} placeholder="Numeric user ID" />
              </Field>
            {:else}
              <Field label="Mode" forId={`step-mode-${index}`}>
                <select id={`step-mode-${index}`} class="select" bind:value={step.mode}>
                  <option value="sequential">Sequential — one approver at a time</option>
                  <option value="parallel">Parallel — ask everyone</option>
                  <option value="any_one">Any one — first answer wins</option>
                </select>
              </Field>
            {/if}
            {#if step.approverType === 'role' || step.approverType === 'user'}
              <Field label="Mode" forId={`step-mode-${index}`}>
                <select id={`step-mode-${index}`} class="select" bind:value={step.mode}>
                  <option value="sequential">Sequential — one approver at a time</option>
                  <option value="parallel">Parallel — ask everyone</option>
                  <option value="any_one">Any one — first answer wins</option>
                </select>
              </Field>
            {/if}
            <Field label="SLA hours" forId={`step-sla-${index}`} hint="Blank means no deadline.">
              <input id={`step-sla-${index}`} class="input" type="number" min="1" bind:value={step.slaHours} placeholder="No SLA" />
            </Field>
          </div>

          <div class="step-toggles">
            <label class="mini-check">
              <input type="checkbox" bind:checked={step.requiresAll} />
              <span><Icon name="check" size="0.6em" /></span>
              Requires every assignee
            </label>
            <label class="mini-check">
              <input type="checkbox" bind:checked={step.requireComment} />
              <span><Icon name="check" size="0.6em" /></span>
              Comment required
            </label>
            <label class="mini-check">
              <input type="checkbox" bind:checked={step.allowSelf} />
              <span><Icon name="check" size="0.6em" /></span>
              Allow self-approval
            </label>
          </div>

          <details class="advanced">
            <summary>Step instructions and escalation</summary>
            <div class="step-grid advanced-grid">
              <Field label="Memo for approvers" forId={`step-memo-${index}`} class="span-2">
                <input id={`step-memo-${index}`} class="input" bind:value={step.memo} maxlength="512" placeholder="What should the approver check?" />
              </Field>
              <Field label="Escalate after hours" forId={`step-esc-hours-${index}`}>
                <input id={`step-esc-hours-${index}`} class="input" type="number" min="1" bind:value={step.escalateAfterHours} placeholder="No escalation" />
              </Field>
              <Field label="Escalate to role" forId={`step-esc-role-${index}`}>
                <input id={`step-esc-role-${index}`} class="input" bind:value={step.escalateRole} maxlength="64" placeholder="Role key" />
              </Field>
              <Field label="Escalate to user ID" forId={`step-esc-user-${index}`}>
                <input id={`step-esc-user-${index}`} class="input" type="number" min="1" bind:value={step.escalateUserId} placeholder="Numeric user ID" />
              </Field>
            </div>
          </details>
        </div>
      {/each}
    </div>
  </section>

  {#snippet footer()}
    <Button variant="ghost" disabled={save.isPending} onclick={onclose}>Cancel</Button>
    <Button icon="check" loading={save.isPending} onclick={submit}>{flow ? 'Save flow' : 'Create flow'}</Button>
  {/snippet}
</Modal>

<style>
  .form-error {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-bottom: 18px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 28%, var(--line));
    border-radius: 9px;
    background: var(--danger-soft);
    color: var(--danger);
    font-size: 10.5px;
    font-weight: 650;
    line-height: 1.5;
  }

  .form-section {
    padding-bottom: 20px;
    margin-bottom: 20px;
    border-bottom: 1px solid var(--line);
  }

  .form-section:last-of-type {
    margin-bottom: 0;
    padding-bottom: 0;
    border-bottom: 0;
  }

  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 14px;
    margin-bottom: 13px;
  }

  .section-heading h3 {
    font-size: 12px;
    font-weight: 800;
  }

  .section-heading p {
    margin-top: 3px;
    color: var(--ink-faint);
    font-size: 9.5px;
    line-height: 1.5;
  }

  .step-count {
    flex: 0 0 auto;
    padding: 3px 7px;
    border-radius: 999px;
    background: var(--surface-muted);
    color: var(--ink-faint);
    font-size: 8.5px;
    font-weight: 800;
  }

  .form-grid,
  .step-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 13px;
  }

  .form-grid :global(.span-2),
  .step-grid :global(.span-2) {
    grid-column: span 2;
  }

  .toggle-stack {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding-top: 3px;
  }

  .toggle {
    display: flex;
    align-items: center;
    gap: 9px;
    cursor: pointer;
  }

  .toggle input {
    position: absolute;
    opacity: 0;
  }

  .track {
    position: relative;
    width: 30px;
    height: 17px;
    flex: 0 0 auto;
    border-radius: 999px;
    background: var(--line-strong);
    transition: background 150ms ease;
  }

  .track span {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 13px;
    height: 13px;
    border-radius: 50%;
    background: #fff;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2);
    transition: transform 150ms ease;
  }

  .toggle input:checked + .track {
    background: var(--teal);
  }

  .toggle input:checked + .track span {
    transform: translateX(13px);
  }

  .toggle strong {
    display: block;
    font-size: 9.5px;
    font-weight: 750;
  }

  .toggle small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 8.5px;
  }

  .steps {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .step-card {
    padding: 13px;
    border: 1px solid var(--line);
    border-radius: 11px;
    background: var(--surface-muted);
  }

  .step-head {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-bottom: 12px;
  }

  .step-number {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    flex: 0 0 auto;
    border-radius: 7px;
    background: var(--brand-soft);
    color: var(--brand-strong);
    font-size: 9px;
    font-weight: 850;
  }

  .step-title {
    flex: 1;
    min-width: 0;
  }

  .step-title strong {
    display: block;
    overflow: hidden;
    font-size: 10.5px;
    font-weight: 800;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .step-title small {
    display: block;
    margin-top: 1px;
    color: var(--ink-faint);
    font-size: 8.5px;
  }

  .step-toggles {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 15px;
    margin-top: 11px;
  }

  .mini-check {
    display: flex;
    align-items: center;
    gap: 5px;
    color: var(--ink-soft);
    font-size: 8.5px;
    font-weight: 650;
    cursor: pointer;
  }

  .mini-check input {
    position: absolute;
    opacity: 0;
  }

  .mini-check > span {
    display: grid;
    place-items: center;
    width: 14px;
    height: 14px;
    border: 1px solid var(--line-strong);
    border-radius: 4px;
    background: var(--surface);
    color: transparent;
  }

  .mini-check input:checked + span {
    border-color: var(--teal);
    background: var(--teal);
    color: #fff;
  }

  .advanced {
    margin-top: 11px;
    padding-top: 10px;
    border-top: 1px solid var(--line);
  }

  .advanced summary {
    color: var(--ink-faint);
    font-size: 8.5px;
    font-weight: 750;
    cursor: pointer;
  }

  .advanced-grid {
    margin-top: 11px;
  }

  @media (max-width: 560px) {
    .form-grid,
    .step-grid {
      grid-template-columns: 1fr;
    }

    .form-grid :global(.span-2),
    .step-grid :global(.span-2) {
      grid-column: span 1;
    }
  }
</style>

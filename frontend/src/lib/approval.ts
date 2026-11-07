import type { Tone } from '$lib/format';
import type { IconName } from '$lib/icons';

export const REQUEST_STATUSES = [
  'not_required',
  'pending',
  'in_progress',
  'approved',
  'rejected',
  'returned',
  'cancelled',
  'expired',
  'escalated'
] as const;

const requestStatuses: Record<string, { label: string; tone: Tone; icon: IconName; description: string }> = {
  not_required: {
    label: 'Not required',
    tone: 'neutral',
    icon: 'minus',
    description: 'No approval chain was needed for this pass'
  },
  pending: {
    label: 'Pending',
    tone: 'amber',
    icon: 'clock',
    description: 'The chain has been filed and is starting'
  },
  in_progress: {
    label: 'In progress',
    tone: 'teal',
    icon: 'stamp',
    description: 'An approver currently owes this chain an answer'
  },
  approved: {
    label: 'Approved',
    tone: 'success',
    icon: 'circleCheck',
    description: 'Every required step answered yes'
  },
  rejected: {
    label: 'Rejected',
    tone: 'danger',
    icon: 'circleX',
    description: 'An approver declined the request'
  },
  returned: {
    label: 'Returned',
    tone: 'amber',
    icon: 'arrowLeft',
    description: 'The requester needs to correct and file it again'
  },
  cancelled: {
    label: 'Cancelled',
    tone: 'neutral',
    icon: 'ban',
    description: 'Withdrawn before the chain finished'
  },
  expired: {
    label: 'Expired',
    tone: 'neutral',
    icon: 'clock',
    description: 'The chain passed its deadline'
  },
  escalated: {
    label: 'Escalated',
    tone: 'violet',
    icon: 'arrowRight',
    description: 'The ask moved to another approver'
  }
};

const askStatuses: Record<string, { label: string; tone: Tone; icon: IconName }> = {
  pending: { label: 'Waiting', tone: 'amber', icon: 'clock' },
  approved: { label: 'Approved', tone: 'success', icon: 'check' },
  auto_approved: { label: 'Auto-approved', tone: 'success', icon: 'check' },
  rejected: { label: 'Rejected', tone: 'danger', icon: 'xmark' },
  returned: { label: 'Returned', tone: 'amber', icon: 'arrowLeft' },
  skipped: { label: 'Skipped', tone: 'neutral', icon: 'minus' },
  delegated: { label: 'Delegated', tone: 'violet', icon: 'users' },
  expired: { label: 'Expired', tone: 'neutral', icon: 'clock' },
  recalled: { label: 'Recalled', tone: 'neutral', icon: 'refresh' }
};

export function approvalStatusMeta(status: string) {
  return (
    requestStatuses[status] ?? {
      label: status.replaceAll('_', ' '),
      tone: 'neutral' as Tone,
      icon: 'circleInfo' as IconName,
      description: ''
    }
  );
}

export function askStatusMeta(status: string) {
  return (
    askStatuses[status] ?? {
      label: status.replaceAll('_', ' '),
      tone: 'neutral' as Tone,
      icon: 'circleInfo' as IconName
    }
  );
}

export function modeLabel(mode: string): string {
  if (mode === 'any_one') return 'Any one';
  if (mode === 'parallel') return 'Parallel';
  if (mode === 'sequential') return 'Sequential';
  return mode;
}

export function approverLabel(type: string): string {
  const labels: Record<string, string> = {
    role: 'Role',
    user: 'Named person',
    supervisor: 'Supervisor',
    host: 'Host',
    department_head: 'Department head',
    requester_manager: 'Requester’s manager'
  };
  return labels[type] ?? type.replaceAll('_', ' ');
}

export const SOURCE_MODULES = [
  { key: 'visitor_management', label: 'Visitors' },
  { key: 'vehicle_tracking', label: 'Vehicles' },
  { key: 'material_logistics', label: 'Material' },
  { key: 'staff_outpass', label: 'Staff outpass' },
  { key: 'contractor_access', label: 'Contractors' }
] as const;

export function sourceModuleLabel(key: string): string {
  return SOURCE_MODULES.find((module) => module.key === key)?.label ?? key.replaceAll('_', ' ');
}

export function formatValue(amount?: number | null, currency?: string | null): string {
  if (amount === null || amount === undefined) return 'No value band';
  try {
    return new Intl.NumberFormat('en-GB', {
      style: 'currency',
      currency: currency || 'USD',
      maximumFractionDigits: 2
    }).format(amount);
  } catch {
    return `${currency || ''} ${amount.toFixed(2)}`.trim();
  }
}

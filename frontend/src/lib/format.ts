import type { IconName } from '$lib/icons';
import type { VisitStatus } from '$lib/api/types';

export type Tone = 'neutral' | 'brand' | 'teal' | 'amber' | 'danger' | 'success' | 'violet';

const dateTime = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit'
});

const dateOnly = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  year: 'numeric'
});

const timeOnly = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit'
});

export function formatDateTime(value?: string | null, timezone?: string): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  try {
    return dateTime.format(date);
  } catch {
    return dateOnly.format(date);
  }
}

export function formatDate(value?: string | null, timezone?: string): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  try {
    return new Intl.DateTimeFormat('en-GB', {
      day: '2-digit',
      month: 'short',
      year: 'numeric',
      ...(timezone ? { timeZone: timezone } : {})
    }).format(date);
  } catch {
    return dateOnly.format(date);
  }
}

export function formatTime(value?: string | null, timezone?: string): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  try {
    return new Intl.DateTimeFormat('en-GB', {
      hour: '2-digit',
      minute: '2-digit',
      ...(timezone ? { timeZone: timezone } : {})
    }).format(date);
  } catch {
    return timeOnly.format(date);
  }
}

export function formatWindow(from?: string | null, until?: string | null, timezone?: string): string {
  if (!from && !until) return 'Not scheduled';
  if (from && until) return `${formatTime(from, timezone)} – ${formatTime(until, timezone)}`;
  return from ? `From ${formatTime(from, timezone)}` : `Until ${formatTime(until, timezone)}`;
}

export function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? '')
    .join('');
}

export function titleCase(value: string): string {
  return value
    .replaceAll('_', ' ')
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function relativeTime(value?: string | null): string {
  if (!value) return '—';
  const date = new Date(value);
  const seconds = Math.round((date.getTime() - Date.now()) / 1000);
  if (Number.isNaN(seconds)) return '—';
  const formatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
  const ranges: [Intl.RelativeTimeFormatUnit, number][] = [
    ['second', 60],
    ['minute', 60],
    ['hour', 24],
    ['day', 7],
    ['week', 4.35],
    ['month', 12]
  ];
  let amount = seconds;
  for (const [unit, size] of ranges) {
    if (Math.abs(amount) < size) return formatter.format(Math.round(amount), unit);
    amount /= size;
  }
  return formatter.format(Math.round(amount), 'year');
}

export type StatusMeta = { label: string; tone: Tone; icon: IconName; description: string };

const statuses: Record<VisitStatus, StatusMeta> = {
  pending: {
    label: 'Pending',
    tone: 'amber',
    icon: 'clock',
    description: 'Waiting for a host or desk decision'
  },
  approved: {
    label: 'Approved',
    tone: 'teal',
    icon: 'circleCheck',
    description: 'Cleared to arrive at site'
  },
  on_site: {
    label: 'On site',
    tone: 'success',
    icon: 'doorOpen',
    description: 'Checked in and currently on site'
  },
  completed: {
    label: 'Completed',
    tone: 'neutral',
    icon: 'check',
    description: 'Visit finished and checked out'
  },
  rejected: {
    label: 'Rejected',
    tone: 'danger',
    icon: 'circleX',
    description: 'Access was declined'
  },
  cancelled: {
    label: 'Cancelled',
    tone: 'neutral',
    icon: 'ban',
    description: 'Visit was called off'
  },
  expired: {
    label: 'Expired',
    tone: 'neutral',
    icon: 'clock',
    description: 'The expected window passed'
  },
  no_show: {
    label: 'No show',
    tone: 'neutral',
    icon: 'circleExclamation',
    description: 'Approved but never arrived'
  }
};

export function statusMeta(status: string): StatusMeta {
  return statuses[status as VisitStatus] ?? {
    label: titleCase(status),
    tone: 'neutral',
    icon: 'circleInfo',
    description: ''
  };
}

export function outcomeTone(outcome?: string): Tone {
  if (!outcome) return 'neutral';
  if (['allowed', 'granted', 'open', 'approved'].includes(outcome)) return 'success';
  if (['denied', 'blocked', 'refused', 'rejected'].includes(outcome)) return 'danger';
  if (['review', 'manual', 'override'].includes(outcome)) return 'amber';
  return 'neutral';
}

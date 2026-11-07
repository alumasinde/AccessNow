import type { components } from './schema';

export type SessionPayload = components['schemas']['CurrentSession'];
export type Visit = components['schemas']['VisitView'];
export type VisitDetail = components['schemas']['VisitDetail'];
export type VisitCreate = components['schemas']['VisitCreateRequest'];
export type VisitStatus =
  | 'pending'
  | 'approved'
  | 'on_site'
  | 'completed'
  | 'rejected'
  | 'cancelled'
  | 'expired'
  | 'no_show';

export const VISIT_STATUSES = [
  'pending',
  'approved',
  'on_site',
  'completed',
  'rejected',
  'cancelled',
  'expired',
  'no_show'
] as const satisfies readonly VisitStatus[];

export type ApprovalInboxItem = components['schemas']['ApprovalInboxItem'];
export type ApprovalChainSummary = components['schemas']['ApprovalChainSummary'];
export type ApprovalChainView = components['schemas']['ApprovalChainView'];
export type ApprovalFlowView = components['schemas']['ApprovalFlowView'];
export type ApprovalFlowBody = components['schemas']['ApprovalFlowBody'];
export type ApprovalDelegationView = components['schemas']['ApprovalDelegationView'];
export type ApprovalDelegationBody = components['schemas']['ApprovalDelegationBody'];
export type ApprovalDecisionRequest = components['schemas']['ApprovalDecisionRequest'];

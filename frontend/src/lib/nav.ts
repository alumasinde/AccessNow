import type { IconName } from '$lib/icons';

export type NavItem = {
  label: string;
  href: string;
  icon: IconName;
  permission: string;
  module: string;
  description: string;
};

export type NavGroup = {
  label: string;
  items: NavItem[];
};

export const navigation: NavGroup[] = [
  {
    label: 'Operations',
    items: [
      {
        label: 'Visitors',
        href: '/visitors',
        icon: 'users',
        permission: 'visit.view',
        module: 'visitor_management',
        description: 'Visitor register and front-desk check-in'
      },
      {
        label: 'Vehicles',
        href: '/workspace/vehicle_tracking',
        icon: 'car',
        permission: 'vehicle.view',
        module: 'vehicle_tracking',
        description: 'Vehicle passes and gate movement'
      },
      {
        label: 'Material',
        href: '/workspace/material_logistics',
        icon: 'boxes',
        permission: 'material.view',
        module: 'material_logistics',
        description: 'Goods in, goods out and returns'
      },
      {
        label: 'Staff outpass',
        href: '/workspace/staff_outpass',
        icon: 'idBadge',
        permission: 'outpass.view',
        module: 'staff_outpass',
        description: 'Staff movement and return tracking'
      },
      {
        label: 'Contractors',
        href: '/workspace/contractor_access',
        icon: 'helmet',
        permission: 'contractor.view',
        module: 'contractor_access',
        description: 'Permits, documents and contractor passes'
      },
      {
        label: 'Gate scanning',
        href: '/workspace/qr_scanning',
        icon: 'qrcode',
        permission: 'scan.view_log',
        module: 'qr_scanning',
        description: 'Scan log and token control'
      }
    ]
  },
  {
    label: 'Control',
    items: [
      {
        label: 'Approvals',
        href: '/approvals',
        icon: 'stamp',
        permission: 'approval.view',
        module: 'approval_workflow',
        description: 'Approval inbox and decision chains'
      },
      {
        label: 'Rules & policy',
        href: '/workspace/rules_policy',
        icon: 'scale',
        permission: 'rule.view',
        module: 'rules_policy',
        description: 'Tenant rules and access restrictions'
      },
      {
        label: 'Watchlist',
        href: '/workspace/blocklist_watchlist',
        icon: 'ban',
        permission: 'blocklist.view',
        module: 'blocklist_watchlist',
        description: 'Screening list and hit review'
      },
      {
        label: 'Emergency roll-call',
        href: '/workspace/emergency_rollcall',
        icon: 'triangle',
        permission: 'rollcall.view',
        module: 'emergency_rollcall',
        description: 'On-site roster and incident headcount'
      }
    ]
  },
  {
    label: 'Oversight',
    items: [
      {
        label: 'Reports',
        href: '/workspace/reports_exports',
        icon: 'chart',
        permission: 'report.view',
        module: 'reports_exports',
        description: 'Registers, exports and schedules'
      },
      {
        label: 'Audit & compliance',
        href: '/workspace/audit_compliance',
        icon: 'shield',
        permission: 'audit.view',
        module: 'audit_compliance',
        description: 'Audit trail, retention and legal hold'
      },
      {
        label: 'Notifications',
        href: '/workspace/notifications',
        icon: 'bell',
        permission: 'notification.view',
        module: 'notifications',
        description: 'Notice centre and delivery routes'
      },
      {
        label: 'Integrations',
        href: '/workspace/integrations_api',
        icon: 'plug',
        permission: 'webhook.manage',
        module: 'integrations_api',
        description: 'API keys, webhooks and external sync'
      },
      {
        label: 'Offline gate',
        href: '/workspace/offline_gate',
        icon: 'cloudArrowDown',
        permission: 'sync.view',
        module: 'offline_gate',
        description: 'Cached passes and conflict resolution'
      }
    ]
  }
];

export function visibleNavigation(groups: NavGroup[], can: (p: string) => boolean, moduleOn: (m: string) => boolean): NavGroup[] {
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => can(item.permission) && moduleOn(item.module))
    }))
    .filter((group) => group.items.length > 0);
}

export function findNavItem(path: string): NavItem | undefined {
  return navigation.flatMap((group) => group.items).find((item) => path === item.href || path.startsWith(item.href + '/'));
}

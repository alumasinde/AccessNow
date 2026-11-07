<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  import type { IconName } from '$lib/icons';
  import type { Tone } from '$lib/format';

  let {
    tone = 'neutral',
    children,
    icon,
    dot = false
  }: { tone?: Tone; children?: Snippet; icon?: IconName; dot?: boolean } = $props();
</script>

<span class="badge {tone}">
  {#if dot}<span class="dot"></span>{:else if icon}<Icon name={icon} size="0.84em" />{/if}
  {@render children?.()}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-height: 25px;
    padding: 3px 9px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 800;
    letter-spacing: 0.015em;
    white-space: nowrap;
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
  }

  .neutral {
    background: var(--surface-muted);
    color: var(--ink-soft);
    border: 1px solid var(--line);
  }

  .brand {
    background: var(--brand-soft);
    color: var(--brand-strong);
  }

  .teal {
    background: var(--teal-soft);
    color: var(--teal);
  }

  .amber {
    background: var(--amber-soft);
    color: var(--amber);
  }

  .danger {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .success {
    background: var(--success-soft);
    color: var(--success);
  }

  .violet {
    background: var(--violet-soft);
    color: var(--violet);
  }
</style>

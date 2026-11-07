<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  import type { IconName } from '$lib/icons';

  type Props = {
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'quiet';
    size?: 'sm' | 'md' | 'lg';
    type?: 'button' | 'submit' | 'reset';
    disabled?: boolean;
    loading?: boolean;
    icon?: IconName;
    trailingIcon?: IconName;
    href?: string;
    onclick?: (event: MouseEvent) => void;
    children?: Snippet;
    class?: string;
    title?: string;
    ariaLabel?: string;
    full?: boolean;
  };

  let {
    variant = 'primary',
    size = 'md',
    type = 'button',
    disabled = false,
    loading = false,
    icon,
    trailingIcon,
    href,
    onclick,
    children,
    class: className = '',
    title,
    ariaLabel,
    full = false
  }: Props = $props();
</script>

{#if href}
  <a
    {href}
    {title}
    aria-label={ariaLabel}
    class="btn {variant} {size} {className}"
    class:full
    onclick={onclick}
  >
    {#if loading}
      <Icon name="spinner" class="spin" />
    {:else if icon}
      <Icon name={icon} />
    {/if}
    {@render children?.()}
    {#if trailingIcon}
      <Icon name={trailingIcon} />
    {/if}
  </a>
{:else}
  <button
    {type}
    {title}
    aria-label={ariaLabel}
    class="btn {variant} {size} {className}"
    class:full
    disabled={disabled || loading}
    onclick={onclick}
  >
    {#if loading}
      <Icon name="spinner" class="spin" />
    {:else if icon}
      <Icon name={icon} />
    {/if}
    {@render children?.()}
    {#if trailingIcon}
      <Icon name={trailingIcon} />
    {/if}
  </button>
{/if}

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    border: 1px solid transparent;
    border-radius: 10px;
    font-weight: 750;
    letter-spacing: -0.01em;
    line-height: 1;
    text-decoration: none;
    white-space: nowrap;
    transition:
      background 160ms ease,
      border-color 160ms ease,
      color 160ms ease,
      transform 160ms ease,
      box-shadow 160ms ease;
  }

  .btn:active:not(:disabled) {
    transform: translateY(1px);
  }

  .btn:disabled {
    opacity: 0.52;
  }

  .sm {
    min-height: 34px;
    padding: 0 11px;
    font-size: 12px;
    border-radius: 9px;
  }

  .md {
    min-height: 41px;
    padding: 0 15px;
    font-size: 13px;
  }

  .lg {
    min-height: 48px;
    padding: 0 20px;
    font-size: 14px;
  }

  .full {
    width: 100%;
  }

  .primary {
    background: var(--brand);
    color: #fff;
    box-shadow: 0 6px 18px rgba(217, 84, 45, 0.2);
  }

  .primary:hover:not(:disabled) {
    background: var(--brand-strong);
    box-shadow: 0 8px 24px rgba(217, 84, 45, 0.28);
  }

  .secondary {
    background: var(--surface);
    border-color: var(--line-strong);
    color: var(--ink);
  }

  .secondary:hover:not(:disabled) {
    border-color: var(--ink-faint);
    background: var(--surface-muted);
  }

  .ghost {
    background: transparent;
    color: var(--ink-soft);
  }

  .ghost:hover:not(:disabled) {
    background: var(--surface-muted);
    color: var(--ink);
  }

  .danger {
    background: var(--danger);
    color: #fff;
  }

  .danger:hover:not(:disabled) {
    filter: brightness(1.08);
  }

  .quiet {
    background: var(--brand-soft);
    color: var(--brand-strong);
  }

  .quiet:hover:not(:disabled) {
    filter: brightness(0.97);
  }

  .spin {
    animation: spin 900ms linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>

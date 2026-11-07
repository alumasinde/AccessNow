<script lang="ts">
  import { fly } from 'svelte/transition';
  import { toasts } from '$lib/stores/toast.svelte';
  import Icon from './Icon.svelte';

  function iconFor(tone: string) {
    if (tone === 'success') return 'circleCheck' as const;
    if (tone === 'error') return 'circleExclamation' as const;
    return 'circleInfo' as const;
  }
</script>

<div class="toasts" aria-live="polite" aria-atomic="false">
  {#each toasts.items as toast (toast.id)}
    <div class="toast {toast.tone}" transition:fly={{ y: 16, duration: 220 }}>
      <span class="icon"><Icon name={iconFor(toast.tone)} size="1.1em" /></span>
      <div>
        <p class="title">{toast.title}</p>
        {#if toast.message}<p class="message">{toast.message}</p>{/if}
      </div>
      <button type="button" aria-label="Dismiss" onclick={() => toasts.dismiss(toast.id)}>
        <Icon name="xmark" size="0.82em" />
      </button>
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    z-index: 100;
    right: 20px;
    bottom: 20px;
    display: flex;
    flex-direction: column;
    gap: 9px;
    width: min(370px, calc(100vw - 32px));
    pointer-events: none;
  }

  .toast {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: start;
    gap: 11px;
    padding: 14px 14px 14px 13px;
    border: 1px solid var(--line);
    border-left: 3px solid var(--ink-faint);
    border-radius: 12px;
    background: var(--surface-raised);
    box-shadow: var(--shadow-md);
    pointer-events: auto;
  }

  .toast.success {
    border-left-color: var(--success);
  }

  .toast.error {
    border-left-color: var(--danger);
  }

  .toast.info {
    border-left-color: var(--teal);
  }

  .icon {
    padding-top: 1px;
    color: var(--teal);
  }

  .success .icon {
    color: var(--success);
  }

  .error .icon {
    color: var(--danger);
  }

  .title {
    color: var(--ink);
    font-size: 12.5px;
    font-weight: 800;
  }

  .message {
    margin-top: 2px;
    color: var(--ink-soft);
    font-size: 11.5px;
    line-height: 1.5;
  }

  button {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--ink-faint);
  }

  button:hover {
    background: var(--surface-muted);
    color: var(--ink);
  }

  @media (max-width: 560px) {
    .toasts {
      right: 16px;
      bottom: 16px;
    }
  }
</style>

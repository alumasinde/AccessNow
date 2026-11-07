<script lang="ts">
  import { tick } from 'svelte';
  import { fade, fly } from 'svelte/transition';
  import type { Snippet } from 'svelte';
  import Button from './Button.svelte';

  let {
    open,
    title,
    description,
    onclose,
    children,
    footer,
    wide = false
  }: {
    open: boolean;
    title: string;
    description?: string;
    onclose: () => void;
    children: Snippet;
    footer?: Snippet;
    wide?: boolean;
  } = $props();

  let panel = $state<HTMLDivElement | null>(null);

  $effect(() => {
    if (!open) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    tick().then(() => panel?.focus());
    return () => {
      document.body.style.overflow = previous;
    };
  });
</script>

<svelte:window
  onkeydown={(event) => {
    if (open && event.key === 'Escape') onclose();
  }}
/>

{#if open}
  <div
    class="overlay"
    role="presentation"
    transition:fade={{ duration: 150 }}
    onclick={(event) => {
      if (event.target === event.currentTarget) onclose();
    }}
  >
    <div
      class="dialog"
      class:wide
      bind:this={panel}
      role="dialog"
      aria-modal="true"
      aria-labelledby="modal-title"
      tabindex="-1"
      in:fly={{ y: 18, duration: 220 }}
    >
      <header>
        <div>
          <h2 id="modal-title">{title}</h2>
          {#if description}<p>{description}</p>{/if}
        </div>
        <Button variant="ghost" size="sm" onclick={onclose} ariaLabel="Close dialog" icon="xmark" />
      </header>
      <div class="body">{@render children()}</div>
      {#if footer}
        <footer>{@render footer()}</footer>
      {/if}
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 80;
    display: grid;
    place-items: center;
    padding: 20px;
    background: rgba(8, 15, 25, 0.58);
    backdrop-filter: blur(4px);
  }

  .dialog {
    width: min(480px, 100%);
    max-height: min(86vh, 760px);
    overflow: auto;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--surface-raised);
    box-shadow: var(--shadow-lg);
  }

  .dialog.wide {
    width: min(680px, 100%);
  }

  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    padding: 22px 22px 0;
  }

  h2 {
    font-size: 20px;
    font-weight: 800;
  }

  header p {
    margin-top: 6px;
    color: var(--ink-soft);
    font-size: 12.5px;
    line-height: 1.55;
  }

  .body {
    padding: 20px 22px;
  }

  footer {
    display: flex;
    justify-content: flex-end;
    gap: 9px;
    padding: 16px 22px;
    border-top: 1px solid var(--line);
    background: var(--surface-muted);
  }

  @media (max-width: 520px) {
    .overlay {
      align-items: end;
      padding: 0;
    }

    .dialog,
    .dialog.wide {
      width: 100%;
      max-height: 92vh;
      border-radius: var(--radius-lg) var(--radius-lg) 0 0;
    }
  }
</style>

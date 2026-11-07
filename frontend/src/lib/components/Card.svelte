<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    title,
    eyebrow,
    description,
    action,
    children,
    padded = true,
    interactive = false,
    class: className = ''
  }: {
    title?: string;
    eyebrow?: string;
    description?: string;
    action?: Snippet;
    children?: Snippet;
    padded?: boolean;
    interactive?: boolean;
    class?: string;
  } = $props();
</script>

<section class="card {className}" class:padded class:interactive>
  {#if title || eyebrow || action}
    <header class="card-head">
      <div>
        {#if eyebrow}<p class="eyebrow">{eyebrow}</p>{/if}
        {#if title}<h3>{title}</h3>{/if}
        {#if description}<p class="description">{description}</p>{/if}
      </div>
      {#if action}<div class="action">{@render action()}</div>{/if}
    </header>
  {/if}
  {@render children?.()}
</section>

<style>
  .card {
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .padded {
    padding: 22px;
  }

  .interactive {
    transition:
      border-color 160ms ease,
      box-shadow 160ms ease,
      transform 160ms ease;
  }

  .interactive:hover {
    border-color: var(--line-strong);
    box-shadow: var(--shadow-md);
    transform: translateY(-1px);
  }

  .card-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 18px;
  }

  h3 {
    margin-top: 3px;
    font-size: 17px;
    font-weight: 800;
  }

  .description {
    max-width: 58ch;
    margin-top: 5px;
    color: var(--ink-soft);
    font-size: 12.5px;
  }

  .action {
    flex: 0 0 auto;
  }

  @media (max-width: 560px) {
    .padded {
      padding: 17px;
    }

    .card-head {
      flex-direction: column;
    }
  }
</style>

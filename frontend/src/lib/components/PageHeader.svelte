<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    eyebrow,
    title,
    description,
    actions,
    children
  }: {
    eyebrow?: string;
    title: string;
    description?: string;
    actions?: Snippet;
    children?: Snippet;
  } = $props();
</script>

<header class="page-header">
  <div class="title-block">
    {#if eyebrow}<p class="eyebrow">{eyebrow}</p>{/if}
    <h1>{title}</h1>
    {#if description}<p class="description">{description}</p>{/if}
    {@render children?.()}
  </div>
  {#if actions}
    <div class="actions">{@render actions()}</div>
  {/if}
</header>

<style>
  .page-header {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 24px;
    margin-bottom: 26px;
  }

  .title-block {
    min-width: 0;
  }

  h1 {
    margin-top: 5px;
    font-size: clamp(27px, 3.2vw, 38px);
    font-weight: 820;
  }

  .description {
    max-width: 64ch;
    margin-top: 8px;
    color: var(--ink-soft);
    font-size: 13px;
  }

  .actions {
    display: flex;
    align-items: center;
    gap: 9px;
    flex: 0 0 auto;
  }

  @media (max-width: 720px) {
    .page-header {
      align-items: flex-start;
      flex-direction: column;
      gap: 16px;
    }

    .actions {
      width: 100%;
      flex-wrap: wrap;
    }
  }
</style>

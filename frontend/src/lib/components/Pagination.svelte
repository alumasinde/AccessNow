<script lang="ts">
  import Button from './Button.svelte';

  let {
    page,
    hasMore,
    count,
    loading = false,
    onNext,
    onPrev
  }: {
    page: number;
    hasMore: boolean;
    count: number;
    loading?: boolean;
    onNext: () => void;
    onPrev: () => void;
  } = $props();
</script>

<div class="pagination">
  <p>
    <strong class="mono">{count}</strong>
    {count === 1 ? 'record' : 'records'} on this page
    {#if page > 1}<span class="faint"> · page {page}</span>{/if}
  </p>
  <div class="controls">
    <Button variant="secondary" size="sm" icon="chevronLeft" disabled={page <= 1 || loading} onclick={onPrev}>
      Back
    </Button>
    <Button
      variant="secondary"
      size="sm"
      trailingIcon="chevronRight"
      disabled={!hasMore || loading}
      onclick={onNext}
    >
      Next
    </Button>
  </div>
</div>

<style>
  .pagination {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 15px 16px;
    border-top: 1px solid var(--line);
  }

  p {
    color: var(--ink-soft);
    font-size: 12px;
  }

  strong {
    color: var(--ink);
  }

  .controls {
    display: flex;
    gap: 8px;
  }

  @media (max-width: 520px) {
    .pagination {
      align-items: flex-start;
      flex-direction: column;
    }

    .controls {
      width: 100%;
    }

    .controls :global(.btn) {
      flex: 1;
    }
  }
</style>

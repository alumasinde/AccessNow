<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    label,
    hint,
    error,
    required = false,
    forId,
    children,
    class: className = ''
  }: {
    label: string;
    hint?: string;
    error?: string;
    required?: boolean;
    forId?: string;
    children: Snippet;
    class?: string;
  } = $props();
</script>

<div class="field {className}">
  <label class="field-label" for={forId}>
    {label}
    {#if required}<span class="required" aria-hidden="true">*</span>{/if}
  </label>
  {@render children()}
  {#if error}
    <p class="field-error">{error}</p>
  {:else if hint}
    <p class="field-hint">{hint}</p>
  {/if}
</div>

<style>
  .field {
    min-width: 0;
  }

  .required {
    color: var(--brand);
  }
</style>

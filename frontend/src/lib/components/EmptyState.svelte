<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  import type { IconName } from '$lib/icons';

  let {
    icon = 'circleInfo',
    title,
    description,
    actions
  }: {
    icon?: IconName;
    title: string;
    description?: string;
    actions?: Snippet;
  } = $props();
</script>

<div class="empty">
  <span class="icon-wrap"><Icon name={icon} size="1.4em" /></span>
  <h3>{title}</h3>
  {#if description}<p>{description}</p>{/if}
  {#if actions}<div class="actions">{@render actions()}</div>{/if}
</div>

<style>
  .empty {
    display: flex;
    align-items: center;
    flex-direction: column;
    max-width: 460px;
    margin: 0 auto;
    padding: 52px 24px;
    text-align: center;
  }

  .icon-wrap {
    display: grid;
    place-items: center;
    width: 52px;
    height: 52px;
    margin-bottom: 16px;
    border: 1px solid var(--line);
    border-radius: 16px;
    background: var(--surface-muted);
    color: var(--brand);
  }

  h3 {
    font-size: 17px;
    font-weight: 800;
  }

  p {
    margin-top: 7px;
    color: var(--ink-soft);
    font-size: 12.5px;
    line-height: 1.6;
  }

  .actions {
    margin-top: 18px;
  }
</style>

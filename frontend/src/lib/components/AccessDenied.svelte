<script lang="ts">
  import EmptyState from './EmptyState.svelte';
  import Button from './Button.svelte';

  let {
    moduleOff = false,
    title
  }: { moduleOff?: boolean; title?: string } = $props();
</script>

<EmptyState
  icon="lock"
  title={title ?? (moduleOff ? 'This module is switched off' : 'You do not have access')}
  description={moduleOff
    ? 'Your workspace administrator has not enabled this module. Nothing is wrong with your account.'
    : 'Your account does not carry the permission for this surface. Ask a workspace administrator if you need it.'}
>
  {#snippet actions()}
    <Button variant="secondary" href="/" icon="arrowLeft">Back to overview</Button>
  {/snippet}
</EmptyState>

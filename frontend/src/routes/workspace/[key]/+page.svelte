<script lang="ts">
  import { page } from '$app/state';
  import { session } from '$lib/session.svelte';
  import { navigation } from '$lib/nav';
  import AccessDenied from '$lib/components/AccessDenied.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Card from '$lib/components/Card.svelte';
  import Button from '$lib/components/Button.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const key = $derived(page.params.key ?? '');
  const item = $derived(navigation.flatMap((group) => group.items).find((candidate) => candidate.href === `/workspace/${key}`));
  const moduleOn = $derived(Boolean(item && session.moduleOn(item.module)));
  const allowed = $derived(Boolean(item && session.can(item.permission)));
</script>

<svelte:head><title>{item?.label ?? 'Workspace'} · AccessNow</title></svelte:head>

<div class="page">
  {#if !item || !moduleOn || !allowed}
    <AccessDenied moduleOff={Boolean(item && !moduleOn)} />
  {:else}
    <PageHeader
      eyebrow="Enabled module"
      title={item.label}
      description={item.description}
    >
      {#snippet actions()}
        <Button variant="secondary" href="/visitors" icon="users">Open visitors</Button>
      {/snippet}
    </PageHeader>

    <Card>
      <div class="ready">
        <span class="ready-icon"><Icon name="circleCheck" size="1.35em" /></span>
        <p class="eyebrow">Connected at the API layer</p>
        <h2>{item.label} is enabled for this workspace.</h2>
        <p>
          Your session module flag and <span class="mono">{item.permission}</span> permission are both active.
          The screen for this module is not part of the Visitors reference slice, so nothing here is pretending to hold data.
        </p>
        <div class="checks">
          <span><Icon name="check" size="0.75em" /> Module on</span>
          <span><Icon name="check" size="0.75em" /> Permission granted</span>
          <span><Icon name="check" size="0.75em" /> Navigation generated from the session</span>
        </div>
        <Button href="/visitors" trailingIcon="arrowRight">See the reference workspace</Button>
      </div>
    </Card>
  {/if}
</div>

<style>
  .ready {
    display: flex;
    align-items: flex-start;
    flex-direction: column;
    max-width: 690px;
    padding: 24px 8px;
  }

  .ready-icon {
    display: grid;
    place-items: center;
    width: 48px;
    height: 48px;
    margin-bottom: 17px;
    border-radius: 14px;
    background: var(--success-soft);
    color: var(--success);
  }

  h2 {
    margin-top: 8px;
    font-size: 25px;
    font-weight: 800;
  }

  .ready > p:not(.eyebrow) {
    margin-top: 12px;
    color: var(--ink-soft);
    font-size: 12.5px;
    line-height: 1.7;
  }

  .checks {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin: 22px 0;
  }

  .checks span {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 9px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--ink-soft);
    font-size: 10px;
    font-weight: 700;
  }

  .checks :global(.fa-icon) {
    color: var(--success);
  }
</style>

<script lang="ts">
  import { goto } from '$app/navigation';
  import { session } from '$lib/session.svelte';
  import { theme } from '$lib/theme.svelte';
  import { toasts } from '$lib/stores/toast.svelte';
  import Button from '$lib/components/Button.svelte';
  import Field from '$lib/components/Field.svelte';
  import Icon from '$lib/components/Icon.svelte';

  let tenant = $state('');
  let identifier = $state('');
  let password = $state('');
  let showPassword = $state(false);
  let submitting = $state(false);
  let error = $state<string | null>(null);

  async function submit() {
    if (submitting) return;
    error = null;
    submitting = true;
    const failure = await session.login(tenant.trim(), identifier.trim(), password);
    submitting = false;
    if (failure) {
      error = failure;
      return;
    }
    toasts.success('Welcome back', 'Your workspace is ready.');
    await goto('/');
  }

  function toggleTheme() {
    theme.toggle();
  }
</script>

<svelte:head><title>Sign in · AccessNow</title></svelte:head>

<div class="login-page">
  <section class="story">
    <div class="story-top">
      <a class="brand" href="/" aria-label="AccessNow">
        <span class="mark" aria-hidden="true"><span></span><span></span></span>
        <span>AccessNow</span>
      </a>
      <span class="badge">Site access control</span>
    </div>

    <div class="story-body">
      <p class="eyebrow light">One register. Every gate.</p>
      <h1>Know who is on site, before the gate opens.</h1>
      <p class="lede">
        Visitors, vehicles, materials and contractors move through the same defensible record.
        Your workspace decides what you can see and do.
      </p>

      <div class="proof">
        <div>
          <span class="proof-icon"><Icon name="shield" size="0.95em" /></span>
          <p><strong>Server-enforced access</strong><small>Every action is checked against your permissions.</small></p>
        </div>
        <div>
          <span class="proof-icon"><Icon name="clock" size="0.95em" /></span>
          <p><strong>A live movement trail</strong><small>Arrivals, exits and decisions stay in order.</small></p>
        </div>
      </div>
    </div>

    <p class="story-foot">Built for front desks, gatehouses and the people accountable for both.</p>
  </section>

  <section class="form-side">
    <button class="theme" type="button" onclick={toggleTheme} aria-label="Toggle colour theme">
      <Icon name={theme.value === 'light' ? 'moon' : 'sun'} size="0.95em" />
    </button>

    <form class="login-card" onsubmit={(event) => { event.preventDefault(); submit(); }}>
      <div class="heading">
        <p class="eyebrow">Welcome back</p>
        <h2>Sign in to your workspace</h2>
        <p>Use the workspace slug your administrator gave you.</p>
      </div>

      {#if error}
        <div class="error" role="alert">
          <Icon name="circleExclamation" size="0.9em" />
          <span>{error}</span>
        </div>
      {/if}

      <Field label="Workspace" forId="tenant" hint="For example: northgate">
        <input
          id="tenant"
          class="input"
          bind:value={tenant}
          placeholder="your-workspace"
          autocomplete="organization"
          autocapitalize="none"
          spellcheck="false"
          required
        />
      </Field>

      <Field label="Email or username" forId="identifier">
        <input
          id="identifier"
          class="input"
          bind:value={identifier}
          placeholder="you@company.com"
          autocomplete="username"
          autocapitalize="none"
          spellcheck="false"
          required
        />
      </Field>

      <Field label="Password" forId="password">
        <div class="password-field">
          <input
            id="password"
            class="input"
            type={showPassword ? 'text' : 'password'}
            bind:value={password}
            placeholder="Enter your password"
            autocomplete="current-password"
            required
          />
          <button
            type="button"
            class="reveal"
            aria-label={showPassword ? 'Hide password' : 'Show password'}
            onclick={() => (showPassword = !showPassword)}
          >
            <Icon name={showPassword ? 'eyeSlash' : 'eye'} size="0.9em" />
          </button>
        </div>
      </Field>

      <Button type="submit" size="lg" full loading={submitting} trailingIcon="arrowRight">
        {submitting ? 'Opening workspace' : 'Sign in'}
      </Button>

      <p class="help">Trouble signing in? Contact your workspace administrator.</p>
    </form>

    <p class="legal">Access is logged. Use only the account assigned to you.</p>
  </section>
</div>

<style>
  .login-page {
    display: grid;
    grid-template-columns: minmax(0, 1.08fr) minmax(0, 0.92fr);
    min-height: 100vh;
    background: var(--surface);
  }

  .story {
    position: relative;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    min-height: 100vh;
    padding: 34px 44px 30px;
    overflow: hidden;
    background:
      radial-gradient(circle at 16% 18%, rgba(255, 122, 77, 0.22), transparent 24rem),
      radial-gradient(circle at 85% 88%, rgba(91, 213, 201, 0.12), transparent 25rem),
      var(--sidebar);
    color: #f4f6fa;
  }

  .story::before {
    content: '';
    position: absolute;
    inset: 0;
    opacity: 0.16;
    background-image:
      linear-gradient(rgba(255, 255, 255, 0.12) 1px, transparent 1px),
      linear-gradient(90deg, rgba(255, 255, 255, 0.12) 1px, transparent 1px);
    background-size: 54px 54px;
    mask-image: linear-gradient(to bottom, black, transparent 72%);
  }

  .story-top,
  .story-body,
  .story-foot {
    position: relative;
  }

  .story-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 11px;
    color: #fff;
    font-size: 16px;
    font-weight: 820;
    letter-spacing: -0.035em;
  }

  .mark {
    display: flex;
    align-items: flex-end;
    gap: 3px;
    width: 34px;
    height: 34px;
    padding: 8px;
    border-radius: 10px;
    background: var(--brand);
    box-shadow: 0 8px 22px rgba(217, 84, 45, 0.35);
  }

  .mark span {
    width: 7px;
    border-radius: 2px;
    background: #fff;
  }

  .mark span:first-child {
    height: 18px;
  }

  .mark span:last-child {
    height: 11px;
    opacity: 0.62;
  }

  .badge {
    padding: 5px 9px;
    border: 1px solid rgba(255, 255, 255, 0.16);
    border-radius: 999px;
    color: #bdc7d6;
    font-size: 9px;
    font-weight: 800;
    letter-spacing: 0.12em;
    text-transform: uppercase;
  }

  .story-body {
    max-width: 610px;
    padding: 60px 0;
  }

  .eyebrow.light {
    color: #ff9b78;
  }

  h1 {
    max-width: 560px;
    margin-top: 13px;
    font-size: clamp(34px, 4.6vw, 61px);
    font-weight: 780;
    letter-spacing: -0.055em;
    line-height: 1.05;
  }

  .lede {
    max-width: 490px;
    margin-top: 22px;
    color: #a9b6c8;
    font-size: 14px;
    line-height: 1.7;
  }

  .proof {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
    max-width: 570px;
    margin-top: 40px;
  }

  .proof > div {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 15px;
    border: 1px solid rgba(255, 255, 255, 0.09);
    border-radius: 13px;
    background: rgba(255, 255, 255, 0.045);
    backdrop-filter: blur(4px);
  }

  .proof-icon {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    flex: 0 0 auto;
    border-radius: 8px;
    background: rgba(255, 122, 77, 0.15);
    color: #ff9b78;
  }

  .proof strong {
    display: block;
    font-size: 11px;
    font-weight: 800;
  }

  .proof small {
    display: block;
    margin-top: 3px;
    color: #9eacbf;
    font-size: 10px;
    line-height: 1.5;
  }

  .story-foot {
    color: #7f8ea5;
    font-size: 10px;
  }

  .form-side {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-direction: column;
    padding: 40px 28px;
    background:
      radial-gradient(circle at 100% 0%, rgba(217, 84, 45, 0.08), transparent 22rem),
      var(--surface);
  }

  .theme {
    position: absolute;
    top: 22px;
    right: 22px;
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: var(--surface);
    color: var(--ink-soft);
  }

  .login-card {
    display: flex;
    flex-direction: column;
    gap: 17px;
    width: min(392px, 100%);
    animation: rise 420ms cubic-bezier(0.2, 0.75, 0.3, 1) both;
  }

  .heading {
    margin-bottom: 5px;
  }

  .heading h2 {
    margin-top: 7px;
    font-size: 27px;
    font-weight: 820;
  }

  .heading p:last-child {
    margin-top: 8px;
    color: var(--ink-soft);
    font-size: 12px;
  }

  .error {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 11px 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 28%, transparent);
    border-radius: 9px;
    background: var(--danger-soft);
    color: var(--danger);
    font-size: 11.5px;
    font-weight: 650;
    line-height: 1.5;
  }

  .password-field {
    position: relative;
  }

  .password-field :global(.input) {
    padding-right: 44px;
  }

  .reveal {
    position: absolute;
    top: 50%;
    right: 8px;
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    transform: translateY(-50%);
    border: 0;
    border-radius: 7px;
    background: transparent;
    color: var(--ink-faint);
  }

  .reveal:hover {
    background: var(--surface-muted);
    color: var(--ink);
  }

  .help {
    color: var(--ink-faint);
    font-size: 10.5px;
    text-align: center;
  }

  .legal {
    position: absolute;
    bottom: 24px;
    color: var(--ink-faint);
    font-size: 9.5px;
  }

  @media (max-width: 900px) {
    .login-page {
      grid-template-columns: 1fr;
    }

    .story {
      display: none;
    }

    .form-side {
      min-height: 100vh;
    }
  }

  @media (max-width: 520px) {
    .form-side {
      justify-content: flex-start;
      padding: 90px 20px 60px;
    }

    .legal {
      position: static;
      margin-top: 30px;
    }
  }
</style>

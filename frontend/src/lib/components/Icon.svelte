<script lang="ts">
  import { icon as faIcon } from '@fortawesome/fontawesome-svg-core';
  import { icons, type IconName } from '$lib/icons';

  let {
    name,
    size = '1em',
    label,
    class: className = ''
  }: { name: IconName; size?: string; label?: string; class?: string } = $props();

  const markup = $derived(faIcon(icons[name]).html.join(''));
</script>

<span
  class="fa-icon {className}"
  style="--icon-size: {size}"
  role={label ? 'img' : undefined}
  aria-label={label}
  aria-hidden={label ? undefined : 'true'}
>
  {@html markup}
</span>

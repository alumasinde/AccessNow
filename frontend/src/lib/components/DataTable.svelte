<script lang="ts" generics="Row">
  import type { Snippet } from 'svelte';

  type Column = {
    label: string;
    align?: 'left' | 'right' | 'center';
    width?: string;
    hideOn?: 'mobile' | 'tablet';
  };

  let {
    columns,
    rows,
    rowKey,
    onrowclick,
    cell,
    empty
  }: {
    columns: Column[];
    rows: Row[];
    rowKey: (row: Row) => string | number;
    onrowclick?: (row: Row) => void;
    cell: Snippet<[Row]>;
    empty?: Snippet;
  } = $props();
</script>

<div class="wrap">
  <table>
    <thead>
      <tr>
        {#each columns as column}
          <th
            style:width={column.width}
            style:text-align={column.align ?? 'left'}
            class:hide-mobile={column.hideOn === 'mobile'}
            class:hide-tablet={column.hideOn === 'tablet'}
          >
            {column.label}
          </th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each rows as row (rowKey(row))}
        <tr
          class:clickable={Boolean(onrowclick)}
          tabindex={onrowclick ? 0 : undefined}
          onclick={() => onrowclick?.(row)}
          onkeydown={(event) => {
            if (onrowclick && (event.key === 'Enter' || event.key === ' ')) {
              event.preventDefault();
              onrowclick(row);
            }
          }}
        >
          {@render cell(row)}
        </tr>
      {:else}
        <tr class="empty-row">
          <td colspan={columns.length}>
            {#if empty}
              {@render empty()}
            {:else}
              No records found.
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .wrap {
    width: 100%;
    overflow-x: auto;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    text-align: left;
  }

  th {
    padding: 0 16px 10px;
    border-bottom: 1px solid var(--line);
    color: var(--ink-faint);
    font-size: 10.5px;
    font-weight: 800;
    letter-spacing: 0.09em;
    text-transform: uppercase;
    white-space: nowrap;
  }

  td {
    padding: 15px 16px;
    border-bottom: 1px solid var(--line);
    vertical-align: middle;
  }

  tbody tr:last-child td {
    border-bottom: 0;
  }

  tbody tr {
    transition: background 140ms ease;
  }

  tbody tr.clickable {
    cursor: pointer;
  }

  tbody tr.clickable:hover,
  tbody tr.clickable:focus-visible {
    background: var(--surface-muted);
  }

  tbody tr:focus-visible {
    outline: none;
    box-shadow: inset 3px 0 0 var(--brand);
  }

  .empty-row td {
    padding: 40px 16px;
    color: var(--ink-soft);
    text-align: center;
  }

  @media (max-width: 760px) {
    th,
    td {
      padding-left: 12px;
      padding-right: 12px;
    }

    .hide-tablet {
      display: none;
    }
  }

  @media (max-width: 560px) {
    .hide-mobile {
      display: none;
    }
  }
</style>

<!-- input: 字段名称、带数量的筛选选项、已选值及选择回调。 -->
<!-- output: 表头列名和箭头、可搜索单选弹层、数量与已选标记。 -->
<!-- pos: 工单表头共用的 shadcn Combobox 组合。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Check, ChevronDown } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Popover from "$lib/components/ui/popover/index.js";
  import * as Command from "$lib/components/ui/command/index.js";
  import { filterLabel, type FilterChoice, type FilterKey } from "./api";
  let { label, field, value, options, onSelect }: {
    label: string; field: FilterKey; value: string; options: FilterChoice[]; onSelect: (value: string) => void;
  } = $props();
  let open = $state(false);
  function select(next: string) { open = false; onSelect(next); }
</script>

<Popover.Root bind:open>
  <Popover.Trigger>
    {#snippet child({ props })}
      <Button {...props} variant="ghost" size="sm" class={`-ml-2 gap-1 px-2 ${value ? "text-primary underline underline-offset-4" : ""}`} role="combobox" aria-expanded={open} aria-label={`筛选${label}`} title={value ? `已筛选：${filterLabel(field, value)}` : `筛选${label}`}>
        {label}<ChevronDown size={14} aria-hidden="true" />
      </Button>
    {/snippet}
  </Popover.Trigger>
  <Popover.Content class="w-64 max-w-[calc(100vw-2rem)] gap-1 p-1" align="start">
    <div class="px-2 pt-2 text-sm font-medium">{label}</div>
    <Command.Root>
      <Command.Input placeholder="搜索选项…" aria-label={`搜索${label}选项`} />
      <Command.List class="max-h-64">
        <Command.Empty>没有匹配选项</Command.Empty>
        <Command.Group>
          {#each [{ value: "", count: options.reduce((sum, item) => sum + item.count, 0) }, ...options] as option (option.value)}
            {@const text = option.value ? filterLabel(field, option.value) : "全部"}
            <Command.Item value={option.value || "__all__"} keywords={[text]} onSelect={() => select(option.value)} class="[&>svg:last-child]:hidden">
              <span class="flex size-4 items-center justify-center rounded-full border" class:bg-primary={value === option.value} class:text-primary-foreground={value === option.value}>
                {#if value === option.value}<Check size={11} aria-hidden="true" />{/if}
              </span>
              <span>{text}</span><span class="ml-auto text-xs text-muted-foreground tabular-nums">{option.count}</span>
            </Command.Item>
          {/each}
        </Command.Group>
      </Command.List>
    </Command.Root>
  </Popover.Content>
</Popover.Root>

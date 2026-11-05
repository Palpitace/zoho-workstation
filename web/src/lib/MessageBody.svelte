<!-- input: 仅供展示的纯文本分段及嵌套引用。 -->
<!-- output: 默认折叠且可键盘展开的邮件引用，保留原文顺序。 -->
<!-- pos: 沟通消息正文展示组件。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import type { MessageSegment } from "./api";
  let { segments }: { segments: MessageSegment[] } = $props();
</script>

{#snippet content(parts: MessageSegment[])}
  {#each parts as part}
    {#if part.quote}
      <details data-message-quote class="my-2 rounded-md border border-current/15 bg-background/40 px-3 py-2">
        <summary class="cursor-pointer text-xs text-muted-foreground">引用的历史邮件 · 展开 / 收起</summary>
        <div class="mt-2 border-l-2 pl-3">{#if part.children}{@render content(part.children)}{:else}<p class="whitespace-pre-wrap break-words">{part.text}</p>{/if}</div>
      </details>
    {:else}<p class="whitespace-pre-wrap break-words">{part.text}</p>{/if}
  {/each}
{/snippet}
<div class="max-w-4xl text-sm leading-7">{@render content(segments)}</div>

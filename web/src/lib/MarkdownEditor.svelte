<!-- input: 草稿 Markdown 和编辑回调。 -->
<!-- output: 使用本地 Vditor 资源的可视编辑、源码和预览，返回唯一 Markdown 正文。 -->
<!-- pos: 知识版本编辑器，关闭浏览器草稿缓存。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount } from "svelte";
  import { knowledgeJSON } from "./knowledge-api";
  import type Vditor from "vditor";
  import { Button } from "$lib/components/ui/button/index.js";
  import MarkdownView from "./MarkdownView.svelte";
  import "vditor/dist/index.css";
  let { value = $bindable(""), disabled = false, token }: { value: string; disabled?: boolean; token: string } = $props();
  let container: HTMLDivElement;
  let editor: Vditor | undefined;
  let mode = $state<"visual" | "source" | "preview">("visual");
  let loading = $state(true);
  let error = $state("");
  let ready = false;
  let alive = true;
  const uploadController = new AbortController();
  onMount(() => {
    let closed = false;
    void import("vditor").then(({ default: Editor }) => {
      if (closed) return;
      editor = new Editor(container, {
        cdn: "/vendor/vditor", value, mode: "wysiwyg", lang: "zh_CN", minHeight: 320,
        cache: { enable: false }, toolbar: ["headings", "bold", "italic", "strike", "|", "list", "ordered-list", "check", "|", "quote", "link", "upload", "table", "code", "|", "undo", "redo"],
        preview: { mode: "editor", markdown: { sanitize: true, codeBlockPreview: false, mathBlockPreview: false, inlineMath: false }, hljs: { enable: false }, render: { media: { enable: false } } },
        link: { isOpen: false }, image: { isPreview: false },
        upload: { accept: "image/png,image/jpeg,image/webp,image/gif", handler: async (files): Promise<null> => {
          if (disabled || !token) { editor?.tip("请等待当前操作完成。"); return null; }
          try {
            for (const file of files) {
              const form = new FormData(); form.set("file", file);
              const result = await knowledgeJSON<{ url: string }>("/api/knowledge/assets", uploadController.signal, token, form);
              if (!/^\/api\/knowledge\/assets\/[a-zA-Z0-9._-]+$/.test(result.url)) throw new Error();
              if (alive) { editor?.insertMD(`![图片](${result.url})`); value = editor?.getValue() || value; }
            }
            return null;
          } catch { if (alive) editor?.tip("图片上传失败，请重试。"); return null; }
        } },
        input: (markdown) => { if (ready) value = markdown; },
        after: () => { if (closed) return; ready = true; loading = false; if (disabled) editor?.disabled(); },
      });
    }).catch(() => { if (!closed) { error = "可视编辑器未加载，可继续使用源码编辑。"; loading = false; mode = "source"; } });
    return () => { closed = true; alive = false; uploadController.abort(); ready = false; editor?.destroy(); };
  });
  $effect(() => { const locked = disabled; if (ready && editor) { if (locked) editor.disabled(); else editor.enable(); } });
  function select(next: typeof mode) {
    if (mode === "visual" && ready && editor) value = editor.getValue();
    if (next === "visual" && ready && editor) editor.setValue(value);
    mode = next;
  }
</script>
<div class="min-w-0 space-y-3">
  <div class="flex gap-1" aria-label="编辑模式">
    <Button type="button" size="sm" variant={mode === "visual" ? "secondary" : "ghost"} aria-pressed={mode === "visual"} disabled={!!error || loading} onclick={() => select("visual")}>可视编辑</Button>
    <Button type="button" size="sm" variant={mode === "source" ? "secondary" : "ghost"} aria-pressed={mode === "source"} onclick={() => select("source")}>Markdown</Button>
    <Button type="button" size="sm" variant={mode === "preview" ? "secondary" : "ghost"} aria-pressed={mode === "preview"} onclick={() => select("preview")}>预览</Button>
  </div>
  {#if loading}<p role="status" class="text-sm text-muted-foreground">正在加载编辑器…</p>{/if}
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  <div class:hidden={mode !== "visual"} bind:this={container}></div>
  {#if mode === "source"}<textarea aria-label="Markdown 正文" bind:value disabled={disabled} rows={16} class="min-h-80 w-full rounded-md border bg-background p-3 font-mono text-sm leading-7"></textarea>{/if}
  {#if mode === "preview"}<div class="rounded-md border p-4"><MarkdownView {value} /></div>{/if}
</div>

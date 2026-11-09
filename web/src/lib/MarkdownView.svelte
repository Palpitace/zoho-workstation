<!-- input: 已保存或正在预览的 Markdown 正文。 -->
<!-- output: 本地解析、严格过滤 HTML 和外部图片的排版预览。 -->
<!-- pos: 知识库只读 Markdown 预览。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount } from "svelte";
  import DOMPurify from "dompurify";
  import "vditor/dist/index.css";
  let { value }: { value: string } = $props();
  let html = $state("");
  let error = $state("");
  let mounted = $state(false);
  onMount(() => { mounted = true; });
  $effect(() => {
    const text = value;
    if (!mounted) return;
    let cancelled = false;
    void (async () => {
      try {
        const { default: Vditor } = await import("vditor");
        const raw = await Vditor.md2html(text, { cdn: "/vendor/vditor", mode: "light", markdown: { sanitize: true, inlineMath: false } });
        const safe = DOMPurify.sanitize(raw, { ALLOWED_TAGS: ["p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote", "pre", "code", "strong", "em", "s", "del", "br", "hr", "table", "thead", "tbody", "tr", "td", "th", "a", "img", "sup", "sub", "input"], ALLOWED_ATTR: ["href", "src", "alt", "title", "colspan", "rowspan", "type", "checked", "disabled"] });
        const doc = new DOMParser().parseFromString(safe, "text/html");
        for (const img of doc.querySelectorAll("img")) {
          const src = img.getAttribute("src") || "";
          if (!/^\/api\/knowledge\/assets\/[a-zA-Z0-9._-]+$/.test(src)) {
            const note = doc.createElement("span"); note.textContent = `[图片未缓存：${img.alt || "图片"}]`; img.replaceWith(note);
          } else { img.setAttribute("loading", "lazy"); img.setAttribute("referrerpolicy", "no-referrer"); }
        }
        for (const link of doc.querySelectorAll("a")) {
          const href = link.getAttribute("href") || "";
          if (!/^(https?:\/\/|#)/i.test(href)) link.removeAttribute("href");
          else { link.setAttribute("rel", "noopener noreferrer"); if (!href.startsWith("#")) link.setAttribute("target", "_blank"); }
        }
        for (const input of doc.querySelectorAll("input")) { input.setAttribute("type", "checkbox"); input.setAttribute("disabled", ""); }
        if (!cancelled) { html = doc.body.innerHTML; error = ""; }
      } catch { if (!cancelled) error = "排版预览未加载，以下为 Markdown 原文。"; }
    })();
    return () => { cancelled = true; };
  });
</script>
{#if error}<p class="mb-3 text-sm text-destructive">{error}</p><pre class="whitespace-pre-wrap break-words text-sm">{value}</pre>{:else}<div class="vditor-reset min-w-0 break-words" aria-label="Markdown 预览">{@html html}</div>{/if}

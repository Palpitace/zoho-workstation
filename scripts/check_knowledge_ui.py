# input: 已构建前端、Playwright/Chromium 与合成知识/工单 API。
# output: 具名库/分类、批量审核、Markdown 编辑及建议引用/过期的浏览器验收。
# pos: 无真实资料和模型调用的知识界面回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import copy
import functools
import http.server
import json
import os
import threading
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright

root = Path(__file__).resolve().parents[1]
(root / ".cache/knowledge-ui").mkdir(parents=True, exist_ok=True)
status = {
    "sync": {"department_id": "7", "selected": 0, "ready": 0, "failed": 0},
    "runtime": {"automatic": True, "ai_scope": "all_synced", "last_error": {}},
    "jev_enabled": False,
    "bub_enabled": False,
    "csrf_token": "synthetic",
    "budget": {"total": 0, "round": 0},
    "ai": {},
}
md = "# Connection FAQ\n\n**Check the USB cable.**\n\n| Model | Action |\n| --- | --- |\n| D30 | Reconnect |\n\n![Device](/api/knowledge/assets/test.png)\n\n<script>window.pwned=true</script>\n\n[Bad](javascript:alert(1))"
entry = {
    "id": 2,
    "library_id": 1,
    "category_id": 1,
    "kind": "sop",
    "status": "published",
    "scope": "technical",
    "title": "Connection FAQ",
    "product": "D30",
    "applicable_products": ["D30"],
    "excluded_products": ["D30N"],
    "source_name": "synthetic.md",
    "source_hash": "synthetic2",
    "source_tickets": [],
    "message_ids": [],
    "markdown": md,
    "blocks": [{"id": "step", "text": "Check the USB cable."}],
    "created_at": "2026-11-09T00:00:00Z",
}
entries = {2: entry}
posts = []
libs = [{"id": 1, "name": "面单机知识库", "department_ids": ["7"]}]
categories = [{"id": 1, "library_id": 1, "name": "故障排查"}]
items = [
    {
        "id": "a",
        "title": "USB troubleshooting",
        "category": "故障排查",
        "scope": "reference",
        "markdown": "# USB\n\nCheck cable.",
        "status": "ready",
        "warnings": [],
        "requires_confirmation": False,
    },
    {
        "id": "b",
        "title": "Refund policy",
        "category": "会员咨询",
        "scope": "policy",
        "markdown": "# Policy\n\nConfirm published terms.",
        "status": "ready",
        "warnings": ["政策需人工核对"],
        "requires_confirmation": True,
    },
    {
        "id": "c",
        "title": "Missing picture",
        "category": "使用问题",
        "scope": "technical",
        "markdown": "# Image instructions",
        "status": "failed",
        "warnings": ["图片下载失败"],
        "requires_confirmation": True,
        "error": "素材缺失",
    },
]
message = {
    "id": "m1",
    "source": "thread",
    "created_time": "2026-11-09T01:00:00Z",
    "role": "customer",
    "visibility": "public",
    "body": "WiFi connection still fails",
    "attachments": [],
}
ticket = {
    "id": "1",
    "subject": "D30 WiFi connection",
    "product": "D30",
    "department_id": "7",
    "messages": [message],
    "complete": True,
    "pending": False,
    "sync_stopped": False,
    "status": "Open",
}
summary = {
    "device_info": [],
    "customer_feedback": [{"text": "WiFi 连接仍失败", "message_ids": ["m1"]}],
    "support_responses": [],
}
run = {"id": 1, "status": "success", "result": {"summary": summary}}
analysis = {
    "bub": {"current": run, "success": run, "stale": False},
    "jev": {"current": None, "success": None, "stale": False},
}
advice = {
    "steps": [
        {
            "action": "请先确认当前连接的 WiFi 网络",
            "reason": "已发布 SOP 要求先确认网络，当前客户尚未提供此信息。",
            "source_ids": ["knowledge:2:step"],
            "current_message_ids": ["m1"],
        }
    ],
    "missing_information": ["WiFi 网络频段"],
    "conflicts": [],
}
source = {
    "id": "knowledge:2:step",
    "entry_id": 2,
    "block_id": "step",
    "title": entry["title"],
    "kind": "sop",
    "product": "D30",
    "text": entry["blocks"][0]["text"],
    "source_hash": "synthetic",
    "message_ids": [],
}
advice_run = {
    "id": 3,
    "status": "success",
    "result": {"advice": advice, "sources": [source]},
}
knowledge = {
    "case": {"current": None, "success": None, "stale": False},
    "advice": {"current": advice_run, "success": advice_run, "stale": False},
    "enabled": True,
    "busy": False,
}

case = {
    **entry,
    "id": 4,
    "kind": "case",
    "title": "连接失败的客户执行记录",
    "scope": "reference",
    "source_ticket_id": "1",
    "message_ids": ["m1"],
    "markdown": "客户尚未确认执行，效果未知。",
    "blocks": [{"id": "case", "text": "客户尚未确认执行，效果未知。"}],
}
entries[4] = case


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def end_headers(self):
        self.send_header(
            "Content-Security-Policy",
            "img-src 'self' data:; object-src 'none'; frame-src 'none'",
        )
        super().end_headers()


server = http.server.ThreadingHTTPServer(
    ("127.0.0.1", 0), functools.partial(Quiet, directory=str(root / "web/dist"))
)
threading.Thread(target=server.serve_forever, daemon=True).start()
with sync_playwright() as p:
    browser = p.chromium.launch(
        executable_path=os.environ.get("PLAYWRIGHT_CHROMIUM_EXECUTABLE")
    )
    page = browser.new_page(
        viewport={"width": 1366, "height": 900}, reduced_motion="reduce"
    )
    errors = []
    external = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.on(
        "request",
        lambda r: (
            external.append(r.url)
            if not r.url.startswith(f"http://127.0.0.1:{server.server_port}")
            else None
        ),
    )

    def route(r):
        path = urlparse(r.request.url).path
        if r.request.method == "POST":
            assert r.request.headers.get("x-csrf-token") == "synthetic"
            posts.append(path)
            if path == "/api/knowledge/libraries":
                sent = json.loads(r.request.post_data)
                libs[0].update(sent)
                body = libs[0]
            elif path == "/api/knowledge/categories":
                sent = json.loads(r.request.post_data)
                body = {**sent, "id": 2}
                categories.append(body)
            elif path == "/api/knowledge/import-batch":
                body = {"id": "batch1", "items": items}
            elif path.endswith("/confirm"):
                sent = json.loads(r.request.post_data)
                assert sent["publish"] is False
                assert sent["item_ids"] == ["a"]
                assert sent["items"][0]["scope"] == "technical"
                body = {"data": [entry]}
            elif path == "/api/knowledge/retrieve":
                body = {
                    "mode": "hybrid",
                    "sources": [
                        {
                            "id": "knowledge:2:step",
                            "entry_id": 2,
                            "block_id": "step",
                            "title": "Connection FAQ",
                            "scope": "technical",
                            "kind": "sop",
                            "product": "D30",
                            "text": "Check the USB cable.",
                            "source_hash": "synthetic2",
                            "message_ids": [],
                        }
                    ],
                }
            elif path.endswith("/publish"):
                target = entries[int(path.split("/")[-2])]
                entries[target["replaces_id"]]["status"] = "archived"
                target["status"] = "published"
                body = {"status": "ok"}
            elif path == "/api/knowledge/assets":
                body = {"url": "/api/knowledge/assets/upload.png"}
            elif path == "/api/knowledge/draft":
                sent = json.loads(r.request.post_data)
                assert "markdown" in sent and "**" in sent["markdown"]
                body = copy.deepcopy(entry)
                body.update(sent, id=3, status="draft", replaces_id=2)
                entries[3] = body
            else:
                raise AssertionError(path)
        elif path == "/api/status":
            body = status
        elif path == "/api/knowledge/libraries":
            body = {"data": libs, "default_department_id": "7"}
        elif path == "/api/knowledge/import-batch/batch1":
            body = {"id": "batch1", "items": items}
        elif path == "/api/knowledge/categories":
            body = {"data": categories}
        elif path == "/api/knowledge/embedding":
            body = {"state": "missing", "degraded": True, "indexed": 0, "total": 1}
        elif path.endswith("/history"):
            body = {"data": list(entries.values())}
        elif path.startswith("/api/knowledge/assets/"):
            import base64

            r.fulfill(
                content_type="image/png",
                body=base64.b64decode(
                    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
                ),
            )
            return
        elif path.startswith("/api/knowledge/"):
            body = entries[int(path.rsplit("/", 1)[-1])]
        elif path == "/api/knowledge":
            body = {"data": list(entries.values())}
        elif path == "/api/tickets/1/knowledge":
            body = knowledge
        elif path == "/api/tickets/1/analysis":
            body = analysis
        elif path == "/api/tickets/1":
            body = ticket
        elif path == "/api/tickets":
            body = {"data": [ticket], "filters": {}}
        else:
            raise AssertionError(path)
        r.fulfill(json=body)

    page.route("**/api/**", route)
    base = f"http://127.0.0.1:{server.server_port}"
    page.goto(base + "/#/knowledge")
    expect(page.get_by_role("combobox", name="知识库", exact=True)).to_have_value("1")
    page.get_by_text("管理知识库与分类", exact=True).click()
    page.get_by_role("button", name="编辑当前知识库", exact=True).click()
    page.get_by_label("知识库名称", exact=True).fill("面单机技术知识库")
    page.get_by_label("关联部门 ID（逗号分隔）", exact=True).fill("7, 8")
    page.get_by_role("button", name="更新当前知识库", exact=True).click()
    expect(page.get_by_role("combobox", name="知识库", exact=True)).to_contain_text(
        "面单机技术知识库"
    )
    assert libs[0]["department_ids"] == ["7", "8"]
    page.get_by_label("分类名称", exact=True).fill("新增技术分类")
    page.get_by_role("button", name="新增", exact=True).click()
    expect(page.get_by_role("combobox", name="知识分类", exact=True)).to_contain_text(
        "新增技术分类"
    )
    page.get_by_text("管理知识库与分类", exact=True).click()
    page.get_by_text("批量导入", exact=True).click()
    page.get_by_label("ZIP / Markdown / Word / PDF", exact=True).set_input_files(
        [
            {
                "name": "a.md",
                "mimeType": "text/markdown",
                "buffer": b"# USB\nCheck cable.",
            },
            {"name": "b.md", "mimeType": "text/markdown", "buffer": b"# Policy"},
        ]
    )
    page.get_by_role("button", name="导入预览", exact=True).click()
    expect(page.get_by_label("选择 USB troubleshooting", exact=True)).to_be_checked()
    expect(page.get_by_label("选择 Refund policy", exact=True)).not_to_be_checked()
    expect(page.get_by_label("选择 Missing picture", exact=True)).to_be_disabled()
    import_article = page.locator("article").filter(
        has=page.get_by_role("heading", name="USB troubleshooting", exact=True)
    )
    import_article.get_by_text("查看并修订内容", exact=True).click()
    import_article.get_by_role("combobox", name="知识用途", exact=True).select_option(
        "technical"
    )
    import_article.get_by_text("查看并修订内容", exact=True).click()
    page.get_by_role("button", name="全选待审核项", exact=True).click()
    expect(page.get_by_label("选择 Refund policy", exact=True)).to_be_checked()
    expect(page.get_by_role("button", name="确认发布所选", exact=True)).to_be_disabled()
    page.get_by_label("选择 Refund policy", exact=True).uncheck()
    page.get_by_role("button", name="保存所选为草稿", exact=True).click()
    expect(page.get_by_text("已保存草稿 1 条。", exact=True)).to_be_visible()
    page.get_by_text("检索测试与本地模型", exact=True).click()
    page.get_by_label("检索问题", exact=True).fill("device disconnected")
    page.get_by_role("button", name="检索", exact=True).click()
    expect(page.get_by_text("关键词 + 语义 · 1 条命中", exact=True)).to_be_visible()
    page.screenshot(
        path=str(root / ".cache/knowledge-ui/library-desktop.png"), full_page=True
    )
    page.goto(base + "/#/knowledge/2")
    expect(
        page.get_by_role("heading", name="Connection FAQ", exact=True).first
    ).to_be_visible()
    expect(page.get_by_label("Markdown 预览").locator("strong")).to_have_text(
        "Check the USB cable."
    )
    expect(page.get_by_label("Markdown 预览").locator("table")).to_be_visible()
    assert not page.evaluate("window.pwned || false")
    assert page.locator('a[href^="javascript:"]').count() == 0
    page.get_by_role("button", name="编辑为新草稿", exact=True).click()
    expect(page.get_by_role("button", name="可视编辑", exact=True)).to_be_enabled(
        timeout=30000
    )
    page.locator(".vditor input[type=file]").set_input_files(
        {"name": "test.png", "mimeType": "image/png", "buffer": b"synthetic"}
    )
    page.wait_for_timeout(500)
    assert "/api/knowledge/assets" in posts
    page.get_by_role("button", name="Markdown", exact=True).click()
    field = page.get_by_label("Markdown 正文", exact=True)
    assert "Check the USB cable." in field.input_value()
    field.fill(
        "# Updated FAQ\n\n**Check the cable before reset.**\n\n![Device](/api/knowledge/assets/test.png)"
    )
    page.get_by_role("button", name="可视编辑", exact=True).click()
    page.get_by_role("button", name="Markdown", exact=True).click()
    expect(field).to_have_value(
        "# Updated FAQ\n\n**Check the cable before reset.**\n\n![Device](/api/knowledge/assets/test.png)\n"
    )
    page.get_by_role("button", name="预览", exact=True).click()
    expect(page.get_by_label("Markdown 预览").first.locator("strong")).to_have_text(
        "Check the cable before reset."
    )
    page.set_viewport_size({"width": 390, "height": 844})
    page.get_by_role("button", name="可视编辑", exact=True).click()
    page.screenshot(
        path=str(root / ".cache/knowledge-ui/visual-editor-mobile.png"), full_page=True
    )
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    page.get_by_role("button", name="保存新草稿", exact=True).click()
    expect(page.get_by_text("未发布草稿，确认发布后生效。", exact=True)).to_be_visible()
    assert entries[2]["status"] == "published"
    page.set_viewport_size({"width": 390, "height": 844})
    page.screenshot(
        path=str(root / ".cache/knowledge-ui/editor-mobile.png"), full_page=True
    )
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    page.goto(base + "/#/tickets/1")
    expect(page.get_by_role("heading", name="参考排查建议", exact=True)).to_be_visible()
    expect(
        page.get_by_text("1. " + advice["steps"][0]["action"], exact=True)
    ).to_be_visible()
    page.get_by_role("link", name="SOP：" + entry["title"], exact=True).click()
    expect(page.locator("#knowledge-block-step")).to_be_focused()
    page.goto(base + "/#/knowledge/4")
    page.get_by_role("link", name="消息 m1", exact=True).click()
    expect(page.locator("#message-thread-m1")).to_be_focused()
    expect(page.locator("#conversation")).to_have_attribute("open", "")
    page.get_by_role("button", name="当前工单消息 m1", exact=True).click()
    expect(page.get_by_role("button", name="返回建议", exact=True)).to_be_visible()
    page.get_by_role("button", name="返回建议", exact=True).click()
    page.get_by_role(
        "heading", name="参考排查建议", exact=True
    ).scroll_into_view_if_needed()
    page.screenshot(
        path=str(root / ".cache/knowledge-ui/advice-mobile.png"), full_page=True
    )
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    knowledge["advice"]["stale"] = True
    page.reload()
    expect(
        page.get_by_text(
            "工单或参考资料已变化，旧建议不再作为当前处理依据。", exact=True
        )
    ).to_be_visible()
    expect(
        page.get_by_text("1. " + advice["steps"][0]["action"], exact=True)
    ).to_have_count(0)
    page.goto(base + "/#/knowledge/3")
    page.get_by_role("button", name="确认发布", exact=True).click()
    expect(page.get_by_text("已发布，可用于工单建议。", exact=True)).to_be_visible()
    assert entries[3]["status"] == "published" and entries[2]["status"] == "archived"
    assert not page.evaluate("window.pwned || false")
    assert not errors, errors
    assert not external, external
    print(
        "PASS: batch preview/risk selection/draft save, hybrid citations, sanitized Markdown table/images, visual/source roundtrip, immutable draft,390px layout,advice citations/stale, explicit publish, no external requests; posts",
        len(posts),
    )
    browser.close()
server.shutdown()

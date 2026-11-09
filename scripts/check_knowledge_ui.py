# input: 已构建前端、Playwright/Chromium 与合成知识/工单 API。
# output: 导入审核、显式发布、修订隔离、建议来源/消息定位及过期移动端验收。
# pos: 无真实数据或模型调用的知识界面回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import copy
import functools
import http.server
import json
import os
from pathlib import Path
import threading
from playwright.sync_api import sync_playwright, expect

root = Path(__file__).resolve().parents[1]
(root / ".cache/knowledge-ui").mkdir(parents=True, exist_ok=True)
status = {
    "sync": {"department_id": "7", "selected": 2, "ready": 2, "failed": 0},
    "runtime": {"automatic": True, "ai_scope": "all_synced", "last_error": {}},
    "jev_enabled": True,
    "bub_enabled": True,
    "csrf_token": "synthetic",
    "budget": {"total": 1, "round": 1},
    "ai": {},
}
entry = {
    "id": 1,
    "kind": "sop",
    "status": "draft",
    "title": "D30 WiFi 连接排查",
    "product": "D30",
    "source_name": "wifi.pdf",
    "source_hash": "synthetic",
    "message_ids": [],
    "blocks": [
        {
            "id": "page-2",
            "text": "先核对 WiFi 网络。<script>throw Error('unsafe')</script>\n确认适用条件后再处理。",
        }
    ],
    "created_at": "2026-11-09T01:00:00Z",
}
case = {
    "id": 2,
    "kind": "case",
    "status": "published",
    "title": "连接失败的客户执行记录",
    "product": "D30",
    "source_name": "工单案例",
    "source_hash": "case-synthetic",
    "source_ticket_id": "1",
    "message_ids": ["m1"],
    "blocks": [
        {"id": "case", "text": "客服建议检查网络。客户尚未确认执行；效果未知。"}
    ],
    "created_at": "2026-11-09T01:00:00Z",
}
entries = {1: entry, 2: case}
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
            "source_ids": ["knowledge:1:page-2"],
            "current_message_ids": ["m1"],
        }
    ],
    "missing_information": ["WiFi 网络频段"],
    "conflicts": [],
}
source = {
    "id": "knowledge:1:page-2",
    "entry_id": 1,
    "block_id": "page-2",
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
posts = []


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


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
    page.on("pageerror", lambda error: errors.append(str(error)))

    def route(r):
        from urllib.parse import urlparse

        path = urlparse(r.request.url).path
        method = r.request.method
        code = 200
        if method == "POST":
            assert r.request.headers.get("x-csrf-token") == "synthetic"
            posts.append((path, r.request.post_data or ""))
            if path == "/api/knowledge/import":
                assert "multipart/form-data" in r.request.headers["content-type"]
                new = copy.deepcopy(entry)
                new["id"] = 3
                new["status"] = "draft"
                new["title"] = "导入文档预览"
                entries[3] = new
                body = new
                code = 201
            elif path == "/api/knowledge/draft":
                data = json.loads(r.request.post_data)
                base = entries[data.get("source_entry_id") or data.get("replaces_id")]
                new = copy.deepcopy(base)
                new.update(
                    id=4,
                    kind="sop",
                    status="draft",
                    title=data["title"],
                    replaces_id=data.get("replaces_id", 0),
                )
                new["blocks"] = [
                    {
                        "id": "section-1",
                        "text": data["text"] or base["blocks"][0]["text"],
                    }
                ]
                entries[4] = new
                body = new
                code = 201
            elif path.endswith("/publish"):
                entries[int(path.split("/")[-2])]["status"] = "published"
                body = {"status": "ok"}
            elif path.endswith("/archive"):
                entries[int(path.split("/")[-2])]["status"] = "archived"
                body = {"status": "ok"}
            elif path.endswith("/analysis/advice"):
                body = {"status": "accepted"}
                code = 202
            else:
                raise AssertionError(path)
        elif path == "/api/status":
            body = status
        elif path == "/api/knowledge":
            body = {"data": list(entries.values())}
        elif path.startswith("/api/knowledge/"):
            body = entries[int(path.rsplit("/", 1)[-1])]
        elif path.endswith("/knowledge"):
            body = knowledge
        elif path.endswith("/analysis"):
            body = analysis
        elif path == "/api/tickets/1":
            body = ticket
        elif path == "/api/tickets":
            body = {"data": [ticket], "filters": {}}
        else:
            raise AssertionError(path)
        r.fulfill(status=code, json=body)

    page.route("**/api/**", route)
    base = f"http://127.0.0.1:{server.server_port}"
    page.goto(base + "/#/knowledge")
    expect(page.get_by_role("heading", name="知识库", exact=True)).to_be_visible()
    expect(page.get_by_role("link", name=entry["title"], exact=True)).to_be_visible()
    assert not posts
    page.get_by_role("link", name=entry["title"], exact=True).click()
    expect(page.get_by_role("button", name="确认发布", exact=True)).to_be_enabled()
    expect(page.get_by_text(entry["blocks"][0]["text"], exact=True)).to_be_visible()
    assert not posts
    page.screenshot(
        path=str(root / ".cache/knowledge-ui/draft-desktop.png"), full_page=True
    )
    page.get_by_role("button", name="确认发布", exact=True).click()
    expect(page.get_by_text("已发布，可用于工单建议。", exact=True)).to_be_visible()
    assert len(posts) == 1 and posts[-1][0].endswith("/publish")
    page.get_by_role("button", name="编辑为新草稿", exact=True).click()
    page.get_by_label("SOP 草稿内容", exact=True).fill(
        "核对 WiFi 频段，再确认连接环境。"
    )
    page.get_by_role("button", name="保存新草稿", exact=True).click()
    expect(page.get_by_role("button", name="确认发布", exact=True)).to_be_visible()
    assert entries[1]["status"] == "published" and entries[4]["status"] == "draft"
    assert json.loads(posts[-1][1])["source_entry_id"] == 1
    page.goto(base + "/#/knowledge")
    page.get_by_text("导入文档", exact=True).click()
    page.get_by_label("资料文件", exact=True).set_input_files(
        {
            "name": "steps.md",
            "mimeType": "text/markdown",
            "buffer": b"# WiFi\nCheck network.",
        }
    )
    page.get_by_role("button", name="导入并预览", exact=True).click()
    expect(page.get_by_role("heading", name="导入文档预览", exact=True)).to_be_visible()
    assert entries[3]["status"] == "draft"
    page.goto(base + "/#/tickets/1")
    expect(page.get_by_role("heading", name="参考排查建议", exact=True)).to_be_visible()
    expect(
        page.get_by_text("1. " + advice["steps"][0]["action"], exact=True)
    ).to_be_visible()
    page.get_by_role("link", name="SOP：" + entry["title"], exact=True).click()
    expect(page.locator("#knowledge-block-page-2")).to_be_focused()
    page.goto(base + "/#/knowledge/2")
    page.get_by_role("link", name="消息 m1", exact=True).click()
    expect(page.locator("#message-thread-m1")).to_be_focused()
    expect(page.locator("#conversation")).to_have_attribute("open", "")
    page.get_by_role("button", name="当前工单消息 m1", exact=True).click()
    expect(page.get_by_role("button", name="返回建议", exact=True)).to_be_visible()
    page.get_by_role("button", name="返回建议", exact=True).click()
    page.set_viewport_size({"width": 390, "height": 844})
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
    expect(page.get_by_role("heading", name="导入文档预览", exact=True)).to_be_visible()
    page.screenshot(
        path=str(root / ".cache/knowledge-ui/draft-mobile.png"), full_page=True
    )
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    assert not errors, errors
    print(
        "Synthetic knowledge UI: import/draft/publish/edit/advice/citations/stale/mobile passed; POSTs:",
        len(posts),
    )
    browser.close()
server.shutdown()

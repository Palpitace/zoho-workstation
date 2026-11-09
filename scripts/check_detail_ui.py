# input: 已构建静态前端及完全合成的详情/分析 API 响应。
# output: 引用折叠/安全展示和稳定刷新、错误保留、导航竞争的浏览器验收。
# pos: 详情界面离线回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import copy
import functools
import http.server
import os
from pathlib import Path
import threading
from playwright.sync_api import sync_playwright, expect

status = {
    "sync": {"department_id": "7", "selected": 1, "ready": 1, "failed": 0},
    "runtime": {"automatic": True, "ai_scope": "all_synced", "last_error": {}},
    "jev_enabled": True,
    "bub_enabled": True,
    "csrf_token": "synthetic",
    "budget": {"total": 1, "round": 1},
    "ai": {},
}
message = {
    "id": "m1",
    "source": "thread",
    "created_time": "2026-11-05T01:00:00Z",
    "role": "customer",
    "visibility": "public",
    "body": "New reply\nOld reply\nOlder reply\n<script>alert(1)</script>",
    "attachments": [],
}
ticket = {
    "id": "1",
    "subject": "Synthetic quote test",
    "department_id": "7",
    "messages": [message],
    "complete": True,
    "pending": False,
    "sync_stopped": False,
    "status": "Open",
    "message_segments": {
        "thread:m1": [
            {"text": "New reply\n"},
            {
                "quote": True,
                "children": [
                    {"text": "Old reply\n"},
                    {"quote": True, "children": [{"text": "Older reply\n"}]},
                ],
            },
            {"text": "<script>alert(1)</script>"},
        ]
    },
}
run = {
    "id": 1,
    "status": "success",
    "result": {
        "summary": {
            "device_info": [],
            "customer_feedback": [{"text": "Synthetic summary", "message_ids": ["m1"]}],
            "support_responses": [],
        }
    },
}
analysis = {
    "bub": {"success": run, "current": run, "stale": False},
    "jev": {"success": None, "current": None, "stale": False},
}


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


server = http.server.ThreadingHTTPServer(
    ("127.0.0.1", 0),
    functools.partial(
        QuietHandler, directory=str(Path(__file__).resolve().parents[1] / "web/dist")
    ),
)
threading.Thread(target=server.serve_forever, daemon=True).start()
state = {"mode": "ok", "held": [], "calls": 0}
with sync_playwright() as p:
    browser = p.chromium.launch(
        executable_path=os.environ.get("PLAYWRIGHT_CHROMIUM_EXECUTABLE")
    )
    page = browser.new_page(reduced_motion="reduce")
    page.clock.install()
    errors = []
    page.on("pageerror", lambda e: errors.append(str(e)))

    def route(r):
        path = r.request.url.split("?")[0]
        if path.endswith("/knowledge"):
            empty = {"current": None, "success": None, "stale": False}
            r.fulfill(json={"case": empty, "advice": empty, "enabled": False, "busy": False})
            return
        state["calls"] += 1
        if state["mode"] == "offline":
            r.abort("internetdisconnected")
            return
        if path.endswith("/status"):
            body = status
        elif path.endswith("/analysis"):
            if state["mode"] == "fail_analysis":
                r.fulfill(status=500, json={"error": "synthetic"})
                return
            body = analysis
        elif "/tickets/" in path:
            body = copy.deepcopy(ticket)
            body["id"] = path.rsplit("/", 1)[-1]
            if body["id"] == "2":
                body["subject"] = "Second synthetic ticket"
        else:
            raise AssertionError(path)
        if state["mode"] == "hold" and "/tickets/1" in path:
            state["held"].append((r, body))
            return
        r.fulfill(json=body)

    page.route("**/api/**", route)
    page.goto(f"http://127.0.0.1:{server.server_port}/#/tickets/1")
    expect(
        page.get_by_role("heading", name="Synthetic quote test", exact=True)
    ).to_be_visible()
    page.locator("#conversation > summary").focus()
    page.keyboard.press("Enter")
    quotes = page.locator("details[data-message-quote]")
    expect(quotes).to_have_count(2)
    assert quotes.evaluate_all("(nodes)=>nodes.every(n=>!n.open)")
    expect(page.get_by_text("New reply", exact=True)).to_be_visible()
    expect(page.get_by_text("Older reply", exact=True)).not_to_be_visible()
    expect(page.get_by_text("<script>alert(1)</script>", exact=True)).to_be_visible()
    quotes.first.locator(":scope > summary").focus()
    page.keyboard.press("Enter")
    assert quotes.first.evaluate("(n)=>n.open")
    quotes.first.locator(":scope > summary").focus()
    page.keyboard.press("Enter")
    page.get_by_role("button", name="查看引用消息 m1", exact=True).click()
    assert quotes.evaluate_all("(nodes)=>nodes.every(n=>n.open)")
    expect(page.get_by_text("Older reply", exact=True)).to_be_visible()
    page.clock.run_for(5500)
    assert quotes.evaluate_all("(nodes)=>nodes.every(n=>n.open)")
    page.get_by_role("button", name="返回总结", exact=True).click()
    # Refresh retains the same content/height while both real requests are pending.
    section = page.get_by_role("region", name="工单详情", exact=True)
    baseline = section.bounding_box()["height"]
    state["mode"] = "hold"
    page.get_by_role("button", name="刷新数据", exact=True).click()
    expect(page.get_by_role("button", name="刷新数据", exact=True)).to_be_disabled()
    page.wait_for_timeout(50)
    assert len(state["held"]) == 2
    assert section.bounding_box()["height"] == baseline
    expect(page.get_by_text("Synthetic summary", exact=True)).to_be_visible()
    before = state["calls"]
    page.clock.run_for(11000)
    assert state["calls"] == before
    # Programmatic repeated click on a disabled button must not start duplicate fetches.
    page.get_by_role("button", name="刷新数据", exact=True).evaluate("(n)=>n.click()")
    assert state["calls"] == before
    state["mode"] = "ok"
    for pending, body in state["held"]:
        pending.fulfill(json=body)
    state["held"] = []
    expect(page.get_by_role("button", name="刷新数据", exact=True)).to_be_enabled()
    assert section.bounding_box()["height"] == baseline
    state["mode"] = "fail_analysis"
    page.get_by_role("button", name="刷新数据", exact=True).click()
    expect(page.get_by_role("button", name="刷新数据", exact=True)).to_be_enabled()
    expect(page.get_by_text("Synthetic summary", exact=True)).to_be_visible()
    expect(page.get_by_role("alert").filter(has_text="刷新失败").first).to_be_visible()
    state["mode"] = "offline"
    page.get_by_role("button", name="刷新数据", exact=True).click()
    expect(page.get_by_role("button", name="刷新数据", exact=True)).to_be_enabled()
    expect(
        page.get_by_role("heading", name="Synthetic quote test", exact=True)
    ).to_be_visible()
    expect(page.get_by_text("Synthetic summary", exact=True)).to_be_visible()
    state["mode"] = "ok"
    page.get_by_role("button", name="刷新数据", exact=True).click()
    expect(page.get_by_role("alert").filter(has_text="刷新失败")).to_have_count(0)
    # Navigate during a slow refresh; the obsolete response cannot replace ticket 2.
    state["mode"] = "hold"
    page.get_by_role("button", name="刷新数据", exact=True).click()
    page.wait_for_timeout(50)
    assert len(state["held"]) == 2
    page.evaluate("window.location.hash='#/tickets/2'")
    expect(
        page.get_by_role("heading", name="Second synthetic ticket", exact=True)
    ).to_be_visible()
    for pending, body in state["held"]:
        pending.fulfill(json=body)
    state["held"] = []
    state["mode"] = "ok"
    page.wait_for_timeout(100)
    expect(
        page.get_by_role("heading", name="Second synthetic ticket", exact=True)
    ).to_be_visible()
    assert not errors, errors
    if screenshot := os.environ.get("DETAIL_UI_SCREENSHOT"):
        page.screenshot(path=screenshot, full_page=True)
    browser.close()
server.shutdown()
server.server_close()
print(
    "PASS quotes/default/nested/keyboard/text-only/reference; stable height, delayed/failed/offline refresh, no duplicate fetches, stale navigation responses"
)

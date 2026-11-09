# input: 已构建 web/dist、Playwright/Chromium 和脚本内合成 API 响应。
# output: Bub活动/重试状态一致性、逐票刷新及恢复确认的浏览器证据。
# pos: 无真实服务调用的 AI 界面验收。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import copy
import functools
import http.server
import os
from pathlib import Path
import threading
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect, sync_playwright


class StaticHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass


def main():
    dist = Path(__file__).resolve().parents[1] / "web" / "dist"
    if not (dist / "index.html").exists():
        raise SystemExit("Run pnpm --dir web build first")
    server = http.server.ThreadingHTTPServer(
        ("127.0.0.1", 0), functools.partial(StaticHandler, directory=str(dist))
    )
    threading.Thread(target=server.serve_forever, daemon=True).start()
    success = {"status": "success", "stale": False, "automatic_eligible": True}
    failed = {"status": "failed", "stale": True, "next_retry_at": "2026-11-05T03:00:00Z",
              "last_failed_at": "2026-11-05T01:00:00Z", "failure_before_start": True,
              "error": "Bub: summary_failed", "automatic_eligible": True}
    status = {
        "sync": {"department_id": "synthetic", "selected": 30, "ready": 30, "failed": 0},
        "jev_enabled": True, "bub_enabled": True, "wecom_enabled": False,
        "csrf_token": "synthetic-token", "budget": {"total": 60, "round": 60},
        "runtime": {"automatic": True, "ai_scope": "all_synced", "last_error": {},
                    "started_at": "2026-11-05T02:00:00Z", "recovery_pending": {"bub": 0, "jev": 0}},
        "ai": {kind: {"pending": 0, "running": 0, "failed": 1, "success": 29,
                      "stale": 1, "retry_waiting": 1} for kind in ("jev", "bub")},
        "retry_candidates": {"bub": [{"ticket_id": "1", "run_id": 2}], "jev": []},
    }
    state = {"changed": False, "requests": [], "hold": False, "held": [], "posts": []}

    def response():
        rows = []
        for index in range(30):
            failing = index == (0 if state["changed"] else 1)
            rows.append({"id": str(index + 1), "subject": f"Synthetic ticket {index + 1}",
                         "status": "Open", "product": "D520", "complete": True,
                         "pending": False, "sync_stopped": False, "sync_status": "synced",
                         "ai": {"bub": copy.deepcopy(failed if failing else success),
                                "jev": copy.deepcopy(success)}})
        return {"data": rows, "filters": {"product": [{"value": "D520", "count": 30}]}}

    def api(route):
        path = urlparse(route.request.url).path
        if route.request.method == "POST":
            state["posts"].append(route.request.post_data_json)
            route.fulfill(json={"status": "accepted"}, status=202)
        elif path.endswith("/knowledge"):
            empty = {"current": None, "success": None, "stale": False}
            route.fulfill(json={"case": empty, "advice": empty, "enabled": False, "busy": False})
        elif path == "/api/tickets/1/analysis":
            route.fulfill(json={"jev": {"current": None, "success": None, "stale": False}, "bub": {
                "current": {"id": 2, "status": "failed", "error": "Bub: model_rate_limited; HTTP 429; request_id=req-safe"},
                "success": None, "stale": False, "task_state": "retry_waiting", "attempts": 2,
                "next_retry_at": "2026-11-09T12:00:00Z"}})
        elif path == "/api/tickets/1":
            route.fulfill(json={"id":"1", "subject":"Synthetic ticket 1", "complete":True, "messages":[], "status":"Open"})
        elif path == "/api/status":
            route.fulfill(json=status)
        elif path == "/api/tickets":
            state["requests"].append(route.request.url)
            if state["hold"]:
                state["hold"] = False
                state["held"].append((route, response()))
            else:
                route.fulfill(json=response())
        else:
            raise AssertionError(f"Unexpected API route: {path}")

    try:
        with sync_playwright() as playwright:
            launch = {"headless": True}
            if executable := os.environ.get("PLAYWRIGHT_CHROMIUM_EXECUTABLE"):
                launch["executable_path"] = executable
            browser = playwright.chromium.launch(**launch)
            page = browser.new_page(viewport={"width": 1440, "height": 850})
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.route("**/api/**", api)
            page.clock.install()
            page.goto(f"http://127.0.0.1:{server.server_port}/")
            row = page.get_by_role("row").filter(has=page.get_by_role("link", name="Synthetic ticket 1", exact=True))
            expect(row).to_be_visible()
            page.get_by_label("搜索关键词").fill("Synthetic")
            page.get_by_role("button", name="查询", exact=True).click()
            page.get_by_role("combobox", name="筛选产品", exact=True).click()
            page.get_by_role("option").filter(has_text="D520").click()
            page.get_by_label("搜索关键词").fill("unsubmitted draft")
            page.evaluate("window.scrollTo(0, 400)")
            scroll = page.evaluate("window.scrollY")
            state["changed"] = True
            page.clock.run_for(5100)
            expect(row.get_by_text("等待重试", exact=True)).to_be_attached()
            assert "历史失败" in row.get_by_text("等待重试", exact=True).get_attribute("title")
            assert page.evaluate("window.scrollY") == scroll
            assert parse_qs(urlparse(state["requests"][-1]).query)["q"] == ["Synthetic"]
            assert parse_qs(urlparse(state["requests"][-1]).query)["product"] == ["D520"]
            expect(page.get_by_label("搜索关键词")).to_have_value("unsubmitted draft")

            # Hidden documents pause polling; becoming visible catches up immediately.
            page.evaluate("Object.defineProperty(document, 'visibilityState', {configurable:true, value:'hidden'})")
            before = len(state["requests"])
            state["changed"] = False
            page.clock.run_for(11000)
            assert len(state["requests"]) == before
            page.evaluate("Object.defineProperty(document, 'visibilityState', {configurable:true, value:'visible'}); document.dispatchEvent(new Event('visibilitychange'))")
            expect(row.get_by_text("等待重试", exact=True)).to_have_count(0)

            # A slow poll must not overlap another poll or overwrite a newer query.
            state["hold"] = True
            page.clock.run_for(5100)
            page.wait_for_function("true")
            assert len(state["held"]) == 1
            before = len(state["requests"])
            page.clock.run_for(11000)
            assert len(state["requests"]) == before
            state["changed"] = True
            page.get_by_role("button", name="查询", exact=True).click()
            expect(row.get_by_text("等待重试", exact=True)).to_be_attached()
            held, old = state["held"].pop()
            held.fulfill(json=old)
            page.wait_for_timeout(100)
            expect(row.get_by_text("等待重试", exact=True)).to_be_attached()

            # Recovery never POSTs just by entering the screen or opening/canceling confirmation.
            page.get_by_role("link", name="运行状态", exact=True).click()
            button = page.get_by_role("button", name="立即重试失败的 Bub（1）", exact=True)
            button.click()
            page.get_by_role("button", name="取消", exact=True).click()
            assert not state["posts"]
            button.click()
            page.get_by_role("button", name="确认重试", exact=True).click()
            expect(page.get_by_text("已受理 1 张 Bub 重试，每张只执行一次。", exact=True)).to_be_visible()
            assert state["posts"] == [{"candidates": [{"ticket_id": "1", "run_id": 2}]}]
            status["runtime"]["bub_running"] = True
            status["bub_tasks"] = {"unit":"tasks", "counts":{"pending":1,"running":1,"retry_waiting":1,"manual":0,"waiting":0},
                "active":[{"ticket_id":"2","ticket_number":"456","kind":"case","state":"running","started_at":"2026-11-09T11:00:00Z","elapsed_seconds":15}],
                "tasks":[{"ticket_id":"1","ticket_number":"123","kind":"bub","state":"retry_waiting","attempts":2,"error":"Bub: model_rate_limited; HTTP 429; request_id=req-safe","next_retry_at":"2026-11-09T12:00:00Z"}]}
            page.get_by_role("button", name="刷新数据", exact=True).click()
            panel=page.locator("section[aria-labelledby='bub-tasks-title']")
            expect(panel.get_by_text("Bub 任务", exact=True)).to_be_visible()
            expect(panel.get_by_role("link",name="工单 #456",exact=True)).to_have_attribute("href", "#/tickets/2")
            expect(panel.get_by_text("已运行 15 秒", exact=False)).to_be_visible()
            panel.locator("summary").click()
            expect(panel.get_by_text("已尝试 2 次",exact=False)).to_be_visible()
            panel.get_by_role("link",name="工单 #123",exact=True).click()
            summary=page.locator("section[aria-labelledby='bub-title']")
            expect(summary.get_by_text("等待重试",exact=True)).to_be_visible()
            expect(summary.get_by_text("其他分析任务正在执行",exact=False)).to_be_visible()
            expect(summary.get_by_text("最近总结失败",exact=True)).to_have_count(0)
            expect(summary.get_by_text("上游限流（429）",exact=False)).to_be_visible()
            assert not errors, errors
            if screenshot := os.environ.get("AI_UI_SCREENSHOT"):
                page.screenshot(path=screenshot, full_page=True)
            browser.close()
        print("PASS: constant totals, filters/scroll/draft, visibility, no overlap/stale overwrite, explicit recovery confirmation")
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()

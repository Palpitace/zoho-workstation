# input: 合成会话、模型结果、SDK 模拟错误及隔离代理/缺依赖环境。
# output: 429/HTTP/格式异常区分、敏感内容隔离和总结引用契约证据。
# pos: Python 总结契约的最小验证。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import asyncio
import copy
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

import httpx2
import main
from pydantic import ValidationError

from main import Snapshot, validate_summary, validation_diagnostics


class SummaryContractTest(unittest.TestCase):
    def test_optional_device_evidence_does_not_block_summary(self):
        snapshot = Snapshot.model_validate({"conversation_hash": "synthetic", "product": "P1", "messages": [
            {"id": "m1", "role": "customer", "body": "Labels overlap"},
            {"id": "m2", "role": "agent", "body": "Please calibrate", "visibility": "public"},
        ]})
        base = {"customer_feedback": [{"text": "Labels overlap", "message_ids": ["m1"]}],
                "support_responses": [{"text": "Support suggested calibration", "message_ids": ["m2"]}]}
        for devices in (None, [], [{"text": "No device provided", "message_ids": []}],
                        [{"text": "Unverified printer", "message_ids": ["unknown"]}]):
            with self.subTest(devices=devices):
                value = copy.deepcopy(base)
                if devices is not None:
                    value["device_info"] = devices
                result = validate_summary(json.dumps(value), snapshot).model_dump()
                self.assertEqual(result, {"device_info": [], **base})
        value = {"device_info": [{"text": "P1", "message_ids": []},
                                 {"text": "Unsupported", "message_ids": []}], **base}
        self.assertEqual(len(validate_summary(json.dumps(value), snapshot).device_info), 1)
        value["customer_feedback"][0]["message_ids"] = []
        with self.assertRaises(ValidationError):
            validate_summary(json.dumps(value), snapshot)

    def test_proxy_transports_construct_without_network(self):
        async def construct():
            async with httpx2.AsyncClient(timeout=3):
                pass

        for key in ("ALL_PROXY", "all_proxy"):
            for mixed in (False, True):
                for scheme in ("socks5", "socks5h"):
                    env = {key: f"{scheme}://synthetic:private@127.0.0.1:7890"}
                    if mixed:
                        env.update(HTTP_PROXY="http://127.0.0.1:7891", HTTPS_PROXY="http://127.0.0.1:7891")
                    with self.subTest(key=key, mixed=mixed, scheme=scheme), patch.dict(os.environ, env, clear=True):
                        asyncio.run(construct())

    def test_offline_check_reports_safe_local_failures(self):
        env = {k: v for k, v in os.environ.items() if k.lower() not in ("http_proxy", "https_proxy", "all_proxy", "no_proxy")}
        env["ALL_PROXY"] = "socks5h://synthetic:private@127.0.0.1:7890"
        entry = str(Path(__file__).with_name("main.py"))
        with tempfile.TemporaryDirectory() as directory:
            def check(expected):
                result = subprocess.run([sys.executable, entry, "--check"], input="", text=True,
                                        capture_output=True, env=env, timeout=10)
                self.assertEqual(json.loads(result.stdout), expected)
                self.assertEqual(result.returncode, 0 if expected["ok"] else 1)
                self.assertNotIn("synthetic:private", result.stdout + result.stderr)

            check({"ok": True})
            # Simulate an incomplete distributed environment at its import boundary.
            Path(directory, "socksio.py").write_text("raise ImportError('synthetic:private')")
            env["PYTHONPATH"] = directory
            check({"ok": False, "error": "proxy_dependency_missing"})
            payload = json.dumps({"input": {"conversation_hash": "synthetic", "messages": []},
                                  "config": {"api_base": "https://model.invalid", "api_key": "synthetic-private-key", "model": "deepseek:synthetic"},
                                  "schema_version": "bub-summary-v6"})
            failure = subprocess.run([sys.executable, entry], input=payload, text=True,
                                     capture_output=True, env=env, timeout=10)
            self.assertEqual(json.loads(failure.stdout), {"ok": False, "error": "proxy_dependency_missing"})
            self.assertEqual(failure.returncode, 1)
            self.assertNotIn("synthetic-private", failure.stdout + failure.stderr)
            env.pop("PYTHONPATH")
            env["ALL_PROXY"] = "unsupported://synthetic:private@127.0.0.1:7890"
            check({"ok": False, "error": "proxy_configuration_invalid"})

    def test_single_incoming_message_without_support_reply(self):
        for role, direction, visibility, status, text, accepted in (
            ("customer", "in", "public", "SUCCESS", "Printing overlaps", True),
            ("unknown", "in", "public", "SUCCESS", "Labels overlap", True),
            ("unknown", "in", "public", "SUCCESS", "Customer confirmed a defect", False),
            ("agent", "in", "public", "SUCCESS", "Labels overlap", False),
            ("unknown", "in", "private", "SUCCESS", "Labels overlap", False),
            ("unknown", "out", "public", "SUCCESS", "Labels overlap", False),
            ("unknown", "in", "public", "DRAFT", "Labels overlap", False),
        ):
            snapshot = Snapshot.model_validate({
                "conversation_hash": "synthetic",
                "messages": [{"id": "m1", "source": "thread", "role": role,
                              "direction": direction, "visibility": visibility, "status": status,
                              "body": "Feedback: Labels overlap\nDevice: synthetic"}],
            })
            result = json.dumps({"device_info": [], "customer_feedback": [
                {"text": text, "message_ids": ["m1"]}], "support_responses": []})
            with self.subTest(role=role, text=text, direction=direction, visibility=visibility, status=status):
                if accepted:
                    self.assertEqual(validate_summary(result, snapshot).support_responses, [])
                else:
                    with self.assertRaises(ValidationError):
                        validate_summary(result, snapshot)

    def test_request_failures_have_safe_distinct_codes(self):
        payload = json.dumps({
            "input": {"conversation_hash": "synthetic",
                      "messages": [{"id": "m1", "role": "customer", "body": "Help"}]},
            "config": {"api_base": "https://model.invalid", "api_key": "synthetic-private-key", "model": "deepseek:synthetic"},
            "schema_version": "bub-summary-v6",
        }).encode()
        original = main.republic.get_model
        for error, expected in (
            (httpx2.ConnectError("synthetic-private-proxy"), "model_connection_failed"),
            (httpx2.ConnectTimeout("synthetic-private-proxy"), "model_timeout"),
            (httpx2.Response(503, text="synthetic-private-provider-body"), "model_http_error"),
            (httpx2.Response(429, text="synthetic-private-provider-body", headers={"x-request-id": "req-safe-123"}), "model_rate_limited"),
            (httpx2.Response(200, headers={"content-type": "text/event-stream"}, text="data: broken-json\n\n"), "invalid_summary"),
        ):
            with self.subTest(expected=expected):
                calls = []

                def respond(request):
                    calls.append(request)
                    if isinstance(error, Exception):
                        raise error
                    return error

                client = httpx2.AsyncClient(transport=httpx2.MockTransport(respond), trust_env=False)

                def configured(*args, **kwargs):
                    return original(*args, **kwargs, http_client=client)

                output, logs = io.StringIO(), io.StringIO()
                try:
                    with (
                        patch.object(main.republic, "get_model", side_effect=configured),
                        patch.object(main.sys, "stdin", types.SimpleNamespace(buffer=io.BytesIO(payload))),
                        patch.object(main.sys, "stdout", output),
                        patch.object(main.sys, "stderr", logs),
                    ):
                        status = main.main()
                finally:
                    asyncio.run(client.aclose())
                self.assertEqual(status, 1)
                self.assertEqual(len(calls), 1)
                result = json.loads(output.getvalue())
                self.assertEqual(result["error"], expected)
                self.assertFalse(result["ok"])
                if isinstance(error, httpx2.Response) and error.status_code != 200:
                    self.assertEqual(result["http_status"], error.status_code)
                if expected == "model_rate_limited":
                    self.assertEqual(result["request_id"], "req-safe-123")
                self.assertNotIn("synthetic-private", output.getvalue() + logs.getvalue())

    def test_upstream_response_errors_never_expose_body_or_headers(self):
        error = main.errors.APIResponseError("synthetic-private-body", headers={"x-request-id": "req-safe", "authorization": "synthetic-private-key"})
        self.assertEqual(main.upstream_failure(error), {"ok": False, "error": "model_response_invalid", "request_id": "req-safe"})
        error = main.errors.APIStatusError(429, "synthetic-private-body", headers={"x-request-id": "unsafe/private"})
        self.assertEqual(main.upstream_failure(error), {"ok": False, "error": "model_rate_limited", "http_status": 429})

    def test_optional_sections_keep_role_evidence(self):
        snapshot = Snapshot.model_validate({
            "conversation_hash": "synthetic-conversation",
            "product": "Printer P1", "description": "型号：P1；客户无法打印。",
            "messages": [
                {"id": "m1", "role": "agent", "body": "请尝试升级固件。", "visibility": "public"},
                {"id": "m2", "role": "customer", "body": "尚未升级，仍然无法打印。"},
                {"id": "m3", "role": "agent", "body": "内部计划退款。", "visibility": "private"},
                {"id": "m4", "role": "agent", "body": "升级建议草稿。", "status": "DRAFT"},
            ],
        })
        summary = {
            "device_info": [{"text": "Printer P1", "message_ids": []}],
            "customer_feedback": [{"text": "客户尚未升级，仍然无法打印。", "message_ids": ["m2"]}],
            "support_responses": [{"text": "客服建议升级固件；客户尚未确认执行。", "message_ids": ["m1"]}],
        }
        parsed = validate_summary(json.dumps(summary), snapshot)
        self.assertEqual(parsed.device_info[0].text, "Printer P1")
        self.assertEqual(parsed.support_responses[0].message_ids, ["m1"])
        empty = {"device_info": [], "customer_feedback": [], "support_responses": []}
        self.assertEqual(validate_summary(json.dumps(empty), snapshot).model_dump(), empty)
        no_messages = snapshot.model_copy(update={"messages": []})
        self.assertEqual(validate_summary(json.dumps(empty), no_messages).model_dump(), empty)
        description_device = {**empty, "device_info": [{"text": "型号：P1", "message_ids": []}]}
        self.assertEqual(validate_summary(json.dumps(description_device), no_messages).device_info[0].text, "型号：P1")
        for field, refs, expected in (
            ("customer_feedback", ["missing"], "unknown_reference"),
            ("customer_feedback", ["m1"], "missing_customer_evidence"),
            ("support_responses", ["m2"], "missing_support_evidence"),
            ("support_responses", ["m3"], "missing_support_evidence"),
            ("support_responses", ["m4"], "missing_support_evidence"),
            ("customer_feedback", [], "missing_reference"),
        ):
            invalid = copy.deepcopy(summary)
            invalid[field][0]["message_ids"] = refs
            with self.subTest(field=field, refs=refs), self.assertRaises(ValidationError) as caught:
                validate_summary(json.dumps(invalid), snapshot)
            self.assertEqual(validation_diagnostics(caught.exception)[0]["code"], expected)
            self.assertEqual(validation_diagnostics(caught.exception)[0]["loc"], [field, 0, "message_ids"])
        for section in ("customer_feedback", "support_responses"):
            fragmented = copy.deepcopy(summary)
            fragmented[section] *= 2
            with self.subTest(fragmented=section), self.assertRaises(ValidationError):
                validate_summary(json.dumps(fragmented), snapshot)
        unsupported = {**empty, "device_info": [{"text": "invented printer", "message_ids": []}]}
        self.assertEqual(validate_summary(json.dumps(unsupported), snapshot).model_dump(), empty)
        invalid = {**summary, "synthetic-private-field": "synthetic-private-content"}
        with self.assertRaises(ValidationError) as caught:
            validate_summary(json.dumps(invalid), snapshot)
        diagnostics = validation_diagnostics(caught.exception)
        self.assertEqual(diagnostics[0]["loc"], ["unknown_field"])
        self.assertNotIn("synthetic-private", json.dumps(diagnostics))

    def test_failed_completion_retains_provider_metadata(self):
        invalid_reference = {
            "device_info": [],
            "customer_feedback": [{"text": "synthetic-private-claim", "message_ids": ["missing"]}],
            "support_responses": [],
        }
        cases = (
            ("stop", json.dumps({"synthetic-private-field": "synthetic-private-body"}), "schema_validation"),
            ("stop", json.dumps(invalid_reference), "unknown_reference"),
            ("length", "synthetic-private-partial-output", "output_truncated"),
        )

        async def verify():
            original = main.republic.get_model
            for reason, content, expected_code in cases:
                calls = []

                def respond(request):
                    calls.append(request)
                    chunk = {
                        "id": "synthetic", "model": "synthetic-returned-version",
                        "choices": [{"index": 0, "delta": {"content": content}, "finish_reason": reason}],
                        "usage": {"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19},
                    }
                    return httpx2.Response(
                        200,
                        headers={"content-type": "text/event-stream", "x-request-id": "synthetic-request"},
                        text="data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n",
                    )

                async with httpx2.AsyncClient(transport=httpx2.MockTransport(respond), trust_env=False) as client:
                    def configured(*args, **kwargs):
                        return original(*args, **kwargs, http_client=client)

                    request = main.Request.model_validate({
                        "input": {
                            "conversation_hash": "synthetic-conversation",
                            "messages": [{"id": "m1", "role": "customer", "body": "Help"}],
                        },
                        "config": {"api_base": "https://api.deepseek.com", "api_key": "synthetic-private-key", "model": "deepseek:synthetic"},
                        "schema_version": "bub-summary-v6",
                    })
                    with patch.object(main.republic, "get_model", side_effect=configured):
                        result = await main.summarize(request)
                with self.subTest(code=expected_code):
                    self.assertEqual(len(calls), 1)
                    self.assertFalse(result["ok"])
                    self.assertNotIn("result", result)
                    self.assertEqual(result["returned_model"], "synthetic-returned-version")
                    self.assertEqual(result["request_id"], "synthetic-request")
                    self.assertEqual(result["usage"]["total_tokens"], 19)
                    self.assertIn(expected_code, [item["code"] for item in result["diagnostics"]])
                    self.assertNotIn("synthetic-private", json.dumps(result))

        asyncio.run(verify())

    def test_complete_conversation_and_message_evidence(self):
        snapshot = {
            "conversation_hash": "synthetic-conversation", "subject": "无法打印",
            "description": "设备无法打印；以下保留完整往来。", "product": "synthetic-printer",
            "complete": True, "warnings": [],
            "messages": [
                {"id": "m1", "role": "agent", "body": "请尝试升级固件。", "visibility": "public"},
                {"id": "m2", "role": "customer", "body": "尚未升级；仍然无法打印。", "visibility": "public"},
                {"id": "m3", "role": "agent", "body": "请升级后再试，若仍失败请提供视频。", "visibility": "public"},
                {"id": "m4", "role": "customer", "body": "现在已经升级，仍无法打印；还没有录视频。", "visibility": "public"},
            ],
        }
        summary = {
            "device_info": [],
            "customer_feedback": [{"text": "客户先前尚未升级，随后确认升级仍无法打印，尚未提供视频。", "message_ids": ["m2", "m4"]}],
            "support_responses": [{"text": "客服建议升级后重试，并在失败时提供视频。", "message_ids": ["m1", "m3"]}],
        }

        async def verify():
            original = main.republic.get_model
            for name, cited_id in (("valid evidence", "m2"), ("unknown evidence", "42")):
                sent = []
                output = copy.deepcopy(summary)
                output["device_info"] = [{"text": "Device unspecified", "message_ids": []}]
                output["customer_feedback"][0]["message_ids"] = [cited_id, "m4"]

                def respond(request):
                    sent.append(json.loads(request.content))
                    chunk = {
                        "id": "synthetic", "model": "synthetic-returned-version",
                        "choices": [{"index": 0, "delta": {"content": json.dumps(output)}, "finish_reason": "stop"}],
                    }
                    return httpx2.Response(
                        200, headers={"content-type": "text/event-stream"},
                        text="data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n",
                    )

                source = snapshot
                request = main.Request.model_validate({
                    "input": source,
                    "config": {"api_base": "https://api.deepseek.com", "api_key": "synthetic-key", "model": "deepseek:synthetic"},
                    "schema_version": "bub-summary-v6",
                })
                async with httpx2.AsyncClient(transport=httpx2.MockTransport(respond), trust_env=False) as client:
                    def configured(*args, **kwargs):
                        return original(*args, **kwargs, http_client=client)

                    with patch.object(main.republic, "get_model", side_effect=configured):
                        result = await main.summarize(request)
                with self.subTest(case=name):
                    self.assertEqual(len(sent), 1)
                    self.assertFalse(sent[0].get("tools"))
                    messages = sent[0]["messages"]
                    system_inputs = [message["content"] for message in messages if message["role"] == "system"]
                    self.assertEqual(system_inputs, [main.SYSTEM_PROMPT])
                    user_inputs = [json.loads(message["content"]) for message in messages if message["role"] == "user"]
                    self.assertEqual(user_inputs, [source])
                    self.assertEqual(result["ok"], cited_id == "m2")
                    if cited_id == "m2":
                        self.assertEqual(result["result"]["customer_feedback"][0]["message_ids"], ["m2", "m4"])
                        self.assertEqual(len(result["result"]["customer_feedback"]), 1)
                        self.assertEqual(result["result"]["support_responses"][0]["message_ids"], ["m1", "m3"])
                    else:
                        self.assertEqual(result["diagnostics"][0]["code"], "unknown_reference")

        asyncio.run(verify())

        with self.assertRaises(ValidationError):
            Snapshot.model_validate({key: value for key, value in snapshot.items() if key != "conversation_hash"})


if __name__ == "__main__":
    unittest.main()

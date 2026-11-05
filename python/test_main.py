# input: 合成完整会话、结构化模型结果和 SDK 模拟网络错误。
# output: v6 整单汇总、单封来信/逐字摘录及可空分类、来源引用、完整会话及安全网络诊断的合成证据。
# pos: Python 总结契约的最小验证。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import asyncio
import copy
import io
import json
import types
import unittest
from unittest.mock import patch

import httpx2
import main
from pydantic import ValidationError

from main import Snapshot, validate_summary, validation_diagnostics


class SummaryContractTest(unittest.TestCase):
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
            (httpx2.Response(503, text="synthetic-private-provider-body"), "model_upstream_failed"),
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
                self.assertEqual(json.loads(output.getvalue()), {"ok": False, "error": expected})
                self.assertNotIn("synthetic-private", output.getvalue() + logs.getvalue())

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
        for section in ("customer_feedback", "support_responses"):
            fragmented = copy.deepcopy(summary)
            fragmented[section] *= 2
            with self.subTest(fragmented=section), self.assertRaises(ValidationError):
                validate_summary(json.dumps(fragmented), snapshot)
        invalid = {**empty, "device_info": [{"text": "invented printer", "message_ids": []}]}
        with self.assertRaises(ValidationError):
            validate_summary(json.dumps(invalid), snapshot)
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

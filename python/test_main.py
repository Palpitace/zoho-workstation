# input: 合成会话、可选 Jev 参考及结构化模型结果。
# output: 完整会话传递、参考隔离、状态与安全诊断的 SDK Mock 证据，不调用真实模型。
# pos: Python 总结契约的最小验证。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import asyncio
import copy
import json
import unittest
from unittest.mock import patch

import httpx2
import main
from pydantic import ValidationError

from main import Snapshot, validate_summary, validation_diagnostics


class SummaryContractTest(unittest.TestCase):
    def test_suggestion_execution_and_references(self):
        snapshot = Snapshot.model_validate({
            "conversation_hash": "synthetic-conversation",
            "jev_reference": None,
            "messages": [
                {"id": "m1", "role": "agent", "body": "请尝试升级固件。"},
                {"id": "m2", "role": "customer", "body": "已经升级，仍然无法打印。"},
            ]
        })
        summary = {
            "current_situation": {"text": "客户升级后仍无法打印。", "message_ids": ["m2"]},
            "key_progress": [],
            "attempted_measures": [{
                "action": "升级固件",
                "execution_status": "suggested",
                "result": "此客服消息只提出建议，未报告结果。",
                "result_status": "unknown",
                "message_ids": ["m1"],
            }],
            "blockers": [],
            "next_steps": [{
                "text": "请客户提供仍无法打印时的错误提示。",
                "basis": "recommendation",
                "message_ids": ["m2"],
            }],
        }
        suggested = validate_summary(json.dumps(summary), snapshot)
        self.assertEqual(suggested.attempted_measures[0].execution_status, "suggested")
        confirmed = copy.deepcopy(summary)
        confirmed["attempted_measures"][0].update({
            "execution_status": "customer_confirmed_executed",
            "result_status": "customer_reported",
            "result": "客户报告升级后仍然无法打印。",
            "message_ids": ["m2"],
        })
        parsed = validate_summary(json.dumps(confirmed), snapshot)
        self.assertEqual(parsed.attempted_measures[0].result_status, "customer_reported")

        invalid_reference = copy.deepcopy(summary)
        invalid_reference["current_situation"]["message_ids"] = ["missing"]
        invalid_execution = copy.deepcopy(summary)
        invalid_execution["attempted_measures"][0]["execution_status"] = "customer_confirmed_executed"
        invalid_result = copy.deepcopy(summary)
        invalid_result["attempted_measures"][0]["result_status"] = "customer_reported"
        invalid_extra = copy.deepcopy(summary)
        invalid_extra["synthetic-private-field"] = "synthetic-private-content"
        for name, value, expected_code in (
            ("missing reference", invalid_reference, "unknown_reference"),
            ("agent suggestion is not execution", invalid_execution, "missing_customer_evidence"),
            ("suggestion has no reported result", invalid_result, "reported_result_without_execution"),
            ("arbitrary extra key is not diagnostic output", invalid_extra, "schema_validation"),
        ):
            with self.subTest(name=name), self.assertRaises(ValidationError) as caught:
                validate_summary(json.dumps(value), snapshot)
            diagnostics = validation_diagnostics(caught.exception)
            self.assertEqual(diagnostics[0]["code"], expected_code)
            encoded = json.dumps(diagnostics)
            self.assertNotIn("synthetic-private", encoded)
            self.assertTrue(all(set(item) == {"loc", "type", "code"} for item in diagnostics))
            if value is invalid_extra:
                self.assertEqual(diagnostics[0]["loc"], ["unknown_field"])

    def test_failed_completion_retains_provider_metadata(self):
        invalid_reference = {
            "current_situation": {"text": "synthetic-private-claim", "message_ids": ["missing"]},
            "key_progress": [], "attempted_measures": [], "blockers": [],
            "next_steps": [{"text": "Ask for details", "basis": "recommendation", "message_ids": ["m1"]}],
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
                            "conversation_hash": "synthetic-conversation", "jev_reference": None,
                            "messages": [{"id": "m1", "role": "customer", "body": "Help"}],
                        },
                        "config": {"api_base": "https://api.deepseek.com", "api_key": "synthetic-private-key", "model": "deepseek:synthetic"},
                        "schema_version": "bub-summary-v2",
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

    def test_optional_reference_keeps_complete_conversation_and_message_evidence(self):
        reference = {
            "kind": "model_assessment", "run_id": 42,
            "conversation_hash": "synthetic-conversation",
            "schema_version": "jev-v1", "requested_model": "synthetic-jev",
            "returned_model": "synthetic-jev-version", "choices": {"priority": "high"},
        }
        snapshot = {
            "conversation_hash": "synthetic-conversation", "subject": "无法打印",
            "description": "设备无法打印；以下保留完整往来。", "product": "synthetic-printer",
            "complete": True, "warnings": [],
            "messages": [
                {"id": "m1", "role": "agent", "body": "请尝试升级固件。", "visibility": "public"},
                {"id": "m2", "role": "customer", "body": "尚未升级；仍然无法打印。", "visibility": "public"},
            ],
        }
        summary = {
            "current_situation": {"text": "客户尚未升级，仍然无法打印。", "message_ids": ["m2"]},
            "key_progress": [], "attempted_measures": [], "blockers": [],
            "next_steps": [{"text": "请确认升级条件。", "basis": "recommendation", "message_ids": ["m1", "m2"]}],
        }

        async def verify():
            original = main.republic.get_model
            for name, optional_reference, cited_id in (
                ("without reference", None, "m2"),
                ("with reference", reference, "m2"),
                ("reference run is not message evidence", reference, "42"),
            ):
                sent = []
                output = copy.deepcopy(summary)
                output["current_situation"]["message_ids"] = [cited_id]

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

                source = {**snapshot, "jev_reference": optional_reference}
                request = main.Request.model_validate({
                    "input": source,
                    "config": {"api_base": "https://api.deepseek.com", "api_key": "synthetic-key", "model": "deepseek:synthetic"},
                    "schema_version": "bub-summary-v2",
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
                    user_inputs = [json.loads(message["content"]) for message in messages if message["role"] == "user"]
                    self.assertEqual(user_inputs, [source])
                    self.assertEqual(result["ok"], cited_id == "m2")
                    if cited_id == "m2":
                        self.assertEqual(result["result"]["current_situation"]["message_ids"], ["m2"])
                    else:
                        self.assertEqual(result["diagnostics"][0]["code"], "unknown_reference")

        asyncio.run(verify())

        valid_source = {**snapshot, "jev_reference": reference}
        cases = (
            ("missing explicit reference", snapshot),
            ("missing conversation version", {key: value for key, value in valid_source.items() if key != "conversation_hash"}),
            ("different conversation", {**snapshot, "jev_reference": {**reference, "conversation_hash": "older-conversation"}}),
            ("free text is not a reference field", {**snapshot, "jev_reference": {**reference, "synthetic-private-field": "synthetic-private-text"}}),
            ("probabilities are not choices", {**snapshot, "jev_reference": {**reference, "choices": {"priority": {"high": 0.9}}}}),
        )
        for name, invalid_source in cases:
            with self.subTest(case=name):
                with self.assertRaises(ValidationError) as caught:
                    Snapshot.model_validate(invalid_source)
                diagnostics = validation_diagnostics(caught.exception)
                self.assertNotIn("synthetic-private", json.dumps(diagnostics))
                if name == "different conversation":
                    self.assertEqual(diagnostics[0]["code"], "reference_version_mismatch")


if __name__ == "__main__":
    unittest.main()

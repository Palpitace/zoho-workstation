# input: 合成历史节点、固定知识片段和 SDK MockTransport。
# output: 独立无工具调用、引用边界、政策只读及失败计数元信息的验证。
# pos: 离线 FAQ 模型入口的契约测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import asyncio
import copy
import json
import unittest
from unittest.mock import patch

import httpx2
from pydantic import ValidationError

import lab


def source():
    return lab.Input.model_validate({
        "ticket": {"conversation_hash": "synthetic", "messages": [
            {"id": "c1", "role": "customer", "body": "P1 cannot print", "visibility": "public", "status": "SUCCESS"},
        ]},
        "sources": [{"id": "knowledge:1:p1", "entry_id": 1, "block_id": "p1", "title": "P1 help", "kind": "sop",
                     "scope": "technical", "product": "P1", "text": "Confirm the media size"}],
        "feedback": [],
    })


def reply():
    return {"text": "Could you confirm the media size?", "language": "en", "rationale": "缺少介质尺寸，先核对前提。",
            "missing_information": ["介质尺寸"], "source_ids": ["knowledge:1:p1"], "message_ids": ["c1"]}


def evaluation():
    return {"verdict": "pass", "score": 4, "findings": [], "suggestions": []}


def proposal():
    return {"items": [{"title": "P1 打印问题", "product": "P1", "question": "不能打印时应先核对什么？",
                       "answer": "先确认介质尺寸。", "conditions": "仅适用于 P1，效果未确认。", "reason": "补充适用前提。",
                       "scope": "technical", "replaces_id": 1, "message_ids": ["c1"], "source_ids": ["knowledge:1:p1"]}]}


class LabContractTest(unittest.TestCase):
    def test_output_references_are_local_and_policy_is_read_only(self):
        context = {"input": source()}
        lab.Reply.model_validate(reply(), context=context)
        lab.Evaluation.model_validate(evaluation(), context=context)
        lab.Proposals.model_validate(proposal(), context=context)
        for field, value in (("message_ids", ["future"]), ("source_ids", ["not-selected"]), ("message_ids", [])):
            candidate = {**reply(), field: value}
            with self.subTest(field=field, value=value), self.assertRaises(ValidationError):
                lab.Reply.model_validate(candidate, context=context)
        for scope in ("policy", "reference"):
            original = source()
            original.sources[0].scope = scope
            with self.subTest(scope=scope), self.assertRaises(ValidationError):
                lab.Proposals.model_validate(proposal(), context={"input": original})
        invalid = proposal()
        invalid["items"][0]["scope"] = "policy"
        with self.assertRaises(ValidationError):
            lab.Proposals.model_validate(invalid, context=context)
        lab.Proposals.model_validate({"items": []}, context=context)
        oversized = proposal()
        oversized["items"][0].update(answer="排" * 4000, conditions="条" * 1000)
        with self.assertRaises(ValidationError):
            lab.Proposals.model_validate(oversized, context=context)

    def test_evaluation_cannot_pass_with_a_blocker(self):
        value = {"verdict": "pass", "score": 5, "findings": [{"code": "policy", "severity": "blocker",
                 "reason": "无授权承诺", "message_ids": [], "source_ids": []}], "suggestions": []}
        with self.assertRaises(ValidationError):
            lab.Evaluation.model_validate(value, context={"input": source()})

    def test_human_feedback_keeps_its_batch_and_evaluator_is_independent(self):
        value = source().model_dump()
        value["reply"] = reply()
        value["feedback"] = [{"id": 1, "batch_id": 7, "call_id": 3, "node_id": "t:c1", "score": 2,
                              "tags": ["遗漏诉求"], "note": "请先确认尺寸", "edited_reply": "", "created_at": "2026-11-05T00:00:00Z",
                              "rated_reply": {**reply(), "source_ids": ["old-version-only"]}}]
        args = {"task": "reply", "input": value, "schema_version": "bub-lab-v1",
                "config": {"api_base": "https://api.deepseek.com", "api_key": "synthetic-key", "model": "deepseek:synthetic"}}
        parsed = lab.Request.model_validate(args)
        self.assertEqual(parsed.input.feedback[0].batch_id, 7)
        self.assertEqual(parsed.input.feedback[0].rated_reply.source_ids, ["old-version-only"])
        # A historical scored draft is allowed, but its obsolete references
        # cannot be used as new output evidence.
        with self.assertRaises(ValidationError):
            lab.Reply.model_validate(parsed.input.feedback[0].rated_reply.model_dump(), context={"input": parsed.input})
        with self.assertRaises(ValidationError):
            lab.Request.model_validate({**args, "task": "evaluate"})

    def test_sdk_uses_one_separate_request_and_preserves_failure_metadata(self):
        async def run():
            original = lab.republic.get_model
            for task, output in (("reply", reply()), ("evaluate", evaluation()), ("propose", proposal())):
                calls = []
                input_value = source().model_dump()
                if task == "evaluate":
                    input_value["reply"] = reply()
                request = lab.Request.model_validate({"task": task, "input": input_value, "schema_version": "bub-lab-v1",
                    "config": {"api_base": "https://api.deepseek.com", "api_key": "synthetic-key", "model": "deepseek:synthetic"}})

                def respond(http_request):
                    calls.append(json.loads(http_request.content))
                    chunk = {"id": "synthetic", "model": "returned-version", "choices": [
                        {"index": 0, "delta": {"content": json.dumps(output)}, "finish_reason": "stop"}]}
                    return httpx2.Response(200, headers={"content-type": "text/event-stream"},
                        text="data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n")

                async with httpx2.AsyncClient(transport=httpx2.MockTransport(respond), trust_env=False) as client:
                    def configured(*args, **kwargs):
                        return original(*args, **kwargs, http_client=client)
                    with patch.object(lab.republic, "get_model", side_effect=configured):
                        result = await lab.generate(request)
                with self.subTest(task=task):
                    self.assertEqual(len(calls), 1)
                    self.assertFalse(calls[0].get("tools"))
                    self.assertEqual(result["returned_model"], "returned-version")
                    self.assertEqual(result["result"], output)
                    users = [json.loads(m["content"]) for m in calls[0]["messages"] if m["role"] == "user"]
                    self.assertEqual(users, [request.input.model_dump(exclude_unset=True)])
                    self.assertNotIn("synthetic-key", json.dumps(users))
                bad = copy.deepcopy(output)
                if task == "reply":
                    bad["message_ids"] = ["future"]
                    output = bad
                    async with httpx2.AsyncClient(transport=httpx2.MockTransport(respond), trust_env=False) as client:
                        with patch.object(lab.republic, "get_model", side_effect=configured):
                            result = await lab.generate(request)
                    self.assertEqual(result["error"], "invalid_lab_result")
                    self.assertEqual(result["returned_model"], "returned-version")
                    self.assertIn("usage", result)
                    self.assertNotIn("result", result)
        asyncio.run(run())


if __name__ == "__main__":
    unittest.main()

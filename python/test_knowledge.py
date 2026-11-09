# input: 合成往来、知识来源、Word/PDF/Markdown 文档及 SDK MockTransport。
# output: 案例执行/结果证据、建议引用、文档顺序/不完整及单次无工具模型调用的验证。
# pos: 知识入口和本地文档提取的行为测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import asyncio
import base64
import copy
import io
import json
import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

from docx import Document
import httpx2
from pydantic import ValidationError
from pypdf import PdfWriter
from pypdf.generic import DecodedStreamObject, DictionaryObject, NameObject

import documents
import knowledge


def context():
    return knowledge.Input.model_validate({
        "ticket": {"conversation_hash": "synthetic", "product": "P1", "messages": [
            {"id": "c1", "role": "customer", "body": "Cannot print", "visibility": "public", "status": "SUCCESS"},
            {"id": "a1", "role": "agent", "body": "Please calibrate", "visibility": "public", "status": "SUCCESS"},
            {"id": "c2", "role": "customer", "body": "Calibrated, still cannot print", "visibility": "public", "status": "SUCCESS"},
        ]},
        "summary": {"device_info": [], "customer_feedback": [], "support_responses": []},
        "sources": [{"id": "knowledge:1:page-1", "entry_id": 1, "block_id": "page-1",
                     "title": "P1 troubleshooting", "kind": "sop", "product": "P1", "text": "Check media size"}],
    })


def case_output():
    return {"title": "Printing problem", "product": "P1", "issue": "Printing failed after calibration",
            "steps": [{"action": "Support suggested calibration", "execution": "confirmed",
                       "outcome": "unresolved", "message_ids": ["a1", "c2"]}], "message_ids": ["c1", "c2"]}


def advice_output():
    return {"steps": [{"action": "Check media size", "reason": "Calibration already failed; follow applicable SOP",
                       "source_ids": ["knowledge:1:page-1"], "current_message_ids": ["c2"]}],
            "missing_information": [], "conflicts": []}


class KnowledgeContractTest(unittest.TestCase):
    def test_case_distinguishes_proposal_execution_and_outcome(self):
        source = context()
        value = case_output()
        self.assertEqual(knowledge.Case.model_validate(value, context={"input": source}).steps[0].outcome, "unresolved")
        with self.assertRaises(ValidationError):
            knowledge.Case.model_validate({**value, "product": "invented-model"}, context={"input": source})
        for execution, outcome, refs, accepted in (
            ("unknown", "unknown", ["a1"], True),
            ("confirmed", "unknown", ["a1"], False),
            ("unknown", "resolved", ["a1", "c2"], False),
            ("confirmed", "resolved", ["a1", "c2"], True),
            ("unknown", "unknown", ["missing"], False),
            ("unknown", "unknown", ["c1"], False),
        ):
            with self.subTest(execution=execution, outcome=outcome, refs=refs):
                candidate = copy.deepcopy(value)
                candidate["steps"][0].update(execution=execution, outcome=outcome, message_ids=refs)
                if accepted:
                    knowledge.Case.model_validate(candidate, context={"input": source})
                else:
                    with self.assertRaises(ValidationError):
                        knowledge.Case.model_validate(candidate, context={"input": source})
        for change in ({"role": "unknown"}, {"mixed_authors": True}, {"visibility": "private"}, {"status": "DRAFT"}):
            modified = source.model_copy(deep=True)
            modified.ticket.messages[2] = modified.ticket.messages[2].model_copy(update=change)
            with self.subTest(change=change), self.assertRaises(ValidationError):
                knowledge.Case.model_validate(value, context={"input": modified})
        customer_only = source.model_copy(deep=True)
        customer_only.ticket.messages = customer_only.ticket.messages[:1]
        value.update(steps=[], message_ids=["c1"], issue="Customer reports printing issue")
        self.assertEqual(knowledge.Case.model_validate(value, context={"input": customer_only}).steps, [])

    def test_advice_requires_selected_knowledge_and_current_ticket_references(self):
        source = context()
        value = advice_output()
        knowledge.Advice.model_validate(value, context={"input": source})
        for field, refs in (("source_ids", []), ("source_ids", ["fabricated"]), ("current_message_ids", ["other-ticket-message"])):
            candidate = copy.deepcopy(value)
            candidate["steps"][0][field] = refs
            with self.subTest(field=field, refs=refs), self.assertRaises(ValidationError):
                knowledge.Advice.model_validate(candidate, context={"input": source})
        source.sources = []
        empty = {"steps": [], "missing_information": ["No applicable knowledge available"], "conflicts": []}
        self.assertEqual(knowledge.Advice.model_validate(empty, context={"input": source}).steps, [])
        with self.assertRaises(ValidationError):
            knowledge.Advice.model_validate(value, context={"input": source})

    def test_real_sdk_uses_one_toolless_request_and_preserves_provenance(self):
        async def run():
            original = knowledge.republic.get_model
            for task, output in (("case", case_output()), ("advice", advice_output())):
                calls = []
                source = context()
                request = knowledge.Request.model_validate({
                    "task": task, "input": source.model_dump(), "schema_version": f"bub-{task}-v1",
                    "config": {"api_base": "https://api.deepseek.com", "api_key": "synthetic-key", "model": "deepseek:synthetic"},
                })

                def respond(http_request):
                    calls.append(json.loads(http_request.content))
                    chunk = {"id": "synthetic", "model": "returned-version", "choices": [
                        {"index": 0, "delta": {"content": json.dumps(output)}, "finish_reason": "stop"}]}
                    return httpx2.Response(200, headers={"content-type": "text/event-stream"},
                                           text="data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n")

                async with httpx2.AsyncClient(transport=httpx2.MockTransport(respond), trust_env=False) as client:
                    def configured(*args, **kwargs):
                        return original(*args, **kwargs, http_client=client)
                    with patch.object(knowledge.republic, "get_model", side_effect=configured):
                        result = await knowledge.generate(request)
                with self.subTest(task=task):
                    self.assertEqual(len(calls), 1)
                    self.assertFalse(calls[0].get("tools"))
                    self.assertEqual(result["returned_model"], "returned-version")
                    self.assertEqual(result["result"], output)
                    actual = [json.loads(message["content"]) for message in calls[0]["messages"] if message["role"] == "user"]
                    self.assertEqual(actual, [request.input.model_dump(exclude_unset=True)])
                    self.assertNotIn("synthetic-key", json.dumps(actual))
        asyncio.run(run())


class DocumentExtractionTest(unittest.TestCase):
    def extract(self, filename, content):
        return documents.extract({"filename": filename, "content_base64": base64.b64encode(content).decode()})

    def test_markdown_headings_and_long_multibyte_text_keep_positions(self):
        result = self.extract("SOP.md", "# 机型 P1\n不要重置。\n## 步骤\n检查连接。".encode())
        self.assertEqual(result["blocks"], [{"id": "line-1", "text": "# 机型 P1\n不要重置。"},
                                           {"id": "line-3", "text": "## 步骤\n检查连接。"}])
        self.assertFalse(result["incomplete"])
        long = "不要重置。" * 1000
        pieces = self.extract("long.md", long.encode())["blocks"]
        self.assertGreater(len(pieces), 1)
        self.assertEqual("".join(piece["text"] for piece in pieces), long)
        self.assertTrue(all(len(piece["text"].encode()) <= documents.MAX_BLOCK_BYTES for piece in pieces))

    def test_docx_preserves_paragraph_table_order(self):
        document = Document()
        document.add_paragraph("P1 only")
        row = document.add_table(rows=1, cols=2).rows[0]
        row.cells[0].text, row.cells[1].text = "Action", "Do not reset"
        document.add_paragraph("Stop if already failed")
        file = io.BytesIO()
        document.save(file)
        result = self.extract("test.docx", file.getvalue())
        self.assertFalse(result["incomplete"])
        self.assertEqual(result["blocks"], [
            {"id": "paragraph-1", "text": "P1 only"},
            {"id": "table-2", "text": "Action | Do not reset"},
            {"id": "paragraph-3", "text": "Stop if already failed"},
        ])

    def test_pdf_keeps_pages_and_rejects_scan_only(self):
        writer = PdfWriter()
        page = writer.add_blank_page(width=612, height=792)
        font = DictionaryObject({NameObject("/Type"): NameObject("/Font"), NameObject("/Subtype"): NameObject("/Type1"),
                                 NameObject("/BaseFont"): NameObject("/Helvetica")})
        page[NameObject("/Resources")] = DictionaryObject({NameObject("/Font"): DictionaryObject({NameObject("/F1"): font})})
        stream = DecodedStreamObject()
        stream.set_data(b"BT /F1 12 Tf 10 700 Td (Do not reset P1) Tj ET")
        page[NameObject("/Contents")] = stream
        writer.add_blank_page(width=612, height=792)
        file = io.BytesIO()
        writer.write(file)
        result = self.extract("manual.pdf", file.getvalue())
        self.assertEqual(result["blocks"], [{"id": "page-1", "text": "Do not reset P1"}])
        self.assertTrue(result["incomplete"])
        self.assertEqual(result["warnings"], ["empty_pdf_pages"])
        blank = PdfWriter()
        blank.add_blank_page(width=612, height=792)
        file = io.BytesIO()
        blank.write(file)
        with self.assertRaisesRegex(documents.DocumentError, "no_extractable_text"):
            self.extract("scan.pdf", file.getvalue())

    def test_unsupported_empty_invalid_and_oversized_documents_are_explicit(self):
        for filename, content, expected in (
            ("old.doc", b"old Word", "unsupported_document"),
            ("../private.md", b"private", "invalid_filename"),
            ("empty.md", b"", "empty_document"),
            ("invalid.md", b"\xff", "invalid_text_encoding"),
            ("large.md", b"a" * (documents.MAX_TEXT_BYTES + 1), "document_too_large"),
        ):
            with self.subTest(filename=filename), self.assertRaisesRegex(documents.DocumentError, expected):
                self.extract(filename, content)
        result = subprocess.run([sys.executable, str(Path(documents.__file__))],
                                input=json.dumps({"filename": "private.pdf", "content_base64": base64.b64encode(b"synthetic-private-body").decode()}),
                                text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(result.stdout), {"ok": False, "error": "invalid_document"})
        self.assertNotIn("synthetic-private", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

# input: stdin 中文件名及 Base64 文档内容，不接受文件系统路径。
# output: 保留段落或页码锚点的文本块、明确的不完整标记及安全失败码。
# pos: Word/PDF/Markdown 的离线有界提取入口。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
from __future__ import annotations

import base64
import binascii
import io
import json
import logging
from pathlib import PurePath
import sys
import warnings
from zipfile import BadZipFile, ZipFile

from docx import Document
from docx.table import Table
from pypdf import PdfReader

MAX_FILE_BYTES = 10 * 1024 * 1024
MAX_TEXT_BYTES = 256 * 1024
MAX_BLOCK_BYTES = 8 * 1024
MAX_BLOCKS = 4096
MAX_STDIN_BYTES = (MAX_FILE_BYTES + 2) // 3 * 4 + 4096


class DocumentError(ValueError):
    pass


def split_block(identifier: str, text: str) -> list[dict[str, str]]:
    text = text.strip()
    if not text:
        return []
    # UTF-8 boundaries are preserved. Suffixes describe parts, not invented pages.
    parts, current, size = [], [], 0
    for character in text:
        width = len(character.encode("utf-8"))
        if size + width > MAX_BLOCK_BYTES:
            parts.append("".join(current))
            current, size = [], 0
        current.append(character)
        size += width
    if current:
        parts.append("".join(current))
    return [
        {"id": identifier if len(parts) == 1 else f"{identifier}-part-{index}", "text": part}
        for index, part in enumerate(parts, 1)
    ]


def extract_text(content: bytes) -> tuple[list[tuple[str, str]], list[str]]:
    try:
        text = content.decode("utf-8-sig")
    except UnicodeDecodeError as error:
        raise DocumentError("invalid_text_encoding") from error
    if "\x00" in text:
        raise DocumentError("invalid_text_encoding")
    sections, lines, start = [], [], 1
    for number, line in enumerate(text.splitlines(), 1):
        if line.startswith("#") and lines:
            sections.append((f"line-{start}", "\n".join(lines)))
            lines, start = [], number
        lines.append(line)
    if lines:
        sections.append((f"line-{start}", "\n".join(lines)))
    return sections, []


def extract_docx(content: bytes) -> tuple[list[tuple[str, str]], list[str]]:
    try:
        with ZipFile(io.BytesIO(content)) as archive:
            members = archive.infolist()
            if len(members) > 2000 or sum(item.file_size for item in members) > 32 * 1024 * 1024:
                raise DocumentError("document_too_large")
            if "word/document.xml" not in archive.namelist():
                raise DocumentError("invalid_document")
            incomplete = any(item.filename.startswith("word/media/") for item in members)
        document = Document(io.BytesIO(content))
    except (BadZipFile, KeyError) as error:
        raise DocumentError("invalid_document") from error
    blocks = []
    for number, item in enumerate(document.iter_inner_content(), 1):
        if isinstance(item, Table):
            text = "\n".join(" | ".join(cell.text for cell in row.cells) for row in item.rows)
            identifier = f"table-{number}"
        else:
            text, identifier = item.text, f"paragraph-{number}"
        blocks.append((identifier, text))
    # The text extraction does not cover images, headers, footnotes or text boxes.
    xml = document.element.xml
    if "txbxContent" in xml:
        incomplete = True
    flags = ["embedded_content_not_extracted"] if incomplete else []
    if any(part.partname.startswith(("/word/header", "/word/footer", "/word/footnotes", "/word/endnotes")) for part in document.part.package.parts):
        flags.append("additional_sections_not_extracted")
    return blocks, flags


def extract_pdf(content: bytes) -> tuple[list[tuple[str, str]], list[str]]:
    reader = PdfReader(io.BytesIO(content), strict=True)
    if reader.is_encrypted:
        raise DocumentError("encrypted_document")
    if len(reader.pages) > 500:
        raise DocumentError("document_too_large")
    blocks, empty, images = [], False, False
    total = 0
    for number, page in enumerate(reader.pages, 1):
        text = page.extract_text() or ""
        total += len(text.encode("utf-8"))
        if total > MAX_TEXT_BYTES:
            raise DocumentError("document_too_large")
        if not text.strip():
            empty = True
        resources = page.get("/Resources", {})
        if resources:
            objects = resources.get_object().get("/XObject", {})
            if objects:
                images = images or any(obj.get_object().get("/Subtype") == "/Image" for obj in objects.get_object().values())
        blocks.append((f"page-{number}", text))
    flags = ["empty_pdf_pages"] if empty else []
    if images:
        flags.append("embedded_content_not_extracted")
    return blocks, flags


def extract(payload: dict) -> dict:
    if set(payload) != {"filename", "content_base64"}:
        raise DocumentError("invalid_input")
    filename, encoded = payload["filename"], payload["content_base64"]
    if not isinstance(filename, str) or not isinstance(encoded, str):
        raise DocumentError("invalid_input")
    if not filename or len(filename) > 255 or "/" in filename or "\\" in filename or "\x00" in filename:
        raise DocumentError("invalid_filename")
    extension = PurePath(filename).suffix.lower()
    parser = {".md": extract_text, ".txt": extract_text, ".docx": extract_docx, ".pdf": extract_pdf}.get(extension)
    if parser is None:
        raise DocumentError("unsupported_document")
    try:
        content = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as error:
        raise DocumentError("invalid_input") from error
    if not content or len(content) > MAX_FILE_BYTES:
        raise DocumentError("document_too_large" if content else "empty_document")
    raw_blocks, flags = parser(content)
    if sum(len(text.encode("utf-8")) for _, text in raw_blocks) > MAX_TEXT_BYTES:
        raise DocumentError("document_too_large")
    blocks = [block for identifier, text in raw_blocks for block in split_block(identifier, text)]
    if len(blocks) > MAX_BLOCKS:
        raise DocumentError("document_too_large")
    if not blocks:
        raise DocumentError("no_extractable_text")
    return {"ok": True, "blocks": blocks, "incomplete": bool(flags), "warnings": flags}


def main() -> int:
    # Parser warnings may contain document strings, never forward them to logs.
    logging.disable(logging.CRITICAL)
    warnings.simplefilter("ignore")
    try:
        payload = sys.stdin.buffer.read(MAX_STDIN_BYTES + 1)
        if len(payload) > MAX_STDIN_BYTES:
            raise DocumentError("document_too_large")
        request = json.loads(payload)
        if not isinstance(request, dict):
            raise DocumentError("invalid_input")
        result = extract(request)
    except DocumentError as error:
        result = {"ok": False, "error": str(error)}
    except (UnicodeDecodeError, json.JSONDecodeError):
        result = {"ok": False, "error": "invalid_input"}
    except Exception:
        result = {"ok": False, "error": "invalid_document"}
    sys.stdout.write(json.dumps(result, ensure_ascii=False, allow_nan=False) + "\n")
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

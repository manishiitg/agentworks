---
name: read-file
description: Extract the actual text/content from a file of ANY format — PDF, Word, PowerPoint, Excel, images, scans, archives. The canonical "how do I read this file" reference, reused by process-file, Autopilot ingestion, and browser downloads.
---

# Read any file

There is no dedicated document-reading tool — you extract the content yourself with the shell (the same approach AgentWorks uses: write a little Python and run it via `execute_shell_command`). Always start by identifying the file, then use the tool that is **already installed** for that format. Do NOT reach for a tool that isn't there (e.g. `pdftotext`/poppler and `libreoffice`/`soffice` are NOT installed on this machine — don't use them).

```
file "<path>"        # what is it, really? (extension can lie)
```

## By format

- **Plain text / Markdown / CSV / JSON / code** — just `cat "<path>"` (or `head`/`jq` for large/structured files).

- **PDF (digital, has a real text layer)** — use `pypdf` (already installed), the canonical fast path:
  ```
  python3 -c "import sys,pypdf; r=pypdf.PdfReader(sys.argv[1]); print('\n\n'.join((p.extract_text() or '') for p in r.pages))" "<path>"
  ```

- **PDF (scanned / image-only), garbled or empty pypdf output, OR any complex layout** (tables, multi-column, mixed text + figures) — parse it **locally** with **`liteparse`** (LlamaIndex's local parser — this app's OCR/parse path). Install on demand if missing, then run in **local mode** so nothing leaves the machine:
  ```
  pip3 install --break-system-packages liteparse   # or: uv pip install liteparse
  ```
  `liteparse` handles scanned pages, OCR, tables, and layout far better than plain extraction. For a single scanned page you may instead call the `read_image` tool (vision — great for handwriting/diagrams). `liteparse` and `read_image` do different jobs; use whichever fits, or both. Keep OCR/parse LOCAL — never send a file to a hosted API.

- **Word (.docx)** — `pandoc "<path>" -t plain` (pandoc is installed), or:
  ```
  python3 -c "import sys,docx; print('\n'.join(p.text for p in docx.Document(sys.argv[1]).paragraphs))" "<path>"
  ```
  (`python-docx` is installed.)

- **PowerPoint (.pptx)** — `pandoc "<path>" -t plain` (pandoc supports pptx input). For per-slide structure: `unzip -p "<path>" 'ppt/slides/*.xml'` and read the text out of the XML. For slides that are mostly images/diagrams, fall back to `liteparse` or `read_image`.

- **Excel (.xlsx)** — use `openpyxl` (install on demand if missing: `pip3 install --break-system-packages openpyxl`), then read cells with a short Python script. Quick text dump alternative: `unzip -p "<path>" 'xl/sharedStrings.xml'`.

- **Old Office (.doc/.ppt/.xls)** — no converter is installed for the legacy binary formats; `liteparse` is the most reliable path, otherwise ask the parent to re-save as the modern format.

- **Images (.png/.jpg/.jpeg/.gif/.webp/.heic — photos of notes, worksheets, handwritten homework)** — you reach files as raw bytes through the shell, so you CANNOT see a picture by `cat`-ing it. Call the **`read_image`** tool with the path: it looks at the actual image (vision) and returns transcribed text + a description — best for handwriting, layout, and diagrams. For dense printed scans where you need raw text, `liteparse` also works.

- **Archives (.zip/.tar/.gz)** — `unzip -l "<path>"` (or `tar tf`) to list, then extract and read each file inside with the rules above.

- **Audio (.oga/.ogg/.m4a/.mp3/.wav)** — a WhatsApp voice note is never one of these files: the platform transcribes it itself before the message ever reaches you, so it just reads as a normal typed message (with a low-confidence transcript flagged as such, right in the text). This step is only for an audio *file* actually uploaded another way (the attach button, filed like any other inbox item) — transcribe it locally with `faster-whisper` (install on demand: `pip3 install --break-system-packages faster-whisper`), the "small" model, `beam_size=5`:
  ```
  python3 -c "
  from faster_whisper import WhisperModel
  m = WhisperModel('small')
  segs, info = m.transcribe('<path>', beam_size=5)
  print(' '.join(s.text.strip() for s in segs))
  print(f'lang={info.language} p={info.language_probability:.2f}')
  "
  ```
  `info.language_probability` is the confidence — below ~0.6 the transcript is often garbled (mishears a name or product as ordinary words: "Veracross" as "where across" is a real example seen in practice); say so rather than trusting a shaky transcript at face value.
- **Video** — you cannot watch it. Record the filename and duration (`ffprobe "<path>"` if available) and ask the parent what it covers.

- **Anything else, or a needed tool genuinely missing** — you are resourceful: `pip3 install --break-system-packages <pkg>` (or `uv pip install <pkg>`) on demand and run it **locally**. `liteparse` is the strong general-purpose fallback for most document formats.

## Rules

- Only record content you have **actually extracted** — never invent or guess what a file contains if you couldn't read it.
- Keep everything **local** — parsing/OCR must not send the file to a hosted service.
- If extraction comes back empty or clearly wrong, say so and escalate (try `liteparse`, then `read_image`, then ask the parent) rather than pretending you read it.
- **Before re-reading a file under `materials/`, check for `<file>.meta.json`'s `extracted_text` field first** — `process-file` saves the full extraction there specifically so vision/OCR only ever runs once per file. Only fall back to actually re-reading the raw file (vision/OCR again) if that field is missing, empty, or looks truncated/wrong for what you now need (e.g. an older material processed before this field existed).

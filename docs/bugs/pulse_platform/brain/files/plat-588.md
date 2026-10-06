[← brain / files](index.md)

# PLAT-588: Brain stores any file type, not only Markdown

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | files |
| Summary | Brain stores any file (documents, media, data, source), not only Markdown; programs are refused |

## What happened

## Fix

## Left

## Why

Owner, 2026-10-06: "users should be able any kind of files .. image, pptx, excel etc anything", "just not binary files" (read as: not programs). Brain accepted only `.md` entries with 64-character stems, a design choice from the Markdown-notes MVP, not a storage limit: content is stored as bytes and backed up to Git as files. Brain skills (PLAT-576) also need scripts and assets.

## Fix

- File names: any extension; letters, digits, spaces, `. _ - + ( )`, up to 128 characters, starting with a letter or digit; no hidden names, `..`, path separators, control characters or Windows-reserved names. Folder names are unchanged.
- Programs are refused by extension (`.exe .dll .so .dylib .bin .msi .com .scr .app .apk .dmg .jar .class .elf .o .a .pkg .deb .rpm`) and by signature (ELF, PE `MZ`, Mach-O, `0xCAFEBABE`), so renaming one does not get it in.
- Text keeps every feature (10 MiB, LF line endings, line/section reads, diffs, search). Anything else goes in as `content_base64`, up to 50 MiB, is stored exactly and marked `binary`; reads return it whole as `content_base64`; diffs and line/section reads are refused for it; search skips it. Git import accepts any regular file (also mode 100755) and marks non-text as binary; the Git workspace total is 1 GiB.
- App Brain tab: a binary file shows an inline preview for images and a Download link for every file, in the existing file header. There is no upload button in the app; files are added by agents and MCP clients (`update_knowledgebase` create with `content_base64`).
- Tests: `TestEntryAndFolderNamesRejectUnsafePaths` now pins the new rule (any type accepted, programs refused by name and by signature, a PNG reads back whole).

## Left

- Not deployed. Not checked in a browser (image preview, download).
- If people should upload files in the app directly, that needs an upload control in the Brain tab.

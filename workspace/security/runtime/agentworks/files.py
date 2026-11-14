"""Fresh, read-only text from the saved workflow's own Crew.

    from agentworks import files
    note = files.read("knowledgebase/notes/owner-handled.md")

Paths are relative to this Crew's Files. Private files, other Crews, symlinks
and text larger than 256 KiB are refused by the existing server file reader.
Native coding agents use their own file tools; this helper is for parent Python.
"""
from agentworks_db import DBError, _call

from . import AutomationError


def read(path: str) -> str:
    """Return current UTF-8 text; each call reads again, including on Resume."""
    if not isinstance(path, str):
        raise TypeError("Choose a relative file path in this Crew.")
    try:
        result = _call("crew_files_read", {"path": path})
    except DBError as error:
        raise AutomationError(str(error)) from error
    if not isinstance(result, dict) or not isinstance(result.get("content"), str):
        raise AutomationError("The file could not be read. Choose a text file from this Crew's Files.")
    return result["content"]

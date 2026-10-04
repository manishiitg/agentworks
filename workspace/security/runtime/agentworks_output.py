"""Structured output for sandboxed Python steps.

Call set_output(value); the platform persists JSON in the assigned step output
directory. Printing remains a log. Serialization and persistence errors raise.
"""

import json
import os
import tempfile


def set_output(value):
    """Publish a JSON-compatible value for downstream steps.

    Multiple successful calls replace the output; the last value wins. A failed
    serialization or write leaves the previous output intact and raises.
    """
    output_dir = os.environ.get("STEP_OUTPUT_DIR")
    if not output_dir or not os.path.isabs(output_dir):
        raise RuntimeError("set_output requires an absolute STEP_OUTPUT_DIR")
    # Serialize before touching an existing output. NaN/Infinity are not JSON.
    payload = json.dumps(value, ensure_ascii=False, allow_nan=False,
                         separators=(",", ":")).encode("utf-8")
    path = None
    try:
        with tempfile.NamedTemporaryFile(mode="wb", dir=output_dir,
                                         prefix=".result-", delete=False) as file:
            path = file.name
            # The service must read slot-created artifacts through the shared
            # workspace group; temporary files otherwise default to 0600.
            os.fchmod(file.fileno(), 0o660)
            file.write(payload)
            file.flush()
            os.fsync(file.fileno())
        os.replace(path, os.path.join(output_dir, "result.json"))
        path = None
    finally:
        if path is not None:
            os.unlink(path)

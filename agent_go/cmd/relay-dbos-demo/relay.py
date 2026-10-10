"""Fixed demo release. Fault injection is test instrumentation, not workflow logic."""
import asyncio
import json
import os
from relay_sdk import tool, _json_write


def crash(ctx, where):
    _json_write(ctx.run_dir / "fault.json", {"where": where})
    os._exit(97)


async def run(INPUT, ctx):
    @tool
    async def lookup(order_id: str):
        """Read the demo order through a real Python tool callback."""
        return {"order_id": order_id, "amount": 2400, "currency": "INR"}

    order = await ctx.call_agent(
        name="extract", system_prompt="Extract the order using lookup.",
        user_message=json.dumps(INPUT), tools=[lookup],
    )

    async def save_order(idempotency_key):
        # This file represents an external service's idempotency ledger.
        # Calls may repeat; creating the order is deduplicated by the key.
        ledger = ctx.run_dir / "service.json"
        if not ledger.exists():
            _json_write(ledger, {"idempotency_key": idempotency_key,
                                 "effects": 1, "order": order})
        with (ctx.run_dir / "service-attempts.txt").open("a") as attempts:
            attempts.write("attempt\n")
        await asyncio.sleep(1.3)
        if os.environ.get("DEMO_FAULT") == "during":
            crash(ctx, "during-service")
        return json.loads(ledger.read_text(encoding="utf-8"))

    saved = await ctx.step(
        "save", save_order, arguments={"idempotency_key": INPUT["order_id"]},
        replay_safe=INPUT["service_replay_safe"],
    )
    if os.environ.get("DEMO_FAULT") == "after":
        crash(ctx, "after-checkpoint")

    reviewed = await ctx.call_agent(
        name="review", system_prompt="Review the saved order.",
        user_message=json.dumps(saved),
    )
    delivery = await ctx.call_mcp(
        server="demo", tool="fetch", arguments={"order_id": INPUT["order_id"]},
    )
    return {"order_id": INPUT["order_id"], "review": reviewed,
            "delivery": delivery, "service_effects": saved["effects"]}

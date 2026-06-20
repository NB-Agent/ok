"""Basic example: connect to an OK agent and run a query."""
import sys
import time

from ok import Agent


def main():
    agent = Agent()

    # Check if the agent is running
    if not agent.connect():
        print("❌ Agent not reachable at http://127.0.0.1:3030")
        print("")
        print("Start the OK agent with:")
        print("  ok serve")
        print("")
        print("Or specify a custom address:")
        print("  from ok import Agent, AgentConfig")
        print('  agent = Agent(AgentConfig(base_url="http://localhost:8080"))')
        sys.exit(1)

    print("✅ Connected to OK Agent\n")

    # Example 1: Simple query
    print("=" * 60)
    print("Example 1: Simple query")
    print("=" * 60)
    result = agent.run("What is 2+2? Explain briefly.")
    print(f"\nAnswer: {result.text}")
    if result.usage:
        print(f"Tokens: {result.usage['total_tokens']} | "
              f"Cost: ${result.usage.get('cost_usd', 0):.4f}")
    print()

    # Example 2: With tool calls (auto-approved)
    print("=" * 60)
    print("Example 2: Tool call (auto-approved)")
    print("=" * 60)
    result = agent.run(
        "What's the current date and time? Use the bash tool to check.",
        auto_approve=True,
    )
    print(f"\nAnswer: {result.text}")
    if result.tool_calls:
        print(f"Tools used: {[t['name'] for t in result.tool_calls]}")
    print()

    # Example 3: Session management
    print("=" * 60)
    print("Example 3: Session info")
    print("=" * 60)
    ctx = agent.get_context()
    print(f"Context window: {ctx.get('used', '?')}/{ctx.get('window', '?')} tokens")

    history = agent.get_history()
    print(f"History messages: {len(history)}")

    # Example 4: Streaming events
    print("\n" + "=" * 60)
    print("Example 4: Event streaming")
    print("=" * 60)
    print()
    agent.submit("Say hello in one sentence.")
    for event in agent.stream(auto_approve=True):
        if event.kind == event.TEXT:
            print(event.text, end="", flush=True)
        elif event.kind == event.DONE:
            print()
            break
        elif event.kind == event.USAGE and event.usage:
            print(f"\n[Tokens: {event.usage.total_tokens}]")

    # Clean up
    agent.close()
    print("\n✅ Done")


if __name__ == "__main__":
    main()

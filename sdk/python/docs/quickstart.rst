Quickstart
==========

Installation
------------

.. code-block:: bash

    pip install ok-sdk

Or with WebSocket support:

.. code-block:: bash

    pip install "ok-sdk[ws]"

Prerequisites
-------------

Start an OK agent serve endpoint:

.. code-block:: bash

    ok serve

This starts the agent at ``http://127.0.0.1:3030``.

Synchronous Usage
-----------------

.. code-block:: python

    from ok import Agent

    agent = Agent()
    if agent.connect():
        result = agent.run("What is 2+2?")
        print(result.text)
        print(f"Tokens: {result.usage}")

Streaming Events
~~~~~~~~~~~~~~~~

.. code-block:: python

    from ok import Agent, TEXT, TOOL_DISPATCH, USAGE

    agent = Agent()
    agent.submit("List files in the current directory")

    for event in agent.stream(auto_approve=True):
        if event.kind == TEXT:
            print(event.text, end="")
        elif event.kind == TOOL_DISPATCH:
            print(f"[tool: {event.tool.name}]")

Async Usage
-----------

.. code-block:: python

    import asyncio
    from ok import AsyncAgent

    async def main():
        async with AsyncAgent() as agent:
            result = await agent.run("Hello!")
            print(result.text)

    asyncio.run(main())

WebSocket Transport
-------------------

.. code-block:: python

    from ok import WebSocketTransport, WSConfig, Agent

    ws = WebSocketTransport(WSConfig())
    agent = Agent(transport=ws)
    result = agent.run("Hello via WebSocket!")

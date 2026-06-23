API Reference
=============

Agent (Sync)
------------

.. autoclass:: ok.Agent
   :members:
   :special-members: __enter__, __exit__

AgentConfig
~~~~~~~~~~~

.. autoclass:: ok.AgentConfig
   :members:

RunResult
~~~~~~~~~

.. autoclass:: ok.RunResult
   :members:

AsyncAgent
----------

.. autoclass:: ok.AsyncAgent
   :members:
   :special-members: __aenter__, __aexit__

AsyncAgentTransport
~~~~~~~~~~~~~~~~~~~

.. autoclass:: ok.AsyncAgentTransport
   :members:

AgentTransport (Sync)
---------------------

.. autoclass:: ok.AgentTransport
   :members:

TransportConfig
~~~~~~~~~~~~~~~

.. autoclass:: ok.TransportConfig
   :members:

WebSocketTransport
------------------

.. autoclass:: ok.WebSocketTransport
   :members:

Event Types
-----------

.. autoclass:: ok.Event
   :members:

.. autoclass:: ok.ToolCall
   :members:

.. autoclass:: ok.Usage
   :members:

.. autoclass:: ok.ApprovalRequest
   :members:

.. autoclass:: ok.AskQuestion
   :members:

.. autoclass:: ok.AskOption
   :members:

.. autoclass:: ok.AskState
   :members:

Event Kind Constants
--------------------

.. autodata:: ok.TURN_STARTED
.. autodata:: ok.REASONING
.. autodata:: ok.TEXT
.. autodata:: ok.MESSAGE
.. autodata:: ok.TOOL_DISPATCH
.. autodata:: ok.TOOL_RESULT
.. autodata:: ok.USAGE
.. autodata:: ok.APPROVAL
.. autodata:: ok.ASK
.. autodata:: ok.ERROR_EVENT
.. autodata:: ok.DONE
.. autodata:: ok.CANCELLED

Exceptions
----------

.. autoclass:: ok.TransportError
   :members:

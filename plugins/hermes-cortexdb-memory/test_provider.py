import importlib.util
import json
import pathlib
import sys
import types
import unittest


class MemoryProvider:
    pass


agent = types.ModuleType("agent")
memory_provider = types.ModuleType("agent.memory_provider")
memory_provider.MemoryProvider = MemoryProvider
agent.memory_provider = memory_provider
sys.modules.setdefault("agent", agent)
sys.modules.setdefault("agent.memory_provider", memory_provider)


class CallToolRequest:
    def __init__(self, name="", args_json=""):
        self.name = name
        self.args_json = args_json


proto = types.SimpleNamespace(CallToolRequest=CallToolRequest)
cortexdb_client = types.ModuleType("cortexdb_client")
cortexdb_client.CortexClient = object
cortexdb_client.proto = proto
sys.modules.setdefault("cortexdb_client", cortexdb_client)

root = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("cortexdb_hermes", root / "__init__.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class FakeTools:
    def __init__(self):
        self.calls = []

    def CallTool(self, request, metadata=None):
        self.calls.append(request)
        self.metadata = metadata
        return types.SimpleNamespace(result_json=json.dumps({
            "context_pack": {"text": "stored context"},
        }))


class ProviderTest(unittest.TestCase):
    def test_prefetch_uses_unified_recall(self):
        provider = module.CortexDBMemoryProvider()
        tools = FakeTools()
        provider._client = types.SimpleNamespace(tools=tools)
        context = provider.prefetch("coffee")
        self.assertIn("stored context", context)
        self.assertEqual(tools.calls[0].name, "knowledge_memory_recall")

    def test_tools_are_registered(self):
        provider = module.CortexDBMemoryProvider()
        names = [schema["name"] for schema in provider.get_tool_schemas()]
        self.assertEqual(names, ["cortexdb_search", "cortexdb_remember", "cortexdb_forget"])



class ParseResultTest(unittest.TestCase):
    def test_a_reply_that_is_not_json_is_reported_by_what_it_was(self):
        with self.assertRaisesRegex(ValueError, r"knowledge_memory_recall: reply is not JSON \(\d+ bytes, request_id r1\): Error: too large"):
            module._parse_tool_result("knowledge_memory_recall", "Error: too large", "r1")
        self.assertEqual(module._parse_tool_result("t", ""), {})

if __name__ == "__main__":
    unittest.main()

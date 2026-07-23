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

    def CallTool(self, request):
        self.calls.append(request)
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


if __name__ == "__main__":
    unittest.main()

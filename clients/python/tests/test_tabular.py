"""Tests for cortexdb_client.tabular. Fixtures only: no sidecar, no network.

The fixtures follow the Go response structs' JSON tags
(pkg/cortexdb: CypherQueryResponse, ToolAggregateMetadataResponse,
MemorySearchResponse, KnowledgeSearchResponse, KnowledgeMemoryRecallResponse,
MemoryListAllResponse, GraphListAllResponse, RecallLogListResponse).

Run: uv run python -m unittest discover -s tests -p "test_tabular.py" -v
     uv run --extra pandas python -m unittest discover -s tests -p "test_tabular.py" -v
"""

import builtins
import csv
import io
import json
import os
import tempfile
import unittest
from unittest import mock

from cortexdb_client import proto, tabular

try:
    import pandas  # noqa: F401
    HAVE_PANDAS = True
except ImportError:
    HAVE_PANDAS = False

CYPHER = {
    "columns": ["n", "r", "city", "tags"],
    "rows": [
        [
            {"id": "entity:alice", "labels": ["person"], "content": "Alice", "properties": {"name": "Alice"}},
            {"id": "e1", "type": "knows", "start": "entity:alice", "end": "entity:bob", "weight": 1, "properties": {}},
            "Vienna",
            ["a", "b"],
        ],
        [{"id": "entity:bob", "labels": ["person"], "content": "Bob", "properties": {}}, None, None, []],
    ],
    "row_count": 2,
    "stats": {"sql_statements": 1, "candidate_rows": 2, "elapsed_ms": 0},
}

AGGREGATE = {
    "results": [
        {"keys": {"team": "platform", "year": 2026}, "value": 2, "count": 2},
        {"keys": {"team": "data", "year": 2026}, "value": 1, "count": 1},
    ],
    "total": 2,
}

MEMORY = {
    "id": "m0",
    "user_id": "u1",
    "session_id": "memory:user:u1:default",
    "scope": "user",
    "namespace": "default",
    "role": "memory",
    "content": "User prefers 4-space indentation.",
    "metadata": {"kind": "memory", "project": "alpha", "importance": 0.8, "tags": ["style"], "nested": {"a": 1}},
    "importance": 0.8,
    "created_at": "2026-11-14T14:31:39Z",
}

MEMORY_SEARCH = {
    "query": "indentation",
    "plan": {"query": "indentation", "retrieval_mode": "auto"},
    "decision": {"requested_mode": "auto", "effective_mode": "lexical", "use_graph": False},
    "results": [{"memory": MEMORY, "score": 0.85, "coverage": 1, "via": ["keyword"]}],
}

KNOWLEDGE_HIT = {
    "knowledge_id": "k0",
    "title": "Indentation guide",
    "snippet": "Python code uses 4-space indentation.",
    "score": 0.33,
    "rank": 1,
    "chunk_ids": ["chunk:k0:000"],
    "metadata": {"team": "platform"},
}

RECALL = {
    "query": "indentation",
    "memory_plan": {"query": "indentation"},
    "memory_decision": {"effective_mode": "lexical"},
    "knowledge_plan": {"query": "indentation"},
    "knowledge_decision": {"effective_mode": "ppr"},
    "memories": [{"memory": MEMORY, "score": 0.88, "via": ["keyword"]}],
    "knowledge": [KNOWLEDGE_HIT],
    # GraphRAGChunkResult has no JSON tags, so its keys are the Go field names.
    "chunks": [{"ID": "chunk:k0:000", "DocumentID": "k0", "Content": "…", "Score": 0.33, "Entities": ["Python"]}],
    "entities": ["Python"],
    "graph_facts": [{"subject": "Alice", "predicate": "knows", "object": "Bob"}],
    "context_pack": {"text": "…"},
}

MEMORY_LIST = {"memories": [MEMORY, dict(MEMORY, id="m1", metadata={})], "truncated": True, "next_cursor": "abc"}

GRAPH_LIST = {
    "nodes": [{"id": "entity:alice", "label": "Alice", "type": "person", "degree": 2}],
    "edges": [{"from": "entity:alice", "to": "entity:bob", "type": "knows"}],
    "total_nodes": 1,
}


class FlattenTest(unittest.TestCase):
    def test_nested_objects_join_keys_and_lists_become_json(self):
        got = tabular.flatten({"a": 1, "b": {"c": {"d": "x"}, "e": None}, "f": [1, {"g": 2}], "h": {}})
        self.assertEqual(got, {"a": 1, "b.c.d": "x", "b.e": None, "f": '[1,{"g":2}]'})

    def test_lists_keep_and_custom_separator(self):
        got = tabular.flatten({"b": {"c": [1, 2]}}, sep="__", lists="keep")
        self.assertEqual(got, {"b__c": [1, 2]})

    def test_unknown_list_mode_is_refused(self):
        with self.assertRaises(ValueError):
            tabular.flatten({}, lists="explode")

    def test_non_ascii_survives_in_json_lists(self):
        self.assertEqual(tabular.flatten({"t": ["记忆"]}), {"t": '["记忆"]'})


class RecordsTest(unittest.TestCase):
    def test_cypher_rows_zip_columns_and_flatten_nodes(self):
        rows = tabular.records(CYPHER)
        self.assertEqual(len(rows), 2)
        self.assertEqual(rows[0]["n.id"], "entity:alice")
        self.assertEqual(rows[0]["n.labels"], '["person"]')
        self.assertEqual(rows[0]["n.properties.name"], "Alice")
        self.assertEqual(rows[0]["r.type"], "knows")
        self.assertEqual(rows[0]["city"], "Vienna")
        self.assertEqual(rows[0]["tags"], '["a","b"]')
        self.assertIsNone(rows[1]["r"])
        self.assertNotIn("n.properties.name", rows[1])

    def test_cypher_without_rows(self):
        self.assertEqual(tabular.records({"columns": ["a"], "rows": None, "row_count": 0}), [])
        self.assertEqual(tabular.records({"columns": ["a"], "rows": [], "row_count": 0}), [])

    def test_aggregate_group_keys_lead_then_measures(self):
        rows = tabular.records(AGGREGATE)
        self.assertEqual(rows[0], {"team": "platform", "year": 2026, "value": 2, "count": 2})
        self.assertEqual(list(rows[0]), ["team", "year", "value", "count"])

    def test_aggregate_takes_measures_it_has_never_heard_of(self):
        future = {
            "results": [
                {
                    "keys": {"team": "data"},
                    "bucket": "2026-11",
                    "count": 4,
                    "sum_tokens": 120,
                    "avg_score": 0.5,
                    "measures": {"p95": 9.5},
                }
            ],
            "total": 1,
        }
        self.assertEqual(
            tabular.records(future),
            [{"team": "data", "bucket": "2026-11", "count": 4, "sum_tokens": 120, "avg_score": 0.5, "measures.p95": 9.5}],
        )

    def test_aggregate_without_groups_and_empty(self):
        self.assertEqual(tabular.records({"results": [{"value": 3, "count": 3}], "total": 1}), [{"value": 3, "count": 3}])
        self.assertEqual(tabular.records({"results": [], "total": 0}), [])
        self.assertEqual(tabular.records({"results": None, "total": 0}), [])

    def test_group_key_colliding_with_a_measure_keeps_its_prefix(self):
        rows = tabular.records({"results": [{"keys": {"count": "many", "team": "x"}, "count": 2}]})
        self.assertEqual(rows, [{"keys.count": "many", "team": "x", "count": 2}])

    def test_memory_hits_lift_the_memory(self):
        (row,) = tabular.records(MEMORY_SEARCH)
        self.assertEqual(row["id"], "m0")
        self.assertEqual(row["score"], 0.85)
        self.assertEqual(row["via"], '["keyword"]')
        self.assertEqual(row["metadata.project"], "alpha")
        self.assertEqual(row["metadata.nested.a"], 1)
        self.assertEqual(row["metadata.tags"], '["style"]')
        self.assertNotIn("memory", row)
        self.assertNotIn("plan", row)

    def test_recall_needs_a_table_and_names_them(self):
        self.assertEqual(tabular.tables(RECALL), ["memories", "knowledge", "chunks", "graph_facts"])
        with self.assertRaises(ValueError) as ctx:
            tabular.records(RECALL)
        self.assertIn("memories", str(ctx.exception))
        self.assertEqual(tabular.records(RECALL, "memories")[0]["id"], "m0")
        self.assertEqual(tabular.records(RECALL, "knowledge")[0]["metadata.team"], "platform")
        self.assertEqual(tabular.records(RECALL, "chunks")[0]["DocumentID"], "k0")
        self.assertEqual(tabular.records(RECALL, "graph_facts")[0]["predicate"], "knows")

    def test_knowledge_search_defaults_to_results(self):
        result = {"query": "q", "results": [KNOWLEDGE_HIT], "chunks": [{"ID": "c"}]}
        self.assertEqual(tabular.records(result)[0]["knowledge_id"], "k0")

    def test_listings(self):
        rows = tabular.records(MEMORY_LIST)
        self.assertEqual([r["id"] for r in rows], ["m0", "m1"])
        self.assertNotIn("metadata.project", rows[1])
        with self.assertRaises(ValueError):
            tabular.records(GRAPH_LIST)
        self.assertEqual(tabular.records(GRAPH_LIST, "nodes")[0]["degree"], 2)
        self.assertEqual(tabular.records(GRAPH_LIST, table="edges")[0], {"from": "entity:alice", "to": "entity:bob", "type": "knows"})

    def test_unknown_table_and_no_table(self):
        with self.assertRaises(ValueError):
            tabular.records(GRAPH_LIST, "memories")
        with self.assertRaises(ValueError):
            tabular.records({"ok": True})
        with self.assertRaises(ValueError):
            tabular.records({"entities": ["a", "b"]}, "entities")
        with self.assertRaises(TypeError):
            tabular.records(42)

    def test_accepts_response_json_text_bytes_and_bare_list(self):
        text = json.dumps(AGGREGATE)
        want = tabular.records(AGGREGATE)
        self.assertEqual(tabular.records(proto.CallToolResponse(result_json=text)), want)
        self.assertEqual(tabular.records(text), want)
        self.assertEqual(tabular.records(text.encode()), want)
        self.assertEqual(tabular.records(AGGREGATE["results"]), want)
        self.assertEqual(tabular.records([]), [])
        with self.assertRaises(ValueError):
            tabular.records(proto.CallToolResponse())

    def test_columns_are_first_seen_order(self):
        self.assertEqual(tabular.columns([{"a": 1}, {"b": 2, "a": 3}, {"c": 4}]), ["a", "b", "c"])


class CsvTest(unittest.TestCase):
    def test_string_has_header_union_and_empty_cells(self):
        text = tabular.to_csv(MEMORY_LIST)
        parsed = list(csv.DictReader(io.StringIO(text)))
        self.assertEqual(len(parsed), 2)
        self.assertEqual(parsed[0]["metadata.project"], "alpha")
        self.assertEqual(parsed[1]["metadata.project"], "")
        self.assertEqual(parsed[0]["metadata.tags"], '["style"]')

    def test_path_and_file_object(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "groups.csv")
            self.assertIsNone(tabular.to_csv(AGGREGATE, path))
            with open(path, encoding="utf-8") as handle:
                self.assertEqual(handle.read(), "team,year,value,count\nplatform,2026,2,2\ndata,2026,1,1\n")
        buffer = io.StringIO()
        tabular.to_csv(GRAPH_LIST, buffer, table="edges")
        self.assertEqual(buffer.getvalue(), "from,to,type\nentity:alice,entity:bob,knows\n")

    def test_no_rows_is_an_empty_file(self):
        self.assertEqual(tabular.to_csv({"results": []}).strip(), "")


class PandasTest(unittest.TestCase):
    def test_missing_pandas_says_how_to_install_it(self):
        real_import = builtins.__import__

        def no_pandas(name, *args, **kwargs):
            if name == "pandas" or name.startswith("pandas."):
                raise ImportError("No module named 'pandas'")
            return real_import(name, *args, **kwargs)

        with mock.patch.dict("sys.modules", {"pandas": None}), mock.patch("builtins.__import__", no_pandas):
            with self.assertRaises(ImportError) as ctx:
                tabular.to_pandas(AGGREGATE)
        self.assertIn("cortexdb-client[pandas]", str(ctx.exception))

    def test_importing_the_package_does_not_import_pandas(self):
        import subprocess
        import sys

        code = "import sys, cortexdb_client; sys.exit(1 if 'pandas' in sys.modules else 0)"
        self.assertEqual(subprocess.run([sys.executable, "-B", "-c", code], env={"PATH": os.environ.get("PATH", "")}).returncode, 0)

    @unittest.skipUnless(HAVE_PANDAS, "pandas not installed (run with --extra pandas)")
    def test_dataframes(self):
        df = tabular.to_pandas(AGGREGATE)
        self.assertEqual(list(df.columns), ["team", "year", "value", "count"])
        self.assertEqual(int(df["count"].sum()), 3)

        df = tabular.to_pandas(MEMORY_LIST)
        self.assertEqual(df.shape[0], 2)
        self.assertTrue(pandas.isna(df.loc[1, "metadata.project"]))

        df = tabular.to_pandas(RECALL, "memories", lists="keep")
        self.assertEqual(df.loc[0, "via"], ["keyword"])

        df = tabular.to_pandas(CYPHER)
        self.assertEqual(df.loc[0, "n.id"], "entity:alice")

        self.assertEqual(tabular.to_pandas({"results": []}).shape, (0, 0))


if __name__ == "__main__":
    unittest.main()

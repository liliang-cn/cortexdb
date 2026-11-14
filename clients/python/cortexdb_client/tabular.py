"""Tool results as tables: flat records, a pandas DataFrame, or CSV.

A tool called through ``client.tools.CallTool`` answers with one JSON object.
The row-shaped part of it — a Cypher query's rows, the groups of
``aggregate_metadata``, the hits of a search or a recall, a listing — is a
list under some key. :func:`records` picks that list and returns it as flat
``dict`` records, one per row, ready for ``csv``, pandas, polars or DuckDB::

    import json
    from cortexdb_client import CortexClient, proto, tabular

    with CortexClient.connect("127.0.0.1:47821") as client:
        res = client.tools.CallTool(proto.CallToolRequest(
            name="graph_cypher_query",
            args_json=json.dumps({"query": "MATCH (n) RETURN n LIMIT 100"}),
        ))
    rows = tabular.records(res)       # [{"n.id": "...", "n.labels": "[...]", ...}, ...]
    df = tabular.to_pandas(res)       # needs: pip install "cortexdb-client[pandas]"
    tabular.to_csv(res, "nodes.csv")

Which list is the table
-----------------------

``records(result, table=None)`` chooses, in this order:

1. ``table``, when given — the name of a top-level key (``"memories"``,
   ``"knowledge"``, ``"chunks"``, ``"graph_facts"``, ``"nodes"``, ``"edges"``,
   ``"recalls"`` …). :func:`tables` lists the names a result has.
2. ``rows``, when the result also has ``columns`` (``graph_cypher_query``).
3. ``results``, when present (``aggregate_metadata``, ``memory_search``,
   ``knowledge_search``).
4. The only top-level list of objects, when there is exactly one
   (``memory_list_all`` → ``memories``, ``search_text`` → ``chunks``).

Anything else raises ``ValueError`` naming the tables to choose from —
``graph_list_all`` has ``nodes`` and ``edges``, ``knowledge_memory_recall`` has
``memories``, ``knowledge``, ``chunks`` and ``graph_facts``.

How a row is flattened
----------------------

* A scalar (string, number, bool, ``None``) is a column under its key.
* A nested object becomes one column per leaf, the keys joined with ``.``:
  ``{"metadata": {"project": "x"}}`` → ``metadata.project``. An empty object
  adds no column.
* A list becomes one column holding its compact JSON text (``lists="json"``,
  the default — safe for CSV), or is kept as a Python list (``lists="keep"``,
  for ``DataFrame.explode``).
* Two wrappers are lifted so their fields are columns in their own right:
  the ``keys`` of an ``aggregate_metadata`` group (``{"keys": {"project":
  "x"}, "count": 3}`` → ``project``, ``count``) and the ``memory`` of a memory
  hit (``{"memory": {"id": "m1"}, "score": 0.4}`` → ``id``, ``score``).
  Lifted columns come first. One that would collide with another column
  keeps its prefix (``keys.count``). Every other key of a group — today
  ``value`` and ``count`` — is a column whatever it is called.
* A Cypher row is its ``columns`` zipped with its values; a node or
  relationship value is flattened under the column name (``n.id``,
  ``n.properties.city``).

Rows need not share columns: a key absent from a row is absent from its
record. :func:`to_csv` and pandas fill the gap (empty cell, ``NaN``).
"""

from __future__ import annotations

import csv
import io
import json
import os
from typing import IO, TYPE_CHECKING, Any, Dict, Iterable, List, Mapping, Optional, Union

if TYPE_CHECKING:  # pragma: no cover - typing only
    import pandas

__all__ = ["Record", "flatten", "records", "tables", "columns", "to_pandas", "to_csv"]

#: One flat row: column name → scalar (or list, with ``lists="keep"``).
Record = Dict[str, Any]

# Wrappers whose fields are lifted to top-level columns (see module docstring).
_LIFTED = ("keys", "memory")

_LIST_MODES = ("json", "keep")


def _dumps(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True, default=str)


def flatten(obj: Mapping[str, Any], *, sep: str = ".", lists: str = "json", prefix: str = "") -> Record:
    """Flatten one nested object into a single-level record.

    Nested objects become ``parent<sep>child`` columns, lists become compact
    JSON text (``lists="json"``) or stay lists (``lists="keep"``), scalars are
    copied. No wrapper is lifted here; see :func:`records` for that.
    """
    if lists not in _LIST_MODES:
        raise ValueError(f"lists must be one of {_LIST_MODES}, got {lists!r}")
    out: Record = {}
    _flatten_into(out, obj, prefix, sep, lists)
    return out


def _flatten_into(out: Record, obj: Mapping[str, Any], prefix: str, sep: str, lists: str) -> None:
    for key, value in obj.items():
        name = f"{prefix}{sep}{key}" if prefix else str(key)
        _put(out, name, value, sep, lists)


def _put(out: Record, name: str, value: Any, sep: str, lists: str) -> None:
    if isinstance(value, Mapping):
        _flatten_into(out, value, name, sep, lists)
    elif isinstance(value, (list, tuple)):
        out[name] = _dumps(value) if lists == "json" else list(value)
    else:
        out[name] = value


def _load(result: Any) -> Any:
    """Decode what a tool call returned into plain Python data."""
    payload = getattr(result, "result_json", None)  # proto.CallToolResponse
    if payload is not None:
        result = payload
    if isinstance(result, (bytes, bytearray)):
        result = bytes(result).decode("utf-8")
    if isinstance(result, str):
        if not result.strip():
            return {}
        return json.loads(result)
    return result


def _is_table(value: Any) -> bool:
    return isinstance(value, list) and all(isinstance(item, Mapping) for item in value)


def _table_names(data: Mapping[str, Any]) -> List[str]:
    names = [key for key, value in data.items() if key != "rows" and _is_table(value)]
    if "columns" in data and "rows" in data:
        names.insert(0, "rows")
    return names


def tables(result: Any) -> List[str]:
    """Names of the tables in a tool result, in the order the result has them.

    A table is a top-level list of objects, or the ``rows`` of a Cypher
    answer. A list the server left out because it was empty is not named.
    """
    data = _load(result)
    if isinstance(data, list):
        return []
    if not isinstance(data, Mapping):
        raise TypeError(f"expected a tool result object, got {type(data).__name__}")
    return _table_names(data)


def _pick(data: Mapping[str, Any], table: Optional[str]) -> str:
    if table is not None:
        if table not in data:
            raise ValueError(f"no table {table!r} in this result; it has {_table_names(data) or 'none'}")
        return table
    if "columns" in data and "rows" in data:
        return "rows"
    if "results" in data:
        return "results"
    names = _table_names(data)
    if len(names) == 1:
        return names[0]
    if not names:
        raise ValueError("this result has no list of rows to tabulate")
    raise ValueError(f"this result has several tables, pass table=: {names}")


def _lifted_row(row: Mapping[str, Any], sep: str, lists: str) -> Record:
    rest = {k: v for k, v in row.items() if not (k in _LIFTED and isinstance(v, Mapping))}
    own = flatten(rest, sep=sep, lists=lists)
    out: Record = {}
    for wrapper in _LIFTED:  # lifted fields lead: group keys, then measures
        inner = row.get(wrapper)
        if not isinstance(inner, Mapping):
            continue
        for name, value in flatten(inner, sep=sep, lists=lists).items():
            out[f"{wrapper}{sep}{name}" if name in own or name in out else name] = value
    out.update(own)
    return out


def records(result: Any, table: Optional[str] = None, *, sep: str = ".", lists: str = "json") -> List[Record]:
    """The rows of a tool result as flat records.

    ``result`` is what ``client.tools.CallTool`` returned (a
    ``CallToolResponse``), its ``result_json`` text, or the decoded object.
    A bare list of objects is taken as the table itself. See the module
    docstring for how the table is chosen and how a row is flattened.
    """
    if lists not in _LIST_MODES:
        raise ValueError(f"lists must be one of {_LIST_MODES}, got {lists!r}")
    data = _load(result)
    if isinstance(data, list):
        rows: Any = data
    elif isinstance(data, Mapping):
        name = _pick(data, table)
        rows = data.get(name)
        if name == "rows" and "columns" in data:
            return _cypher_records(data.get("columns") or [], rows or [], sep, lists)
    else:
        raise TypeError(f"expected a tool result object, got {type(data).__name__}")
    if rows is None:  # a Go nil slice arrives as null
        return []
    if not _is_table(rows):
        raise ValueError(f"table {table!r} is not a list of objects")
    return [_lifted_row(row, sep, lists) for row in rows]


def _cypher_records(cols: Iterable[Any], rows: Iterable[Any], sep: str, lists: str) -> List[Record]:
    names = [str(c) for c in cols]
    out: List[Record] = []
    for row in rows:
        record: Record = {}
        for name, value in zip(names, row):
            _put(record, name, value, sep, lists)
        out.append(record)
    return out


def columns(rows: Iterable[Mapping[str, Any]]) -> List[str]:
    """Every column of ``rows``, in the order each first appears."""
    seen: Dict[str, None] = {}
    for row in rows:
        for name in row:
            seen.setdefault(name, None)
    return list(seen)


def to_pandas(result: Any, table: Optional[str] = None, *, sep: str = ".", lists: str = "json") -> "pandas.DataFrame":
    """The rows of a tool result as a ``pandas.DataFrame``.

    pandas is optional: ``pip install "cortexdb-client[pandas]"``. Columns
    come in first-seen order; a key a row lacks is ``NaN`` there.
    """
    try:
        import pandas as pd
    except ImportError as exc:
        raise ImportError(
            "to_pandas() needs pandas, which cortexdb-client does not install by default. "
            'Install it with: pip install "cortexdb-client[pandas]"  (or: uv add "cortexdb-client[pandas]"). '
            "records() returns the same rows as plain dicts without it."
        ) from exc
    rows = records(result, table, sep=sep, lists=lists)
    return pd.DataFrame.from_records(rows, columns=columns(rows))


def to_csv(
    result: Any,
    dest: Union[str, "os.PathLike[str]", IO[str], None] = None,
    table: Optional[str] = None,
    *,
    sep: str = ".",
) -> Optional[str]:
    """Write the rows of a tool result as CSV, header first.

    ``dest`` is a path, an open text file, or ``None`` to get the CSV back as
    a string. Lists are written as JSON text and a missing value as an empty
    cell. Uses only the standard library. A path is written as UTF-8.
    """
    rows = records(result, table, sep=sep, lists="json")
    header = columns(rows)

    def write(handle: IO[str]) -> None:
        writer = csv.DictWriter(handle, fieldnames=header, restval="", lineterminator="\n")
        writer.writeheader()
        writer.writerows(rows)

    if dest is None:
        buffer = io.StringIO()
        write(buffer)
        return buffer.getvalue()
    if isinstance(dest, (str, os.PathLike)):
        with open(dest, "w", encoding="utf-8", newline="") as handle:
            write(handle)
        return None
    write(dest)
    return None
